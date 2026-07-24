//go:build integration

package bob

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	dbsqlc "github.com/hansonyu183/zerp-back/internal/database/sqlc"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	integrationActorOne = "01J00000000000000000000000"
	integrationActorTwo = "01J00000000000000000000001"
)

func integrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required")
	}
	testDatabaseName := strings.TrimSpace(os.Getenv("TEST_POSTGRES_DB"))
	if testDatabaseName == "" {
		t.Fatal("TEST_POSTGRES_DB is required")
	}
	if !strings.HasSuffix(testDatabaseName, "_test") {
		t.Fatalf("TEST_POSTGRES_DB %q must end with _test", testDatabaseName)
	}
	pool, err := pgxpool.New(t.Context(), databaseURL)
	if err != nil {
		t.Fatalf("connect integration database: %v", err)
	}
	t.Cleanup(pool.Close)

	var currentDatabase string
	if err = pool.QueryRow(t.Context(), "select current_database()").Scan(&currentDatabase); err != nil {
		t.Fatalf("read integration database name: %v", err)
	}
	if currentDatabase != testDatabaseName {
		t.Fatalf("connected database %q does not match TEST_POSTGRES_DB %q", currentDatabase, testDatabaseName)
	}

	var table *string
	if err = pool.QueryRow(t.Context(), "select to_regclass('bob_objects')::text").Scan(&table); err != nil || table == nil {
		t.Fatalf("BOB migrations are not applied: table=%v err=%v", table, err)
	}
	return pool
}

