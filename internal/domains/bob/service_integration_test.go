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
	_, salesperson := createApprovedIntegration(t, service, EntityEmployee, CreateDetailInput{
		Code: "LSE" + newID(), Name: "Lifecycle Salesperson",
	}, "lifecycle-salesperson")
	code := "IT" + newID()
	created, err := service.Create(t.Context(), EntityCustomer, CreateInput{Data: CreateDetailInput{
		Code: code, Name: "Integration Customer", SalespersonEmployeeID: salesperson.ObjectID,
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
	_, salesperson := createApprovedIntegration(t, service, EntityEmployee, CreateDetailInput{
		Code: "LCE" + newID(), Name: "Contract Salesperson",
	}, "contract-salesperson")
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
		{EntityCategory, CreateDetailInput{Name: "Product Category", TargetEntity: EntityProduct}},
		{EntityDepartment, CreateDetailInput{Name: "Operations"}},
		{EntityPosition, CreateDetailInput{Name: "Operator"}},
		{EntitySettlementMethod, CreateDetailInput{
			Name: "Month End", RuleType: SettlementRuleMonthEnd, MonthOffset: 1,
		}},
	}
	for _, test := range tests {
		t.Run(test.entity, func(t *testing.T) {
			test.data.Code = "LC" + newID()
			if test.entity == EntityCustomer || test.entity == EntitySupplier {
				test.data.SalespersonEmployeeID = salesperson.ObjectID
			}
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

func TestCommonAttributesReferencesFiltersAndRedactionIntegration(t *testing.T) {
	pool := integrationPool(t)
	service := NewService(pool)

	category, categoryApproved := createApprovedIntegration(t, service, EntityCategory, CreateDetailInput{
		Code: "CC" + newID(), Name: "客户分类", TargetEntity: EntityCustomer,
		Description: "用于客户筛选",
	}, "common-customer-category")
	productCategory, _ := createApprovedIntegration(t, service, EntityCategory, CreateDetailInput{
		Code: "PC" + newID(), Name: "产品分类", TargetEntity: EntityProduct,
	}, "common-product-category")
	department, _ := createApprovedIntegration(t, service, EntityDepartment, CreateDetailInput{
		Code: "DP" + newID(), Name: "运营部",
	}, "common-department")
	position, _ := createApprovedIntegration(t, service, EntityPosition, CreateDetailInput{
		Code: "PS" + newID(), Name: "运营专员",
	}, "common-position")
	salesperson, _ := createApprovedIntegration(t, service, EntityEmployee, CreateDetailInput{
		Code: "SP" + newID(), Name: "客户业务员", DepartmentID: department.ObjectID,
		PositionID: position.ObjectID,
	}, "common-salesperson")
	settlementCode := "SM" + newID()
	settlementMethod, _ := createApprovedIntegration(t, service, EntitySettlementMethod, CreateDetailInput{
		Code: settlementCode, Name: "月结 30 天", RuleType: SettlementRuleRelativeDays,
		DayOffset: 30,
	}, "common-settlement-method")
	settlementPage, err := service.Query(t.Context(), EntitySettlementMethod, QueryInput{
		Page: 1, PageSize: 20, Filters: QueryFilters{Keyword: settlementCode},
	})
	if err != nil || settlementPage.Total != 1 {
		t.Fatalf("query settlement methods page=%+v err=%v", settlementPage, err)
	}

	if _, err := service.Create(t.Context(), EntityCustomer, CreateInput{Data: CreateDetailInput{
		Code: "WC" + newID(), Name: "错误分类客户", CategoryID: productCategory.ObjectID,
		SalespersonEmployeeID: salesperson.ObjectID,
	}}, integrationActorOne, "wrong-category-target"); !errorIsKind(err, ErrorConflict) {
		t.Fatalf("wrong category target error = %v", err)
	}
	if _, err := service.Create(t.Context(), EntityCustomer, CreateInput{Data: CreateDetailInput{
		Code: "WS" + newID(), Name: "错误结算方式客户", SettlementMethodID: position.ObjectID,
		SalespersonEmployeeID: salesperson.ObjectID,
	}}, integrationActorOne, "wrong-settlement-target"); !errorIsKind(err, ErrorConflict) {
		t.Fatalf("wrong settlement target error = %v", err)
	}
	if _, err := service.Create(t.Context(), EntityCustomer, CreateInput{Data: CreateDetailInput{
		Code: "WE" + newID(), Name: "错误业务员客户", SalespersonEmployeeID: settlementMethod.ObjectID,
	}}, integrationActorOne, "wrong-salesperson-target"); !errorIsKind(err, ErrorConflict) {
		t.Fatalf("wrong salesperson target error = %v", err)
	}
	draftSalesperson, err := service.Create(t.Context(), EntityEmployee, CreateInput{Data: CreateDetailInput{
		Code: "SD" + newID(), Name: "草稿业务员",
	}}, integrationActorOne, "draft-salesperson")
	if err != nil {
		t.Fatalf("create draft salesperson: %v", err)
	}
	if _, err = service.Create(t.Context(), EntityCustomer, CreateInput{Data: CreateDetailInput{
		Code: "WD" + newID(), Name: "无效业务员客户", SalespersonEmployeeID: draftSalesperson.ObjectID,
	}}, integrationActorOne, "inactive-salesperson-target"); !errorIsKind(err, ErrorConflict) {
		t.Fatalf("inactive salesperson target error = %v", err)
	}
	taxNumber := "TAX" + newID()
	customer, err := service.Create(t.Context(), EntityCustomer, CreateInput{Data: CreateDetailInput{
		Code: "CA" + newID(), Name: "属性客户", CustomerType: stringIntegrationPointer(CustomerTypeDealer),
		ShortName: "属性客户简称", CategoryID: category.ObjectID, TaxNumber: taxNumber,
		ContactName: "联系人", ContactPhone: "+86 13800000000",
		Email: "CONTACT@EXAMPLE.COM", Address: "上海市示例路", Remark: "新增属性",
		SettlementMethodID:    settlementMethod.ObjectID,
		SalespersonEmployeeID: salesperson.ObjectID,
	}}, integrationActorOne, "common-customer-create")
	if err != nil {
		t.Fatalf("create customer attributes: %v", err)
	}
	saved, err := service.Save(t.Context(), EntityCustomer, SaveInput{
		ObjectID: customer.ObjectID, VersionID: customer.VersionID, Revision: customer.Revision,
		Data: DetailInput{Name: "属性客户（更新）"},
	}, integrationActorOne, "common-customer-preserve")
	if err != nil {
		t.Fatalf("save customer preserving omitted attributes: %v", err)
	}
	view, err := service.Get(t.Context(), EntityCustomer, GetInput{ObjectID: customer.ObjectID})
	if err != nil || view.Data.ShortName != "属性客户简称" || view.Data.TaxNumber != taxNumber ||
		view.Data.Email != "contact@example.com" || view.Data.CategoryID != category.ObjectID ||
		view.Data.SettlementMethodID != settlementMethod.ObjectID ||
		view.Data.SalespersonEmployeeID != salesperson.ObjectID {
		t.Fatalf("preserved customer view=%+v err=%v", view, err)
	}
	page, err := service.Query(t.Context(), EntityCustomer, QueryInput{
		Page: 1, PageSize: 20,
		Filters: QueryFilters{
			CustomerType: CustomerTypeDealer, CategoryID: category.ObjectID,
			SalespersonEmployeeID: salesperson.ObjectID, Keyword: taxNumber,
		},
	})
	if err != nil || page.Total != 1 || len(page.Items) != 1 ||
		page.Items[0].CurrentVersion.Summary.SettlementMethodID != settlementMethod.ObjectID ||
		page.Items[0].CurrentVersion.Summary.SalespersonEmployeeID != salesperson.ObjectID {
		t.Fatalf("query common attributes page=%+v err=%v", page, err)
	}
	supplier, err := service.Create(t.Context(), EntitySupplier, CreateInput{Data: CreateDetailInput{
		Code: "SA" + newID(), Name: "属性供应商",
		SettlementMethodID:    settlementMethod.ObjectID,
		SalespersonEmployeeID: salesperson.ObjectID,
	}}, integrationActorOne, "common-supplier-create")
	if err != nil {
		t.Fatalf("create supplier salesperson: %v", err)
	}
	supplierView, err := service.Get(t.Context(), EntitySupplier, GetInput{ObjectID: supplier.ObjectID})
	if err != nil || supplierView.Data.SalespersonEmployeeID != salesperson.ObjectID {
		t.Fatalf("supplier salesperson view=%+v err=%v", supplierView.Data, err)
	}
	supplierPage, err := service.Query(t.Context(), EntitySupplier, QueryInput{
		Page: 1, PageSize: 20,
		Filters: QueryFilters{SalespersonEmployeeID: salesperson.ObjectID},
	})
	if err != nil || supplierPage.Total < 1 {
		t.Fatalf("query supplier salesperson page=%+v err=%v", supplierPage, err)
	}
	saved, err = service.Save(t.Context(), EntityCustomer, SaveInput{
		ObjectID: customer.ObjectID, VersionID: customer.VersionID, Revision: saved.Revision,
		Data: DetailInput{
			Name: "属性客户（更新）", ShortName: Optional(""), ContactPhone: Optional(""),
		},
	}, integrationActorOne, "common-customer-clear")
	if err != nil {
		t.Fatalf("clear optional attributes: %v", err)
	}
	view, _ = service.Get(t.Context(), EntityCustomer, GetInput{ObjectID: customer.ObjectID})
	if view.Data.ShortName != "" || view.Data.ContactPhone != "" || view.Data.TaxNumber != taxNumber {
		t.Fatalf("explicit clear view = %+v", view.Data)
	}
	clearable, err := service.Create(t.Context(), EntityCustomer, CreateInput{Data: CreateDetailInput{
		Code: "CL" + newID(), Name: "清空默认值客户",
		SettlementMethodID:    settlementMethod.ObjectID,
		SalespersonEmployeeID: salesperson.ObjectID,
	}}, integrationActorOne, "common-customer-clear-create")
	if err != nil {
		t.Fatalf("create clearable customer: %v", err)
	}
	if _, err = service.Save(t.Context(), EntityCustomer, SaveInput{
		ObjectID: clearable.ObjectID, VersionID: clearable.VersionID, Revision: clearable.Revision,
		Data: DetailInput{
			Name: "清空默认值客户", SettlementMethodID: Optional(""),
			SalespersonEmployeeID: Optional(""),
		},
	}, integrationActorOne, "common-customer-clear-references"); !errorIsKind(err, ErrorValidation) {
		t.Fatalf("clear required salesperson error = %v", err)
	}
	clearedView, err := service.Get(t.Context(), EntityCustomer, GetInput{ObjectID: clearable.ObjectID})
	if err != nil || clearedView.Data.SettlementMethodID != settlementMethod.ObjectID ||
		clearedView.Data.SalespersonEmployeeID != salesperson.ObjectID {
		t.Fatalf("rejected clear changed customer view=%+v err=%v", clearedView, err)
	}
	if _, err = service.Create(t.Context(), EntityCustomer, CreateInput{Data: CreateDetailInput{
		Code: "DU" + newID(), Name: "重复税号客户", TaxNumber: strings.ToLower(taxNumber),
		SalespersonEmployeeID: salesperson.ObjectID,
	}}, integrationActorOne, "duplicate-tax"); !errorIsKind(err, ErrorConflict) {
		t.Fatalf("duplicate tax error = %v", err)
	}
	submitted, _ := service.Submit(t.Context(), EntityCustomer, VersionRevisionInput{
		ObjectID: customer.ObjectID, VersionID: customer.VersionID, Revision: saved.Revision,
	}, integrationActorOne, "common-customer-submit")
	approved, err := service.Approve(t.Context(), EntityCustomer, ReviewInput{
		ObjectID: customer.ObjectID, VersionID: customer.VersionID, Revision: submitted.Revision,
	}, integrationActorTwo, "common-customer-approve")
	if err != nil {
		t.Fatalf("approve common customer: %v", err)
	}

	employee, _ := createApprovedIntegration(t, service, EntityEmployee, CreateDetailInput{
		Code: "EM" + newID(), Name: "关联员工", DepartmentID: department.ObjectID,
		PositionID: position.ObjectID, Phone: "13800000001", HireDate: "2025-01-02",
	}, "common-employee")
	warehouse, _ := createApprovedIntegration(t, service, EntityWarehouse, CreateDetailInput{
		Code: "WH" + newID(), Name: "关联仓库", ManagerEmployeeID: employee.ObjectID,
		Address: "上海市仓库路",
	}, "common-warehouse")
	if warehouse.ObjectID == "" {
		t.Fatal("warehouse reference was not created")
	}

	accountNumber := "6222" + newID()
	accountCode := "FA" + newID()
	account, _ := createApprovedIntegration(t, service, EntityFundAccount, CreateDetailInput{
		Code: accountCode, Name: "敏感账户", Currency: "CNY",
		AccountName: "示例公司", BankName: "示例银行", BankBranch: "上海支行",
		AccountNumber: accountNumber,
	}, "common-account")
	accountView, err := service.Get(t.Context(), EntityFundAccount, GetInput{ObjectID: account.ObjectID})
	if err != nil || accountView.Data.AccountNumber == "" {
		t.Fatalf("get account view=%+v err=%v", accountView, err)
	}
	accountPage, err := service.Query(t.Context(), EntityFundAccount, QueryInput{
		Page: 1, PageSize: 20, Filters: QueryFilters{Currency: "cny", Keyword: accountCode},
	})
	if err != nil || accountPage.Total != 1 || accountPage.Items[0].CurrentVersion.Summary.AccountNumber != "" {
		t.Fatalf("query account redaction page=%+v err=%v", accountPage, err)
	}
	accountVersions, err := service.Versions(t.Context(), EntityFundAccount, HistoryInput{
		ObjectID: account.ObjectID, Page: 1, PageSize: 20,
	})
	if err != nil || accountVersions.Items[0].Summary.AccountNumber == "" {
		t.Fatalf("account versions=%+v err=%v", accountVersions, err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin account resolve: %v", err)
	}
	accountReference, err := service.ResolveEffectiveReference(
		t.Context(), tx, EntityFundAccount, account.ObjectID, account.VersionID,
	)
	_ = tx.Rollback(t.Context())
	if err != nil || accountReference.Data.AccountNumber != "" {
		t.Fatalf("account reference=%+v err=%v", accountReference, err)
	}
	if _, err = service.Create(t.Context(), EntityFundAccount, CreateInput{Data: CreateDetailInput{
		Code: "FD" + newID(), Name: "重复账号", Currency: "CNY", AccountNumber: accountNumber,
	}}, integrationActorOne, "duplicate-account"); !errorIsKind(err, ErrorConflict) {
		t.Fatalf("duplicate account error = %v", err)
	}

	categoryEdit, err := service.Edit(t.Context(), EntityCategory, ObjectRevisionInput{
		ObjectID: category.ObjectID, ObjectRevision: categoryApproved.ObjectRevision,
	}, integrationActorOne, "category-target-edit")
	if err != nil {
		t.Fatalf("edit referenced category: %v", err)
	}
	tx, err = pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin customer resolve during category edit: %v", err)
	}
	_, err = service.ResolveEffectiveReference(t.Context(), tx, EntityCustomer, customer.ObjectID, customer.VersionID)
	_ = tx.Rollback(t.Context())
	if err != nil {
		t.Fatalf("category edit recursively blocked customer: %v", err)
	}
	if _, err = service.Save(t.Context(), EntityCategory, SaveInput{
		ObjectID: categoryEdit.ObjectID, VersionID: categoryEdit.VersionID, Revision: categoryEdit.Revision,
		Data: DetailInput{
			Name: "客户分类", TargetEntity: stringIntegrationPointer(EntityProduct),
		},
	}, integrationActorOne, "category-target-change"); !errorIsKind(err, ErrorConflict) {
		t.Fatalf("referenced category target change error = %v", err)
	}

	categorySaved, err := service.Save(t.Context(), EntityCategory, SaveInput{
		ObjectID: categoryEdit.ObjectID, VersionID: categoryEdit.VersionID, Revision: categoryEdit.Revision,
		Data: DetailInput{Name: "客户分类"},
	}, integrationActorOne, "category-restore-save")
	if err != nil {
		t.Fatalf("restore category draft: %v", err)
	}
	categorySubmitted, _ := service.Submit(t.Context(), EntityCategory, VersionRevisionInput{
		ObjectID: categoryEdit.ObjectID, VersionID: categoryEdit.VersionID, Revision: categorySaved.Revision,
	}, integrationActorOne, "category-restore-submit")
	categoryRestored, err := service.Approve(t.Context(), EntityCategory, ReviewInput{
		ObjectID: categoryEdit.ObjectID, VersionID: categoryEdit.VersionID, Revision: categorySubmitted.Revision,
	}, integrationActorTwo, "category-restore-approve")
	if err != nil {
		t.Fatalf("restore category effective version: %v", err)
	}

	customerEdit, err := service.Edit(t.Context(), EntityCustomer, ObjectRevisionInput{
		ObjectID: customer.ObjectID, ObjectRevision: approved.ObjectRevision,
	}, integrationActorOne, "historical-category-customer-edit")
	if err != nil {
		t.Fatalf("edit customer before clearing category: %v", err)
	}
	customerSaved, err := service.Save(t.Context(), EntityCustomer, SaveInput{
		ObjectID: customerEdit.ObjectID, VersionID: customerEdit.VersionID, Revision: customerEdit.Revision,
		Data: DetailInput{Name: "属性客户（更新）", CategoryID: Optional("")},
	}, integrationActorOne, "historical-category-customer-save")
	if err != nil {
		t.Fatalf("clear current customer category: %v", err)
	}
	customerSubmitted, _ := service.Submit(t.Context(), EntityCustomer, VersionRevisionInput{
		ObjectID: customerEdit.ObjectID, VersionID: customerEdit.VersionID, Revision: customerSaved.Revision,
	}, integrationActorOne, "historical-category-customer-submit")
	if _, err = service.Approve(t.Context(), EntityCustomer, ReviewInput{
		ObjectID: customerEdit.ObjectID, VersionID: customerEdit.VersionID, Revision: customerSubmitted.Revision,
	}, integrationActorTwo, "historical-category-customer-approve"); err != nil {
		t.Fatalf("approve customer without current category: %v", err)
	}

	categoryHistoryEdit, err := service.Edit(t.Context(), EntityCategory, ObjectRevisionInput{
		ObjectID: category.ObjectID, ObjectRevision: categoryRestored.ObjectRevision,
	}, integrationActorOne, "historical-category-target-edit")
	if err != nil {
		t.Fatalf("edit category with historical reference: %v", err)
	}
	if _, err = service.Save(t.Context(), EntityCategory, SaveInput{
		ObjectID: categoryHistoryEdit.ObjectID, VersionID: categoryHistoryEdit.VersionID,
		Revision: categoryHistoryEdit.Revision,
		Data: DetailInput{
			Name: "客户分类", TargetEntity: stringIntegrationPointer(EntityProduct),
		},
	}, integrationActorOne, "historical-category-target-save"); !errorIsKind(err, ErrorConflict) {
		t.Fatalf("historical category target change error = %v", err)
	}
	if approved.ObjectID == "" {
		t.Fatal("customer approval result is empty")
	}
}

func TestCategoryAndDepartmentHierarchyCycleIntegration(t *testing.T) {
	pool := integrationPool(t)
	service := NewService(pool)

	categoryRoot, categoryRootApproved := createApprovedIntegration(t, service, EntityCategory, CreateDetailInput{
		Code: "CR" + newID(), Name: "分类根", TargetEntity: EntityProduct,
	}, "category-root")
	categoryChild, _ := createApprovedIntegration(t, service, EntityCategory, CreateDetailInput{
		Code: "CH" + newID(), Name: "分类子项", TargetEntity: EntityProduct, ParentID: categoryRoot.ObjectID,
	}, "category-child")
	categoryEdit, err := service.Edit(t.Context(), EntityCategory, ObjectRevisionInput{
		ObjectID: categoryRoot.ObjectID, ObjectRevision: categoryRootApproved.ObjectRevision,
	}, integrationActorOne, "category-cycle-edit")
	if err != nil {
		t.Fatalf("edit category root: %v", err)
	}
	if _, err = service.Save(t.Context(), EntityCategory, SaveInput{
		ObjectID: categoryEdit.ObjectID, VersionID: categoryEdit.VersionID, Revision: categoryEdit.Revision,
		Data: DetailInput{
			Name: "分类根", ParentID: Optional(categoryChild.ObjectID),
		},
	}, integrationActorOne, "category-cycle-save"); !errorIsKind(err, ErrorConflict) {
		t.Fatalf("category cycle error = %v", err)
	}

	departmentRoot, departmentRootApproved := createApprovedIntegration(t, service, EntityDepartment, CreateDetailInput{
		Code: "DR" + newID(), Name: "部门根",
	}, "department-root")
	departmentChild, _ := createApprovedIntegration(t, service, EntityDepartment, CreateDetailInput{
		Code: "DH" + newID(), Name: "部门子项", ParentID: departmentRoot.ObjectID,
	}, "department-child")
	departmentEdit, err := service.Edit(t.Context(), EntityDepartment, ObjectRevisionInput{
		ObjectID: departmentRoot.ObjectID, ObjectRevision: departmentRootApproved.ObjectRevision,
	}, integrationActorOne, "department-cycle-edit")
	if err != nil {
		t.Fatalf("edit department root: %v", err)
	}
	if _, err = service.Save(t.Context(), EntityDepartment, SaveInput{
		ObjectID: departmentEdit.ObjectID, VersionID: departmentEdit.VersionID, Revision: departmentEdit.Revision,
		Data: DetailInput{Name: "部门根", ParentID: Optional(departmentChild.ObjectID)},
	}, integrationActorOne, "department-cycle-save"); !errorIsKind(err, ErrorConflict) {
		t.Fatalf("department cycle error = %v", err)
	}
}

func TestCommonAttributeSchemaAndPermissionsIntegration(t *testing.T) {
	pool := integrationPool(t)

	for _, table := range []string{
		"bob_category_versions",
		"bob_department_versions",
		"bob_position_versions",
	} {
		var relation *string
		if err := pool.QueryRow(t.Context(), "select to_regclass($1)::text", table).Scan(&relation); err != nil {
			t.Fatalf("read %s: %v", table, err)
		}
		if relation == nil || *relation != table {
			t.Fatalf("%s relation = %v", table, relation)
		}
	}

	expected := map[string]int{}
	actions := []string{
		"approve", "audit-history", "create", "delete", "edit", "get",
		"query", "reject", "save", "submit", "versions",
	}
	for entity, first := range map[string]int{
		EntityCategory: 89, EntityDepartment: 100, EntityPosition: 111,
	} {
		for offset, action := range actions {
			expected["/bob/"+entity+"/"+action] = first + offset
		}
	}
	rows, err := pool.Query(t.Context(), `
		SELECT id, path
		FROM app_permissions
		WHERE domain = 'bob' AND entity IN ('category', 'department', 'position')
	`)
	if err != nil {
		t.Fatalf("query common attribute permissions: %v", err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var id, path string
		if err = rows.Scan(&id, &path); err != nil {
			t.Fatalf("scan common attribute permission: %v", err)
		}
		sequence, ok := expected[path]
		expectedID := "01JBOB" + fmt.Sprintf("%020d", sequence)
		if !ok || id != expectedID {
			t.Fatalf("permission id=%s path=%s expected=%s", id, path, expectedID)
		}
		seen++
	}
	if err = rows.Err(); err != nil {
		t.Fatalf("iterate common attribute permissions: %v", err)
	}
	if seen != 33 {
		t.Fatalf("common attribute permission count = %d, want 33", seen)
	}

	var grants int
	if err = pool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM app_role_permissions rp
		JOIN app_permissions p ON p.id = rp.permission_id
		WHERE p.domain = 'bob' AND p.entity IN ('category', 'department', 'position')
	`).Scan(&grants); err != nil {
		t.Fatalf("count common attribute role grants: %v", err)
	}
	if grants != 0 {
		t.Fatalf("new common attribute permissions were granted item-by-item: %d", grants)
	}
}

func TestCurrentIdentifierUniquenessAndHistoryReleaseIntegration(t *testing.T) {
	service := NewService(integrationPool(t))

	product, productApproved := createApprovedIntegration(t, service, EntityProduct, CreateDetailInput{
		Code: "PU" + newID(), Name: "唯一条码产品", Unit: "件", Barcode: " barcode-" + newID(),
	}, "identifier-product")
	productView, err := service.Get(t.Context(), EntityProduct, GetInput{ObjectID: product.ObjectID})
	if err != nil {
		t.Fatalf("get identifier product: %v", err)
	}
	originalBarcode := productView.Data.Barcode
	if originalBarcode != strings.ToUpper(strings.TrimSpace(originalBarcode)) {
		t.Fatalf("barcode was not normalized: %q", originalBarcode)
	}
	if _, err = service.Create(t.Context(), EntityProduct, CreateInput{Data: CreateDetailInput{
		Code: "PD" + newID(), Name: "重复条码产品", Unit: "件", Barcode: strings.ToLower(originalBarcode),
	}}, integrationActorOne, "duplicate-barcode"); !errorIsKind(err, ErrorConflict) {
		t.Fatalf("duplicate barcode error = %v", err)
	}
	productEdit, err := service.Edit(t.Context(), EntityProduct, ObjectRevisionInput{
		ObjectID: product.ObjectID, ObjectRevision: productApproved.ObjectRevision,
	}, integrationActorOne, "release-barcode-edit")
	if err != nil {
		t.Fatalf("edit identifier product: %v", err)
	}
	productSaved, err := service.Save(t.Context(), EntityProduct, SaveInput{
		ObjectID: product.ObjectID, VersionID: productEdit.VersionID, Revision: productEdit.Revision,
		Data: DetailInput{
			Name: "唯一条码产品", Unit: "件", Barcode: Optional("BARCODE-" + newID()),
		},
	}, integrationActorOne, "release-barcode-save")
	if err != nil {
		t.Fatalf("save replacement barcode: %v", err)
	}
	productSubmitted, _ := service.Submit(t.Context(), EntityProduct, VersionRevisionInput{
		ObjectID: product.ObjectID, VersionID: productEdit.VersionID, Revision: productSaved.Revision,
	}, integrationActorOne, "release-barcode-submit")
	if _, err = service.Approve(t.Context(), EntityProduct, ReviewInput{
		ObjectID: product.ObjectID, VersionID: productEdit.VersionID, Revision: productSubmitted.Revision,
	}, integrationActorTwo, "release-barcode-approve"); err != nil {
		t.Fatalf("approve replacement barcode: %v", err)
	}
	if _, err = service.Create(t.Context(), EntityProduct, CreateInput{Data: CreateDetailInput{
		Code: "PR" + newID(), Name: "复用历史条码产品", Unit: "件", Barcode: originalBarcode,
	}}, integrationActorOne, "reuse-historical-barcode"); err != nil {
		t.Fatalf("historical barcode was not released: %v", err)
	}

	platform, _ := createApprovedIntegration(t, service, EntitySupplier, CreateDetailInput{
		Code: "VP" + newID(), Name: "VIN 测试平台",
		SupplierType: stringIntegrationPointer(SupplierTypeLogisticsPlatform),
	}, "identifier-platform")
	vinSuffix := strings.ReplaceAll(newID()[20:], "Q", "A")
	vin := "LSVAA4187N2" + vinSuffix
	vehicle, err := service.Create(t.Context(), EntityVehicle, CreateInput{Data: CreateDetailInput{
		Code: "VU" + newID(), Name: "唯一 VIN 车辆", PlateNumber: "沪B" + newID()[20:],
		VehicleType: "货车", PlatformObjectID: platform.ObjectID, VIN: strings.ToLower(vin),
	}}, integrationActorOne, "identifier-vehicle")
	if err != nil {
		t.Fatalf("create unique VIN vehicle: %v cause=%v vin=%q", err, errors.Unwrap(err), vin)
	}
	vehicleView, _ := service.Get(t.Context(), EntityVehicle, GetInput{ObjectID: vehicle.ObjectID})
	if vehicleView.Data.VIN != vin {
		t.Fatalf("VIN normalization = %q, want %q", vehicleView.Data.VIN, vin)
	}
	if _, err = service.Create(t.Context(), EntityVehicle, CreateInput{Data: CreateDetailInput{
		Code: "VD" + newID(), Name: "重复 VIN 车辆", PlateNumber: "沪C" + newID()[20:],
		VehicleType: "货车", PlatformObjectID: platform.ObjectID, VIN: vin,
	}}, integrationActorOne, "duplicate-vin"); !errorIsKind(err, ErrorConflict) {
		t.Fatalf("duplicate VIN error = %v", err)
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
		"delete":        86,
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
		var storedGrantCount int
		if err = pool.QueryRow(t.Context(), `
			SELECT count(*)
			FROM app_role_permissions rp
			JOIN app_roles r ON r.id = rp.role_id
			JOIN app_permissions p ON p.id = rp.permission_id
			WHERE r.code = 'superadmin' AND p.domain = 'bob' AND p.entity = 'warehouse'
			  AND p.action <> 'delete'
		`).Scan(&storedGrantCount); err != nil {
			t.Fatalf("count stored superadmin warehouse grants: %v", err)
		}
		if storedGrantCount != 0 {
			t.Fatalf("stored superadmin warehouse grants = %d, want 0", storedGrantCount)
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
		"delete":        87,
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
		var storedGrantCount int
		if err = pool.QueryRow(t.Context(), `
			SELECT count(*)
			FROM app_role_permissions rp
			JOIN app_roles r ON r.id = rp.role_id
			JOIN app_permissions p ON p.id = rp.permission_id
			WHERE r.code = 'superadmin' AND p.domain = 'bob' AND p.entity = 'vehicle'
			  AND p.action <> 'delete'
		`).Scan(&storedGrantCount); err != nil {
			t.Fatalf("count stored superadmin vehicle grants: %v", err)
		}
		if storedGrantCount != 0 {
			t.Fatalf("stored superadmin vehicle grants = %d, want 0", storedGrantCount)
		}
	}
}

func TestDeletePermissionCatalogIntegration(t *testing.T) {
	pool := integrationPool(t)
	rows, err := pool.Query(t.Context(), `
		SELECT id, entity, path, status
		FROM app_permissions
		WHERE domain = 'bob' AND action = 'delete'
		ORDER BY id
	`)
	if err != nil {
		t.Fatalf("query delete permissions: %v", err)
	}
	defer rows.Close()

	expected := []struct {
		entity   string
		sequence int
	}{
		{EntityCustomer, 81},
		{EntitySupplier, 82},
		{EntityEmployee, 83},
		{EntityProduct, 84},
		{EntityService, 85},
		{EntityWarehouse, 86},
		{EntityVehicle, 87},
		{EntityFundAccount, 88},
		{EntityCategory, 92},
		{EntityDepartment, 103},
		{EntityPosition, 114},
		{EntitySettlementMethod, 125},
	}
	index := 0
	for rows.Next() {
		var id, entity, path, status string
		if err = rows.Scan(&id, &entity, &path, &status); err != nil {
			t.Fatalf("scan delete permission: %v", err)
		}
		if index >= len(expected) {
			t.Fatalf("unexpected extra delete permission %q", path)
		}
		if id != fmt.Sprintf("01JBOB%020d", expected[index].sequence) ||
			entity != expected[index].entity ||
			path != "/bob/"+entity+"/delete" ||
			status != "ENABLED" {
			t.Fatalf("delete permission %d: id=%q entity=%q path=%q status=%q", index, id, entity, path, status)
		}
		index++
	}
	if err = rows.Err(); err != nil {
		t.Fatalf("iterate delete permissions: %v", err)
	}
	if index != len(expected) {
		t.Fatalf("delete permission count = %d, want %d", index, len(expected))
	}
}

func TestDeleteFirstDraftEveryEntityIntegration(t *testing.T) {
	pool := integrationPool(t)
	service := NewService(pool)
	_, salesperson := createApprovedIntegration(t, service, EntityEmployee, CreateDetailInput{
		Code: "DSE" + newID(), Name: "Delete Salesperson",
	}, "delete-salesperson")
	platform, _ := createApprovedIntegration(t, service, EntitySupplier, CreateDetailInput{
		Code:         "DP" + newID(),
		Name:         "Delete Vehicle Platform",
		SupplierType: stringIntegrationPointer(SupplierTypeLogisticsPlatform),
	}, "delete-platform")

	for _, entity := range entities {
		t.Run(entity, func(t *testing.T) {
			created, err := service.Create(
				t.Context(),
				entity,
				CreateInput{Data: deleteIntegrationData(entity, platform.ObjectID, salesperson.ObjectID)},
				integrationActorOne,
				"delete-create-"+entity,
			)
			if err != nil {
				t.Fatalf("create %s draft: %v", entity, err)
			}
			if entity == EntityCustomer {
				created, err = service.Save(t.Context(), entity, SaveInput{
					ObjectID:  created.ObjectID,
					VersionID: created.VersionID,
					Revision:  created.Revision,
					Data:      DetailInput{Name: "Saved Before Delete"},
				}, integrationActorOne, "delete-save-customer")
				if err != nil {
					t.Fatalf("save deletable draft: %v", err)
				}
			}
			if err = service.Delete(t.Context(), entity, DeleteInput{
				ObjectID:       created.ObjectID,
				ObjectRevision: created.ObjectRevision,
				VersionID:      created.VersionID,
				Revision:       created.Revision,
			}); err != nil {
				t.Fatalf("delete %s first draft: %v cause=%v", entity, err, errors.Unwrap(err))
			}
			assertBobAggregateCounts(t, pool, created.ObjectID, created.VersionID, 0, 0, 0, 0)
		})
	}
}

func TestDeleteFirstDraftRejectsLifecycleAndIdentityConflictsIntegration(t *testing.T) {
	pool := integrationPool(t)
	service := NewService(pool)
	_, salesperson := createApprovedIntegration(t, service, EntityEmployee, CreateDetailInput{
		Code: "DCE" + newID(), Name: "Delete Conflict Salesperson",
	}, "delete-conflict-salesperson")

	newCustomer := func(prefix string) MutationResult {
		t.Helper()
		created, err := service.Create(t.Context(), EntityCustomer, CreateInput{Data: CreateDetailInput{
			Code:                  prefix + newID(),
			Name:                  prefix + " Customer",
			SalespersonEmployeeID: salesperson.ObjectID,
		}}, integrationActorOne, prefix+"-create")
		if err != nil {
			t.Fatalf("create %s customer: %v", prefix, err)
		}
		return created
	}
	deleteInput := func(result MutationResult) DeleteInput {
		return DeleteInput{
			ObjectID:       result.ObjectID,
			ObjectRevision: result.ObjectRevision,
			VersionID:      result.VersionID,
			Revision:       result.Revision,
		}
	}
	assertConflict := func(name, entity string, input DeleteInput) {
		t.Helper()
		if err := service.Delete(t.Context(), entity, input); !errorIsKind(err, ErrorConflict) {
			t.Fatalf("%s error = %v, want conflict", name, err)
		}
		assertBobAggregatePresent(t, pool, input.ObjectID, input.VersionID)
	}

	t.Run("object revision", func(t *testing.T) {
		created := newCustomer("DOR")
		input := deleteInput(created)
		input.ObjectRevision++
		assertConflict("object revision", EntityCustomer, input)
	})
	t.Run("version revision", func(t *testing.T) {
		created := newCustomer("DVR")
		input := deleteInput(created)
		input.Revision++
		assertConflict("version revision", EntityCustomer, input)
	})
	t.Run("object and version mismatch", func(t *testing.T) {
		first := newCustomer("DIM1")
		second := newCustomer("DIM2")
		input := deleteInput(first)
		input.VersionID = second.VersionID
		if err := service.Delete(t.Context(), EntityCustomer, input); !errorIsKind(err, ErrorValidation) {
			t.Fatalf("mismatched version error = %v", err)
		}
		assertBobAggregateCounts(t, pool, first.ObjectID, first.VersionID, 1, 1, 1, 1)
		assertBobAggregateCounts(t, pool, second.ObjectID, second.VersionID, 1, 1, 1, 1)
	})
	t.Run("entity mismatch", func(t *testing.T) {
		created := newCustomer("DEM")
		if err := service.Delete(t.Context(), EntitySupplier, deleteInput(created)); !errorIsKind(err, ErrorValidation) {
			t.Fatalf("entity mismatch error = %v", err)
		}
		assertBobAggregateCounts(t, pool, created.ObjectID, created.VersionID, 1, 1, 1, 1)
	})
	t.Run("pending after submit", func(t *testing.T) {
		created := newCustomer("DPN")
		submitted, err := service.Submit(t.Context(), EntityCustomer, VersionRevisionInput{
			ObjectID: created.ObjectID, VersionID: created.VersionID, Revision: created.Revision,
		}, integrationActorOne, "delete-pending-submit")
		if err != nil {
			t.Fatalf("submit pending delete case: %v", err)
		}
		assertConflict("pending", EntityCustomer, deleteInput(submitted))
	})
	t.Run("reviewed and rejected", func(t *testing.T) {
		created := newCustomer("DRJ")
		submitted, err := service.Submit(t.Context(), EntityCustomer, VersionRevisionInput{
			ObjectID: created.ObjectID, VersionID: created.VersionID, Revision: created.Revision,
		}, integrationActorOne, "delete-rejected-submit")
		if err != nil {
			t.Fatalf("submit rejected delete case: %v", err)
		}
		comment := "reject delete case"
		rejected, err := service.Reject(t.Context(), EntityCustomer, ReviewInput{
			ObjectID: created.ObjectID, VersionID: created.VersionID, Revision: submitted.Revision, Comment: &comment,
		}, integrationActorTwo, "delete-rejected-review")
		if err != nil {
			t.Fatalf("reject delete case: %v", err)
		}
		assertConflict("rejected", EntityCustomer, deleteInput(rejected))
	})
	t.Run("effective version", func(t *testing.T) {
		created, approved := createApprovedIntegration(t, service, EntityCustomer, CreateDetailInput{
			Code: "DEF" + newID(), Name: "Effective Delete Customer",
		}, "delete-effective")
		input := deleteInput(approved)
		input.VersionID = created.VersionID
		assertConflict("effective", EntityCustomer, input)
	})
	t.Run("multiple versions and version two", func(t *testing.T) {
		created, approved := createApprovedIntegration(t, service, EntityCustomer, CreateDetailInput{
			Code: "DMV" + newID(), Name: "Multiple Version Customer",
		}, "delete-multiple")
		edited, err := service.Edit(t.Context(), EntityCustomer, ObjectRevisionInput{
			ObjectID: created.ObjectID, ObjectRevision: approved.ObjectRevision,
		}, integrationActorOne, "delete-multiple-edit")
		if err != nil {
			t.Fatalf("edit multiple-version delete case: %v", err)
		}
		assertConflict("multiple versions", EntityCustomer, deleteInput(edited))
	})
}

func TestDeleteFirstDraftRejectsVOUReferenceAndPreservesDataIntegration(t *testing.T) {
	pool := integrationPool(t)
	service := NewService(pool)
	_, salesperson := createApprovedIntegration(t, service, EntityEmployee, CreateDetailInput{
		Code: "DRE" + newID(), Name: "Referenced Draft Salesperson",
	}, "delete-reference-salesperson")
	created, err := service.Create(t.Context(), EntityCustomer, CreateInput{Data: CreateDetailInput{
		Code:                  "DREF" + newID(),
		Name:                  "Referenced Draft Customer",
		SalespersonEmployeeID: salesperson.ObjectID,
	}}, integrationActorOne, "delete-reference-create")
	if err != nil {
		t.Fatalf("create referenced draft: %v", err)
	}
	documentID := insertSaleOrderReferenceIntegration(t, pool, created)
	t.Cleanup(func() {
		deleteVOUTestDocument(t, pool, documentID)
	})

	err = service.Delete(t.Context(), EntityCustomer, DeleteInput{
		ObjectID:       created.ObjectID,
		ObjectRevision: created.ObjectRevision,
		VersionID:      created.VersionID,
		Revision:       created.Revision,
	})
	if !errorIsKind(err, ErrorConflict) {
		t.Fatalf("delete referenced draft error = %v", err)
	}
	assertBobAggregateCounts(t, pool, created.ObjectID, created.VersionID, 1, 1, 1, 1)
	var references int
	if err = pool.QueryRow(t.Context(), `
		SELECT count(*) FROM vou_sale_order_details WHERE document_id = $1
	`, documentID).Scan(&references); err != nil || references != 1 {
		t.Fatalf("VOU reference count=%d err=%v", references, err)
	}
}

func TestDeleteFirstDraftRollbackAfterPartialWorkIntegration(t *testing.T) {
	pool := integrationPool(t)
	service := NewService(pool)
	_, salesperson := createApprovedIntegration(t, service, EntityEmployee, CreateDetailInput{
		Code: "DRBE" + newID(), Name: "Rollback Salesperson",
	}, "delete-rollback-salesperson")
	created, err := service.Create(t.Context(), EntityCustomer, CreateInput{Data: CreateDetailInput{
		Code:                  "DRB" + newID(),
		Name:                  "Rollback Delete Customer",
		SalespersonEmployeeID: salesperson.ObjectID,
	}}, integrationActorOne, "delete-rollback-create")
	if err != nil {
		t.Fatalf("create rollback draft: %v", err)
	}
	service.afterDeleteDetailsHook = func() error {
		return errors.New("injected delete failure")
	}
	err = service.Delete(t.Context(), EntityCustomer, DeleteInput{
		ObjectID:       created.ObjectID,
		ObjectRevision: created.ObjectRevision,
		VersionID:      created.VersionID,
		Revision:       created.Revision,
	})
	if !errorIsKind(err, ErrorInternal) {
		t.Fatalf("injected delete error = %v", err)
	}
	assertBobAggregateCounts(t, pool, created.ObjectID, created.VersionID, 1, 1, 1, 1)
}

func TestDeleteFirstDraftConcurrencyIntegration(t *testing.T) {
	pool := integrationPool(t)
	service := NewService(pool)
	_, salesperson := createApprovedIntegration(t, service, EntityEmployee, CreateDetailInput{
		Code: "DCCE" + newID(), Name: "Delete Concurrency Salesperson",
	}, "delete-concurrency-salesperson")
	tests := []struct {
		name   string
		action func(MutationResult) error
	}{
		{
			name: "save",
			action: func(created MutationResult) error {
				_, err := service.Save(context.Background(), EntityCustomer, SaveInput{
					ObjectID: created.ObjectID, VersionID: created.VersionID, Revision: created.Revision,
					Data: DetailInput{Name: "Concurrent Save"},
				}, integrationActorOne, "delete-concurrent-save")
				return err
			},
		},
		{
			name: "submit",
			action: func(created MutationResult) error {
				_, err := service.Submit(context.Background(), EntityCustomer, VersionRevisionInput{
					ObjectID: created.ObjectID, VersionID: created.VersionID, Revision: created.Revision,
				}, integrationActorOne, "delete-concurrent-submit")
				return err
			},
		},
		{
			name: "edit",
			action: func(created MutationResult) error {
				_, err := service.Edit(context.Background(), EntityCustomer, ObjectRevisionInput{
					ObjectID: created.ObjectID, ObjectRevision: created.ObjectRevision,
				}, integrationActorOne, "delete-concurrent-edit")
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			created, err := service.Create(t.Context(), EntityCustomer, CreateInput{Data: CreateDetailInput{
				Code:                  "DC" + newID(),
				Name:                  "Concurrent Delete Customer",
				SalespersonEmployeeID: salesperson.ObjectID,
			}}, integrationActorOne, "delete-concurrent-create")
			if err != nil {
				t.Fatalf("create concurrent delete draft: %v", err)
			}
			start := make(chan struct{})
			results := make(chan error, 2)
			go func() {
				<-start
				results <- service.Delete(context.Background(), EntityCustomer, DeleteInput{
					ObjectID:       created.ObjectID,
					ObjectRevision: created.ObjectRevision,
					VersionID:      created.VersionID,
					Revision:       created.Revision,
				})
			}()
			go func() {
				<-start
				results <- test.action(created)
			}()
			close(start)
			successes := 0
			for range 2 {
				if resultErr := <-results; resultErr == nil {
					successes++
				} else if !errorIsKind(resultErr, ErrorConflict) && !errorIsKind(resultErr, ErrorValidation) {
					t.Fatalf("unexpected concurrent error: %v", resultErr)
				}
			}
			if successes != 1 {
				t.Fatalf("concurrent successes = %d, want 1", successes)
			}
			var objectCount int
			if err = pool.QueryRow(t.Context(), `SELECT count(*) FROM bob_objects WHERE id = $1`, created.ObjectID).Scan(&objectCount); err != nil {
				t.Fatalf("count concurrent object: %v", err)
			}
			if objectCount == 0 {
				assertBobAggregateCounts(t, pool, created.ObjectID, created.VersionID, 0, 0, 0, 0)
			} else {
				var versionCount, detailCount int
				if err = pool.QueryRow(t.Context(), `
					SELECT
						(SELECT count(*) FROM bob_versions WHERE object_id = $1),
						(SELECT count(*) FROM bob_customer_versions WHERE version_id = $2)
				`, created.ObjectID, created.VersionID).Scan(&versionCount, &detailCount); err != nil {
					t.Fatalf("count concurrent aggregate: %v", err)
				}
				if versionCount != 1 || detailCount != 1 {
					t.Fatalf("concurrent aggregate version=%d detail=%d", versionCount, detailCount)
				}
			}
		})
	}
}

func TestConcurrentEditAllowsOneWinnerIntegration(t *testing.T) {
	pool := integrationPool(t)
	service := NewService(pool)
	_, salesperson := createApprovedIntegration(t, service, EntityEmployee, CreateDetailInput{
		Code: "CCE" + newID(), Name: "Concurrent Edit Salesperson",
	}, "concurrent-salesperson")
	created, err := service.Create(t.Context(), EntityCustomer, CreateInput{Data: CreateDetailInput{
		Code: "CC" + newID(), Name: "Concurrent Customer",
		SalespersonEmployeeID: salesperson.ObjectID,
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
	_, salesperson := createApprovedIntegration(t, service, EntityEmployee, CreateDetailInput{
		Code: "RLE" + newID(), Name: "Reference Lock Salesperson",
	}, "lock-salesperson")
	created, err := service.Create(t.Context(), EntityCustomer, CreateInput{Data: CreateDetailInput{
		Code: "RL" + newID(), Name: "Reference Lock Customer",
		SalespersonEmployeeID: salesperson.ObjectID,
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
	_, salesperson := createApprovedIntegration(t, service, EntityEmployee, CreateDetailInput{
		Code: "DUE" + newID(), Name: "Duplicate Code Salesperson",
	}, "duplicate-salesperson")
	code := "DU" + newID()
	if _, err := service.Create(t.Context(), EntityCustomer, CreateInput{Data: CreateDetailInput{
		Code: code, Name: "Original", SalespersonEmployeeID: salesperson.ObjectID,
	}}, integrationActorOne, "duplicate-create-original"); err != nil {
		t.Fatalf("create original: %v", err)
	}
	if _, err := service.Create(t.Context(), EntityCustomer, CreateInput{Data: CreateDetailInput{
		Code: code, Name: "Duplicate", SalespersonEmployeeID: salesperson.ObjectID,
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

func deleteIntegrationData(entity, platformObjectID, salespersonEmployeeID string) CreateDetailInput {
	data := CreateDetailInput{
		Code: "DEL" + newID(),
		Name: "Deletable " + entity,
	}
	switch entity {
	case EntityCustomer, EntitySupplier:
		data.SalespersonEmployeeID = salespersonEmployeeID
	case EntityProduct, EntityService:
		data.Unit = "unit"
	case EntityFundAccount:
		data.Currency = "CNY"
	case EntityVehicle:
		data.PlateNumber = "沪D" + newID()
		data.VehicleType = "Truck"
		data.PlatformObjectID = platformObjectID
	case EntityCategory:
		data.TargetEntity = EntityProduct
	case EntitySettlementMethod:
		data.RuleType = SettlementRuleRelativeDays
		data.DayOffset = 30
	}
	return data
}

func assertBobAggregateCounts(
	t *testing.T,
	pool *pgxpool.Pool,
	objectID, versionID string,
	wantObjects, wantVersions, wantDetails, wantAudits int,
) {
	t.Helper()
	var objects, versions, details, audits int
	err := pool.QueryRow(t.Context(), `
		SELECT
			(SELECT count(*) FROM bob_objects WHERE id = $1),
			(SELECT count(*) FROM bob_versions WHERE id = $2 AND object_id = $1),
			(SELECT count(*) FROM bob_customer_versions WHERE version_id = $2) +
			(SELECT count(*) FROM bob_supplier_versions WHERE version_id = $2) +
			(SELECT count(*) FROM bob_employee_versions WHERE version_id = $2) +
			(SELECT count(*) FROM bob_product_versions WHERE version_id = $2) +
			(SELECT count(*) FROM bob_service_versions WHERE version_id = $2) +
			(SELECT count(*) FROM bob_warehouse_versions WHERE version_id = $2) +
			(SELECT count(*) FROM bob_vehicle_versions WHERE version_id = $2) +
			(SELECT count(*) FROM bob_fund_account_versions WHERE version_id = $2) +
			(SELECT count(*) FROM bob_category_versions WHERE version_id = $2) +
			(SELECT count(*) FROM bob_department_versions WHERE version_id = $2) +
			(SELECT count(*) FROM bob_position_versions WHERE version_id = $2) +
			(SELECT count(*) FROM bob_settlement_method_versions WHERE version_id = $2),
			(SELECT count(*) FROM bob_audit_events WHERE object_id = $1 AND version_id = $2)
	`, objectID, versionID).Scan(&objects, &versions, &details, &audits)
	if err != nil {
		t.Fatalf("count BOB aggregate: %v", err)
	}
	if objects != wantObjects || versions != wantVersions || details != wantDetails || audits != wantAudits {
		t.Fatalf(
			"aggregate counts object=%d version=%d detail=%d audit=%d, want %d/%d/%d/%d",
			objects, versions, details, audits,
			wantObjects, wantVersions, wantDetails, wantAudits,
		)
	}
}

func assertBobAggregatePresent(t *testing.T, pool *pgxpool.Pool, objectID, versionID string) {
	t.Helper()
	var objects, versions, details, audits int
	err := pool.QueryRow(t.Context(), `
		SELECT
			(SELECT count(*) FROM bob_objects WHERE id = $1),
			(SELECT count(*) FROM bob_versions WHERE id = $2 AND object_id = $1),
			(SELECT count(*) FROM bob_customer_versions WHERE version_id = $2) +
			(SELECT count(*) FROM bob_supplier_versions WHERE version_id = $2) +
			(SELECT count(*) FROM bob_employee_versions WHERE version_id = $2) +
			(SELECT count(*) FROM bob_product_versions WHERE version_id = $2) +
			(SELECT count(*) FROM bob_service_versions WHERE version_id = $2) +
			(SELECT count(*) FROM bob_warehouse_versions WHERE version_id = $2) +
			(SELECT count(*) FROM bob_vehicle_versions WHERE version_id = $2) +
			(SELECT count(*) FROM bob_fund_account_versions WHERE version_id = $2),
			(SELECT count(*) FROM bob_audit_events WHERE object_id = $1 AND version_id = $2)
	`, objectID, versionID).Scan(&objects, &versions, &details, &audits)
	if err != nil {
		t.Fatalf("count preserved BOB aggregate: %v", err)
	}
	if objects != 1 || versions != 1 || details != 1 || audits < 1 {
		t.Fatalf("preserved aggregate counts object=%d version=%d detail=%d audit=%d", objects, versions, details, audits)
	}
}

func insertSaleOrderReferenceIntegration(t *testing.T, pool *pgxpool.Pool, target MutationResult) string {
	t.Helper()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin VOU reference insert: %v", err)
	}
	defer tx.Rollback(t.Context()) //nolint:errcheck
	documentID := newID()
	if _, err = tx.Exec(t.Context(), `
		INSERT INTO vou_documents (
			id, entity, document_no, business_date, currency, total_amount_cents, created_by, updated_by
		) VALUES ($1, 'sale-order', $2, current_date, 'CNY', 100, $3, $3)
	`, documentID, "D"+newID(), integrationActorOne); err != nil {
		t.Fatalf("insert VOU reference document: %v", err)
	}
	if _, err = tx.Exec(t.Context(), `
		INSERT INTO vou_sale_order_details (
			document_id, customer_object_id, customer_version_id, customer_code, customer_name
		) VALUES ($1, $2, $3, 'DRAFT-REFERENCE', 'Draft Reference')
	`, documentID, target.ObjectID, target.VersionID); err != nil {
		t.Fatalf("insert VOU reference detail: %v", err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatalf("commit VOU reference insert: %v", err)
	}
	return documentID
}

func deleteVOUTestDocument(t *testing.T, pool *pgxpool.Pool, documentID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Errorf("begin VOU test cleanup: %v", err)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err = tx.Exec(ctx, `DELETE FROM vou_sale_order_details WHERE document_id = $1`, documentID); err != nil {
		t.Errorf("delete VOU reference detail: %v", err)
		return
	}
	if _, err = tx.Exec(ctx, `DELETE FROM vou_documents WHERE id = $1`, documentID); err != nil {
		t.Errorf("delete VOU reference document: %v", err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		t.Errorf("commit VOU test cleanup: %v", err)
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
	if (entity == EntityCustomer || entity == EntitySupplier) && data.SalespersonEmployeeID == "" {
		_, employee := createApprovedIntegration(t, service, EntityEmployee, CreateDetailInput{
			Code: "AUTOEMP" + newID(), Name: "Integration Salesperson",
		}, requestPrefix+"-salesperson")
		data.SalespersonEmployeeID = employee.ObjectID
	}
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