func TestLifecycleIntegration(t *testing.T) {
	pool := integrationPool(t)
	service := NewService(pool)
	code := "IT" + newID()
	created, err := service.Create(t.Context(), EntityCustomer, CreateInput{Data: CreateDetailInput{
		Code: code, Name: "Integration Customer",
	}}, integrationActorOne, "integration-create")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Status != StatusDraft || created.Revision != 1 || created.ObjectRevision != 1 {
		t.Fatalf("unexpected create result: %+v", created)
	}

	submitted, err := service.Submit(t.Context(), EntityCustomer, VersionRevisionInput{
		ObjectID: created.ObjectID, VersionID: created.VersionID, Revision: created.Revision,
	}, integrationActorOne, "integration-submit-1")
	if err != nil || submitted.Status != StatusPending || submitted.Revision != 2 {
		t.Fatalf("submit: result=%+v err=%v", submitted, err)
	}
	comment := "needs correction"
	rejected, err := service.Reject(t.Context(), EntityCustomer, ReviewInput{
		ObjectID: created.ObjectID, VersionID: created.VersionID, Revision: submitted.Revision, Comment: &comment,
	}, integrationActorTwo, "integration-reject")
	if err != nil || rejected.Status != StatusRejected || rejected.Revision != 3 {
		t.Fatalf("reject: result=%+v err=%v", rejected, err)
	}
	saved, err := service.Save(t.Context(), EntityCustomer, SaveInput{
		ObjectID: created.ObjectID, VersionID: created.VersionID, Revision: rejected.Revision,
		Data: DetailInput{Name: "Integration Customer Corrected"},
	}, integrationActorOne, "integration-save")
	if err != nil || saved.Revision != 4 {
		t.Fatalf("save: result=%+v err=%v", saved, err)
	}
	if _, err = service.Save(t.Context(), EntityCustomer, SaveInput{
		ObjectID: created.ObjectID, VersionID: created.VersionID, Revision: rejected.Revision,
		Data: DetailInput{Name: "Stale Save"},
	}, integrationActorOne, "integration-stale-save"); !errorIsKind(err, ErrorConflict) {
		t.Fatalf("stale save error = %v", err)
	}
	submitted, err = service.Submit(t.Context(), EntityCustomer, VersionRevisionInput{
		ObjectID: created.ObjectID, VersionID: created.VersionID, Revision: saved.Revision,
	}, integrationActorOne, "integration-submit-2")
	if err != nil || submitted.Revision != 5 {
		t.Fatalf("resubmit: result=%+v err=%v", submitted, err)
	}
	if _, err = service.Submit(t.Context(), EntityCustomer, VersionRevisionInput{
		ObjectID: created.ObjectID, VersionID: created.VersionID, Revision: saved.Revision,
	}, integrationActorOne, "integration-stale-submit"); !errorIsKind(err, ErrorConflict) {
		t.Fatalf("stale submit error = %v", err)
	}
	approved, err := service.Approve(t.Context(), EntityCustomer, ReviewInput{
		ObjectID: created.ObjectID, VersionID: created.VersionID, Revision: submitted.Revision,
	}, integrationActorTwo, "integration-approve")
	if err != nil || approved.Status != StatusEffective || approved.Revision != 6 || approved.ObjectRevision != 2 {
		t.Fatalf("approve: result=%+v err=%v", approved, err)
	}
	if _, err = service.Approve(t.Context(), EntityCustomer, ReviewInput{
		ObjectID: created.ObjectID, VersionID: created.VersionID, Revision: submitted.Revision,
	}, integrationActorTwo, "integration-stale-approve"); !errorIsKind(err, ErrorConflict) {
		t.Fatalf("stale approve error = %v", err)
	}

	view, err := service.Get(t.Context(), EntityCustomer, GetInput{ObjectID: created.ObjectID})
	if err != nil || view.Data.Name != "Integration Customer Corrected" || view.Version.Status != StatusEffective {
		t.Fatalf("get effective: view=%+v err=%v", view, err)
	}
	page, err := service.Query(t.Context(), EntityCustomer, QueryInput{
		Page: 1, PageSize: 20, Filters: QueryFilters{Keyword: code}, Sort: []SortItem{},
	})
	if err != nil || page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("query: page=%+v err=%v", page, err)
	}
	history, err := service.AuditHistory(t.Context(), EntityCustomer, HistoryInput{ObjectID: created.ObjectID, Page: 1, PageSize: 20})
	if err != nil || history.Total != 6 {
		t.Fatalf("audit history: total=%d err=%v", history.Total, err)
	}
	versions, err := service.Versions(t.Context(), EntityCustomer, HistoryInput{ObjectID: created.ObjectID, Page: 1, PageSize: 20})
	if err != nil || versions.Total != 1 || len(versions.Items) != 1 || versions.Items[0].ReviewedBy == nil {
		t.Fatalf("versions: page=%+v err=%v", versions, err)
	}

	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin reference transaction: %v", err)
	}
	reference, err := service.ResolveEffectiveReference(t.Context(), tx, EntityCustomer, created.ObjectID, created.VersionID)
	if err != nil || reference.Code != code {
		t.Fatalf("resolve reference: reference=%+v err=%v", reference, err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatalf("commit reference transaction: %v", err)
	}

	edited, err := service.Edit(t.Context(), EntityCustomer, ObjectRevisionInput{
		ObjectID: created.ObjectID, ObjectRevision: approved.ObjectRevision,
	}, integrationActorOne, "integration-edit")
	if err != nil || edited.Status != StatusDraft || edited.Version != 2 || edited.ObjectRevision != 3 {
		t.Fatalf("edit: result=%+v err=%v", edited, err)
	}
	oldView, err := service.Get(t.Context(), EntityCustomer, GetInput{ObjectID: created.ObjectID, VersionID: created.VersionID})
	if err != nil || oldView.Version.Status != StatusInvalid {
		t.Fatalf("old version after edit: view=%+v err=%v", oldView, err)
	}
	tx, err = pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin invalid reference transaction: %v", err)
	}
	_, err = service.ResolveEffectiveReference(t.Context(), tx, EntityCustomer, created.ObjectID, created.VersionID)
	_ = tx.Rollback(t.Context())
	if !errorIsKind(err, ErrorConflict) {
		t.Fatalf("invalidated reference error = %v", err)
	}
	if _, err = service.Edit(t.Context(), EntityCustomer, ObjectRevisionInput{
		ObjectID: created.ObjectID, ObjectRevision: approved.ObjectRevision,
	}, integrationActorOne, "integration-edit-repeat"); !errorIsKind(err, ErrorConflict) {
		t.Fatalf("repeat edit error = %v", err)
	}
}

func TestEveryEntityUsesTheLifecycleContractIntegration(t *testing.T) {
	pool := integrationPool(t)
	service := NewService(pool)
	platform, _ := createApprovedIntegration(t, service, EntitySupplier, CreateDetailInput{
		Code: "PL" + newID(), Name: "Lifecycle Platform",
		SupplierType: stringIntegrationPointer(SupplierTypeLogisticsPlatform),
	}, "contract-platform")
	tests := []struct {
		entity string
		data   CreateDetailInput
	}{
		{EntityCustomer, CreateDetailInput{Name: "Customer"}},
		{EntitySupplier, CreateDetailInput{Name: "Supplier"}},
		{EntityEmployee, CreateDetailInput{Name: "Employee"}},
		{EntityProduct, CreateDetailInput{Name: "Product", Unit: "piece"}},
		{EntityService, CreateDetailInput{Name: "Service", Unit: "hour"}},
		{EntityWarehouse, CreateDetailInput{Name: "主仓"}},
		{EntityVehicle, CreateDetailInput{
			Name: "Vehicle", PlateNumber: "沪A" + newID(), VehicleType: "Truck",
			PlatformObjectID: platform.ObjectID,
		}},
		{EntityFundAccount, CreateDetailInput{Name: "Cash", Currency: "CNY"}},
	}
	for _, test := range tests {
		t.Run(test.entity, func(t *testing.T) {
			test.data.Code = "LC" + newID()
			created, err := service.Create(t.Context(), test.entity, CreateInput{Data: test.data}, integrationActorOne, "contract-create")
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			submitted, err := service.Submit(t.Context(), test.entity, VersionRevisionInput{
				ObjectID: created.ObjectID, VersionID: created.VersionID, Revision: created.Revision,
			}, integrationActorOne, "contract-submit")
			if err != nil {
				t.Fatalf("submit: %v", err)
			}
			if _, err = service.Approve(t.Context(), test.entity, ReviewInput{
				ObjectID: created.ObjectID, VersionID: created.VersionID, Revision: submitted.Revision,
			}, integrationActorOne, "contract-self-approve"); !errorIsKind(err, ErrorConflict) {
				t.Fatalf("self approval error = %v", err)
			}
			approved, err := service.Approve(t.Context(), test.entity, ReviewInput{
				ObjectID: created.ObjectID, VersionID: created.VersionID, Revision: submitted.Revision,
			}, integrationActorTwo, "contract-approve")
			if err != nil {
				t.Fatalf("approve: %v", err)
			}
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatalf("begin resolve: %v", err)
			}
			reference, err := service.ResolveEffectiveReference(t.Context(), tx, test.entity, created.ObjectID, created.VersionID)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if reference.Data.Name != test.data.Name {
				t.Fatalf("reference name = %q, want %q", reference.Data.Name, test.data.Name)
			}
			if err = tx.Commit(t.Context()); err != nil {
				t.Fatalf("commit resolve: %v", err)
			}
			edited, err := service.Edit(t.Context(), test.entity, ObjectRevisionInput{
				ObjectID: created.ObjectID, ObjectRevision: approved.ObjectRevision,
			}, integrationActorOne, "contract-edit")
			if err != nil || edited.Version != 2 || edited.Status != StatusDraft {
				t.Fatalf("edit: result=%+v err=%v", edited, err)
			}
			oldVersion, err := service.Get(t.Context(), test.entity, GetInput{ObjectID: created.ObjectID, VersionID: created.VersionID})
			if err != nil || oldVersion.Version.Status != StatusInvalid {
				t.Fatalf("invalidated version: view=%+v err=%v", oldVersion, err)
			}
		})
	}
}

func TestLogisticsPlatformAndVehicleLifecycleIntegration(t *testing.T) {
	pool := integrationPool(t)
	service := NewService(pool)
	generalSupplier, _ := createApprovedIntegration(t, service, EntitySupplier, CreateDetailInput{
		Code: "GS" + newID(), Name: "普通供应商",
	}, "general-supplier")
	if _, err := service.Create(t.Context(), EntityVehicle, CreateInput{Data: CreateDetailInput{
		Code: "GV" + newID(), Name: "错误归属车辆", PlateNumber: "粤A" + newID(),
		VehicleType: "厢式货车", PlatformObjectID: generalSupplier.ObjectID,
	}}, integrationActorOne, "general-supplier-vehicle"); !errorIsKind(err, ErrorConflict) {
		t.Fatalf("general supplier vehicle error = %v", err)
	}

	platformCreated, platformApproved := createApprovedIntegration(t, service, EntitySupplier, CreateDetailInput{
		Code: "LP" + newID(), Name: "自营物流平台",
		SupplierType: stringIntegrationPointer(SupplierTypeLogisticsPlatform),
	}, "logistics-platform")
	vehiclePlate := "粤B" + newID()
	vehicleCreated, _ := createApprovedIntegration(t, service, EntityVehicle, CreateDetailInput{
		Code: "VH" + newID(), Name: "配送车", PlateNumber: vehiclePlate,
		VehicleType: "厢式货车", PlatformObjectID: platformCreated.ObjectID,
	}, "logistics-vehicle")
	vehiclePage, err := service.Query(t.Context(), EntityVehicle, QueryInput{
		Page: 1, PageSize: 20, Filters: QueryFilters{Keyword: strings.ToLower(vehiclePlate)},
	})
	if err != nil || vehiclePage.Total != 1 || len(vehiclePage.Items) != 1 {
		t.Fatalf("query vehicle by plate: page=%+v err=%v", vehiclePage, err)
	}

	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin vehicle reference: %v", err)
	}
	reference, err := service.ResolveEffectiveReference(
		t.Context(), tx, EntityVehicle, vehicleCreated.ObjectID, vehicleCreated.VersionID,
	)
	if err != nil {
		t.Fatalf("resolve vehicle: %v", err)
	}
	if reference.Data.PlatformObjectID != platformCreated.ObjectID || reference.Data.VehicleType != "厢式货车" {
		t.Fatalf("vehicle reference = %+v", reference)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatalf("commit vehicle reference: %v", err)
	}
	draftVehicleData := DetailInput{
		Name: "待保存车辆", PlateNumber: "粤C" + newID(),
		VehicleType: "厢式货车", PlatformObjectID: platformCreated.ObjectID,
	}
	draftVehicle, err := service.Create(t.Context(), EntityVehicle, CreateInput{Data: CreateDetailInput{
		Code: "VD" + newID(), Name: draftVehicleData.Name, PlateNumber: draftVehicleData.PlateNumber,
		VehicleType: draftVehicleData.VehicleType, PlatformObjectID: draftVehicleData.PlatformObjectID,
	}}, integrationActorOne, "vehicle-draft-create")
	if err != nil {
		t.Fatalf("create draft vehicle: %v", err)
	}

	platformEdited, err := service.Edit(t.Context(), EntitySupplier, ObjectRevisionInput{
		ObjectID: platformCreated.ObjectID, ObjectRevision: platformApproved.ObjectRevision,
	}, integrationActorOne, "platform-edit")
	if err != nil {
		t.Fatalf("edit platform: %v", err)
	}
	tx, err = pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin unavailable vehicle reference: %v", err)
	}
	_, err = service.ResolveEffectiveReference(
		t.Context(), tx, EntityVehicle, vehicleCreated.ObjectID, vehicleCreated.VersionID,
	)
	_ = tx.Rollback(t.Context())
	if !errorIsKind(err, ErrorConflict) {
		t.Fatalf("platform edit vehicle reference error = %v", err)
	}
	if _, err = service.Save(t.Context(), EntityVehicle, SaveInput{
		ObjectID: draftVehicle.ObjectID, VersionID: draftVehicle.VersionID, Revision: draftVehicle.Revision,
		Data: draftVehicleData,
	}, integrationActorOne, "vehicle-save-platform-unavailable"); !errorIsKind(err, ErrorConflict) {
		t.Fatalf("vehicle save while platform unavailable error = %v", err)
	}

	platformSaved, err := service.Save(t.Context(), EntitySupplier, SaveInput{
		ObjectID: platformEdited.ObjectID, VersionID: platformEdited.VersionID, Revision: platformEdited.Revision,
		Data: DetailInput{Name: "自营物流平台（更新）"},
	}, integrationActorOne, "platform-save-compatible")
	if err != nil {
		t.Fatalf("save platform without supplierType: %v", err)
	}
	platformSubmitted, err := service.Submit(t.Context(), EntitySupplier, VersionRevisionInput{
		ObjectID: platformSaved.ObjectID, VersionID: platformSaved.VersionID, Revision: platformSaved.Revision,
	}, integrationActorOne, "platform-submit")
	if err != nil {
		t.Fatalf("submit platform: %v", err)
	}
	platformReapproved, err := service.Approve(t.Context(), EntitySupplier, ReviewInput{
		ObjectID: platformSubmitted.ObjectID, VersionID: platformSubmitted.VersionID, Revision: platformSubmitted.Revision,
	}, integrationActorTwo, "platform-approve")
	if err != nil {
		t.Fatalf("approve platform: %v", err)
	}
	platformView, err := service.Get(t.Context(), EntitySupplier, GetInput{ObjectID: platformCreated.ObjectID})
	if err != nil || platformView.Data.SupplierType != SupplierTypeLogisticsPlatform {
		t.Fatalf("platform type after compatible save: view=%+v err=%v", platformView, err)
	}

	tx, err = pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin restored vehicle reference: %v", err)
	}
	if _, err = service.ResolveEffectiveReference(
		t.Context(), tx, EntityVehicle, vehicleCreated.ObjectID, vehicleCreated.VersionID,
	); err != nil {
		t.Fatalf("resolve vehicle after platform approval: %v", err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatalf("commit restored vehicle reference: %v", err)
	}
	if _, err = service.Save(t.Context(), EntityVehicle, SaveInput{
		ObjectID: draftVehicle.ObjectID, VersionID: draftVehicle.VersionID, Revision: draftVehicle.Revision,
		Data: draftVehicleData,
	}, integrationActorOne, "vehicle-save-platform-restored"); err != nil {
		t.Fatalf("save vehicle after platform approval: %v", err)
	}

	downgradeEdit, err := service.Edit(t.Context(), EntitySupplier, ObjectRevisionInput{
		ObjectID: platformCreated.ObjectID, ObjectRevision: platformReapproved.ObjectRevision,
	}, integrationActorOne, "platform-downgrade-edit")
	if err != nil {
		t.Fatalf("edit platform for downgrade: %v", err)
	}
	if _, err = service.Save(t.Context(), EntitySupplier, SaveInput{
		ObjectID: downgradeEdit.ObjectID, VersionID: downgradeEdit.VersionID, Revision: downgradeEdit.Revision,
		Data: DetailInput{
			Name: "普通供应商", SupplierType: stringIntegrationPointer(SupplierTypeGeneral),
		},
	}, integrationActorOne, "platform-downgrade-save"); !errorIsKind(err, ErrorConflict) {
		t.Fatalf("platform downgrade error = %v", err)
	}
}

func TestVehiclePlateUniquenessAndHistoryIntegration(t *testing.T) {
	pool := integrationPool(t)
	service := NewService(pool)
	platform, _ := createApprovedIntegration(t, service, EntitySupplier, CreateDetailInput{
		Code: "PU" + newID(), Name: "Plate Platform",
		SupplierType: stringIntegrationPointer(SupplierTypeLogisticsPlatform),
	}, "plate-platform")

	plate := "沪C" + newID()
	start := make(chan struct{})
	results := make(chan error, 2)
	for index := range 2 {
		go func(index int) {
			<-start
			_, createErr := service.Create(context.Background(), EntityVehicle, CreateInput{Data: CreateDetailInput{
				Code: "PC" + fmt.Sprint(index) + newID(), Name: "Concurrent Vehicle",
				PlateNumber: strings.ToLower(plate), VehicleType: "Truck", PlatformObjectID: platform.ObjectID,
			}}, integrationActorOne, fmt.Sprintf("plate-concurrent-%d", index))
			results <- createErr
		}(index)
	}
	close(start)
	successes, conflicts := 0, 0
	for range 2 {
		switch err := <-results; {
		case err == nil:
			successes++
		case errorIsKind(err, ErrorConflict):
			conflicts++
		default:
			t.Fatalf("concurrent plate error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("plate successes=%d conflicts=%d", successes, conflicts)
	}

	original, approved := createApprovedIntegration(t, service, EntityVehicle, CreateDetailInput{
		Code: "PR" + newID(), Name: "Reusable Plate Vehicle", PlateNumber: "沪D" + newID(),
		VehicleType: "Truck", PlatformObjectID: platform.ObjectID,
	}, "plate-release")
	originalView, err := service.Get(t.Context(), EntityVehicle, GetInput{ObjectID: original.ObjectID})
	if err != nil {
		t.Fatalf("get original vehicle: %v", err)
	}
	edited, err := service.Edit(t.Context(), EntityVehicle, ObjectRevisionInput{
		ObjectID: original.ObjectID, ObjectRevision: approved.ObjectRevision,
	}, integrationActorOne, "plate-release-edit")
	if err != nil {
		t.Fatalf("edit original vehicle: %v", err)
	}
	if _, err = service.Save(t.Context(), EntityVehicle, SaveInput{
		ObjectID: edited.ObjectID, VersionID: edited.VersionID, Revision: edited.Revision,
		Data: DetailInput{
			Name: "Reusable Plate Vehicle", PlateNumber: "沪E" + newID(),
			VehicleType: "Truck", PlatformObjectID: platform.ObjectID,
		},
	}, integrationActorOne, "plate-release-save"); err != nil {
		t.Fatalf("save replacement plate: %v", err)
	}
	if _, err = service.Create(t.Context(), EntityVehicle, CreateInput{Data: CreateDetailInput{
		Code: "PN" + newID(), Name: "Reused Plate Vehicle", PlateNumber: originalView.Data.PlateNumber,
		VehicleType: "Truck", PlatformObjectID: platform.ObjectID,
	}}, integrationActorOne, "plate-reuse-create"); err != nil {
		t.Fatalf("reuse historical plate: %v", err)
	}
}

func TestWarehouseSchemaAndPermissionsIntegration(t *testing.T) {
	pool := integrationPool(t)

	var warehouseTable *string
	if err := pool.QueryRow(t.Context(), "select to_regclass('bob_warehouse_versions')::text").Scan(&warehouseTable); err != nil {
		t.Fatalf("read warehouse table: %v", err)
	}
	if warehouseTable == nil || *warehouseTable != "bob_warehouse_versions" {
		t.Fatalf("warehouse table = %v", warehouseTable)
	}

	expectedSequence := map[string]int{
		"approve":       61,
		"audit-history": 62,
		"create":        63,
		"edit":          64,
		"get":           65,
		"query":         66,
		"reject":        67,
		"save":          68,
		"submit":        69,
		"versions":      70,
	}
	rows, err := pool.Query(t.Context(), `
		SELECT id, path, action, status
		FROM app_permissions
		WHERE domain = 'bob' AND entity = 'warehouse'
	`)
	if err != nil {
		t.Fatalf("query warehouse permissions: %v", err)
	}
	defer rows.Close()
	seen := make(map[string]bool, len(expectedSequence))
	for rows.Next() {
		var id, path, action, status string
		if err = rows.Scan(&id, &path, &action, &status); err != nil {
			t.Fatalf("scan warehouse permission: %v", err)
		}
		sequence, exists := expectedSequence[action]
		if !exists {
			t.Fatalf("unexpected warehouse action %q", action)
		}
		if id != fmt.Sprintf("01JBOB%020d", sequence) {
			t.Fatalf("permission %s id = %q", action, id)
		}
		if path != "/bob/warehouse/"+action || status != "ENABLED" {
			t.Fatalf("permission %s path=%q status=%q", action, path, status)
		}
		seen[action] = true
	}
	if err = rows.Err(); err != nil {
		t.Fatalf("iterate warehouse permissions: %v", err)
	}
	if len(seen) != len(expectedSequence) {
		t.Fatalf("warehouse permission actions = %v", seen)
	}

	var superadminCount int
	if err = pool.QueryRow(t.Context(), "SELECT count(*) FROM app_roles WHERE code = 'superadmin'").Scan(&superadminCount); err != nil {
		t.Fatalf("count superadmin roles: %v", err)
	}
	if superadminCount > 0 {
		var grantCount int
		if err = pool.QueryRow(t.Context(), `
			SELECT count(*)
			FROM app_role_permissions rp
			JOIN app_roles r ON r.id = rp.role_id
			JOIN app_permissions p ON p.id = rp.permission_id
			WHERE r.code = 'superadmin' AND p.domain = 'bob' AND p.entity = 'warehouse'
		`).Scan(&grantCount); err != nil {
			t.Fatalf("count superadmin warehouse grants: %v", err)
		}
		if grantCount != superadminCount*len(expectedSequence) {
			t.Fatalf("superadmin warehouse grants = %d, want %d", grantCount, superadminCount*len(expectedSequence))
		}
	}
}

func TestVehicleSchemaAndPermissionsIntegration(t *testing.T) {
	pool := integrationPool(t)

	var vehicleTable *string
	if err := pool.QueryRow(t.Context(), "select to_regclass('bob_vehicle_versions')::text").Scan(&vehicleTable); err != nil {
		t.Fatalf("read vehicle table: %v", err)
	}
	if vehicleTable == nil || *vehicleTable != "bob_vehicle_versions" {
		t.Fatalf("vehicle table = %v", vehicleTable)
	}

	expectedSequence := map[string]int{
		"approve":       71,
		"audit-history": 72,
		"create":        73,
		"edit":          74,
		"get":           75,
		"query":         76,
		"reject":        77,
		"save":          78,
		"submit":        79,
		"versions":      80,
	}
	rows, err := pool.Query(t.Context(), `
		SELECT id, path, action, status
		FROM app_permissions
		WHERE domain = 'bob' AND entity = 'vehicle'
	`)
	if err != nil {
		t.Fatalf("query vehicle permissions: %v", err)
	}
	defer rows.Close()
	seen := make(map[string]bool, len(expectedSequence))
	for rows.Next() {
		var id, path, action, status string
		if err = rows.Scan(&id, &path, &action, &status); err != nil {
			t.Fatalf("scan vehicle permission: %v", err)
		}
		sequence, exists := expectedSequence[action]
		if !exists {
			t.Fatalf("unexpected vehicle action %q", action)
		}
		if id != fmt.Sprintf("01JBOB%020d", sequence) {
			t.Fatalf("permission %s id = %q", action, id)
		}
		if path != "/bob/vehicle/"+action || status != "ENABLED" {
			t.Fatalf("permission %s path=%q status=%q", action, path, status)
		}
		seen[action] = true
	}
	if err = rows.Err(); err != nil {
		t.Fatalf("iterate vehicle permissions: %v", err)
	}
	if len(seen) != len(expectedSequence) {
		t.Fatalf("vehicle permission actions = %v", seen)
	}

	var superadminCount int
	if err = pool.QueryRow(t.Context(), "SELECT count(*) FROM app_roles WHERE code = 'superadmin'").Scan(&superadminCount); err != nil {
		t.Fatalf("count superadmin roles: %v", err)
	}
	if superadminCount > 0 {
		var grantCount int
		if err = pool.QueryRow(t.Context(), `
			SELECT count(*)
			FROM app_role_permissions rp
			JOIN app_roles r ON r.id = rp.role_id
			JOIN app_permissions p ON p.id = rp.permission_id
			WHERE r.code = 'superadmin' AND p.domain = 'bob' AND p.entity = 'vehicle'
		`).Scan(&grantCount); err != nil {
			t.Fatalf("count superadmin vehicle grants: %v", err)
		}
		if grantCount != superadminCount*len(expectedSequence) {
			t.Fatalf("superadmin vehicle grants = %d, want %d", grantCount, superadminCount*len(expectedSequence))
		}
	}
}

func TestConcurrentEditAllowsOneWinnerIntegration(t *testing.T) {
	pool := integrationPool(t)
	service := NewService(pool)
	created, err := service.Create(t.Context(), EntityCustomer, CreateInput{Data: CreateDetailInput{
		Code: "CC" + newID(), Name: "Concurrent Customer",
	}}, integrationActorOne, "concurrent-create")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	submitted, err := service.Submit(t.Context(), EntityCustomer, VersionRevisionInput{
		ObjectID: created.ObjectID, VersionID: created.VersionID, Revision: created.Revision,
	}, integrationActorOne, "concurrent-submit")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	approved, err := service.Approve(t.Context(), EntityCustomer, ReviewInput{
		ObjectID: created.ObjectID, VersionID: created.VersionID, Revision: submitted.Revision,
	}, integrationActorTwo, "concurrent-approve")
	if err != nil {
		t.Fatalf("approve: %v", err)
	}

	start := make(chan struct{})
	errorsChannel := make(chan error, 2)
	for index := 0; index < 2; index++ {
		go func() {
			<-start
			_, editErr := service.Edit(context.Background(), EntityCustomer, ObjectRevisionInput{
				ObjectID: created.ObjectID, ObjectRevision: approved.ObjectRevision,
			}, integrationActorOne, "concurrent-edit")
			errorsChannel <- editErr
		}()
	}
	close(start)
	successes, conflicts := 0, 0
	for range 2 {
		editErr := <-errorsChannel
		switch {
		case editErr == nil:
			successes++
		case errorIsKind(editErr, ErrorConflict):
			conflicts++
		default:
			t.Fatalf("unexpected edit error: %v", editErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
}

func TestEffectiveReferenceLockBlocksEditIntegration(t *testing.T) {
	pool := integrationPool(t)
	service := NewService(pool)
	created, err := service.Create(t.Context(), EntityCustomer, CreateInput{Data: CreateDetailInput{
		Code: "RL" + newID(), Name: "Reference Lock Customer",
	}}, integrationActorOne, "lock-create")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	submitted, err := service.Submit(t.Context(), EntityCustomer, VersionRevisionInput{
		ObjectID: created.ObjectID, VersionID: created.VersionID, Revision: created.Revision,
	}, integrationActorOne, "lock-submit")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	approved, err := service.Approve(t.Context(), EntityCustomer, ReviewInput{
		ObjectID: created.ObjectID, VersionID: created.VersionID, Revision: submitted.Revision,
	}, integrationActorTwo, "lock-approve")
	if err != nil {
		t.Fatalf("approve: %v", err)
	}

	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin reference transaction: %v", err)
	}
	if _, err = service.ResolveEffectiveReference(t.Context(), tx, EntityCustomer, created.ObjectID, created.VersionID); err != nil {
		t.Fatalf("resolve reference: %v", err)
	}
	editResult := make(chan error, 1)
	editContext, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		_, editErr := service.Edit(editContext, EntityCustomer, ObjectRevisionInput{
			ObjectID: created.ObjectID, ObjectRevision: approved.ObjectRevision,
		}, integrationActorOne, "lock-edit")
		editResult <- editErr
	}()
	select {
	case editErr := <-editResult:
		_ = tx.Rollback(t.Context())
		t.Fatalf("edit completed while reference lock was held: %v", editErr)
	case <-time.After(150 * time.Millisecond):
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatalf("commit reference transaction: %v", err)
	}
	select {
	case editErr := <-editResult:
		if editErr != nil {
			t.Fatalf("edit after reference commit: %v", editErr)
		}
	case <-editContext.Done():
		t.Fatalf("edit remained blocked after reference commit: %v", editContext.Err())
	}
}

func TestDatabaseRejectsVersionWithoutTypedDetail(t *testing.T) {
	pool := integrationPool(t)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	queries := dbsqlc.New(pool).WithTx(tx)
	objectID, versionID := newID(), newID()
	if err = queries.InsertBobObject(t.Context(), dbsqlc.InsertBobObjectParams{
		ID: objectID, Entity: EntityCustomer, Code: "MISSING" + newID(), CurrentVersionID: versionID, ActorID: integrationActorOne,
	}); err != nil {
		t.Fatalf("insert object: %v", err)
	}
	if err = queries.InsertBobVersion(t.Context(), dbsqlc.InsertBobVersionParams{
		ID: versionID, ObjectID: objectID, Entity: EntityCustomer, VersionNo: 1, ActorID: integrationActorOne,
	}); err != nil {
		t.Fatalf("insert version: %v", err)
	}
	err = tx.Commit(t.Context())
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("commit error = %v, want check violation", err)
	}
}

func TestDuplicateCodeReturnsConflictAndRollsBackIntegration(t *testing.T) {
	pool := integrationPool(t)
	service := NewService(pool)
	code := "DU" + newID()
	if _, err := service.Create(t.Context(), EntityCustomer, CreateInput{Data: CreateDetailInput{
		Code: code, Name: "Original",
	}}, integrationActorOne, "duplicate-create-original"); err != nil {
		t.Fatalf("create original: %v", err)
	}
	if _, err := service.Create(t.Context(), EntityCustomer, CreateInput{Data: CreateDetailInput{
		Code: code, Name: "Duplicate",
	}}, integrationActorOne, "duplicate-create-conflict"); !errorIsKind(err, ErrorConflict) {
		t.Fatalf("duplicate create error = %v", err)
	}

	var objects, versions int
	if err := pool.QueryRow(t.Context(), `
		SELECT count(DISTINCT o.id), count(v.id)
		FROM bob_objects o
		JOIN bob_versions v ON v.object_id = o.id
		WHERE o.entity = $1 AND o.code = $2
	`, EntityCustomer, code).Scan(&objects, &versions); err != nil {
		t.Fatalf("count duplicate code rows: %v", err)
	}
	if objects != 1 || versions != 1 {
		t.Fatalf("objects=%d versions=%d, want one committed aggregate", objects, versions)
	}
}

func createApprovedIntegration(
	t *testing.T,
	service *Service,
	entity string,
	data CreateDetailInput,
	requestPrefix string,
) (MutationResult, MutationResult) {
	t.Helper()
	created, err := service.Create(
		t.Context(), entity, CreateInput{Data: data}, integrationActorOne, requestPrefix+"-create",
	)
	if err != nil {
		t.Fatalf("create approved %s: %v", entity, err)
	}
	submitted, err := service.Submit(t.Context(), entity, VersionRevisionInput{
		ObjectID: created.ObjectID, VersionID: created.VersionID, Revision: created.Revision,
	}, integrationActorOne, requestPrefix+"-submit")
	if err != nil {
		t.Fatalf("submit approved %s: %v", entity, err)
	}
	approved, err := service.Approve(t.Context(), entity, ReviewInput{
		ObjectID: submitted.ObjectID, VersionID: submitted.VersionID, Revision: submitted.Revision,
	}, integrationActorTwo, requestPrefix+"-approve")
	if err != nil {
		t.Fatalf("approve %s: %v", entity, err)
	}
	return created, approved
}

func stringIntegrationPointer(value string) *string {
	return &value
}
