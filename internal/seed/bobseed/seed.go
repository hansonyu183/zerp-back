package bobseed

import (
	"context"
	"errors"
	"fmt"

	dbsqlc "github.com/hansonyu183/zerp-back/internal/database/sqlc"
	"github.com/hansonyu183/zerp-back/internal/domains/bob"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	submitterID = "01J00000000000000000000000"
	reviewerID  = "01J00000000000000000000001"
)

type Result struct {
	Created int
	Resumed int
	Skipped int
}

type lifecycleService interface {
	Create(context.Context, string, bob.CreateInput, string, string) (bob.MutationResult, error)
	Get(context.Context, string, bob.GetInput) (bob.ObjectView, error)
	Edit(context.Context, string, bob.ObjectRevisionInput, string, string) (bob.MutationResult, error)
	Save(context.Context, string, bob.SaveInput, string, string) (bob.MutationResult, error)
	Submit(context.Context, string, bob.VersionRevisionInput, string, string) (bob.MutationResult, error)
	Approve(context.Context, string, bob.ReviewInput, string, string) (bob.MutationResult, error)
	Reject(context.Context, string, bob.ReviewInput, string, string) (bob.MutationResult, error)
}

type objectLookup interface {
	Find(context.Context, string, string) (string, bool, error)
}

type queryLookup struct {
	queries *dbsqlc.Queries
}

func (l queryLookup) Find(ctx context.Context, entity, code string) (string, bool, error) {
	id, err := l.queries.FindBobObjectIDByCode(ctx, dbsqlc.FindBobObjectIDByCodeParams{
		Entity: entity,
		Code:   code,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return id, err == nil, err
}

type Seeder struct {
	service lifecycleService
	lookup  objectLookup
}

func New(pool *pgxpool.Pool) *Seeder {
	return &Seeder{
		service: bob.NewService(pool),
		lookup:  queryLookup{queries: dbsqlc.New(pool)},
	}
}

type sample struct {
	entity       string
	data         bob.CreateDetailInput
	status       string
	platformCode string
}

var samples = [...]sample{
	{entity: bob.EntityCustomer, data: bob.CreateDetailInput{Code: "DEMO-CUST-001", Name: "星河零售有限公司"}, status: bob.StatusEffective},
	{entity: bob.EntityCustomer, data: bob.CreateDetailInput{Code: "DEMO-CUST-002", Name: "新客户（草稿）"}, status: bob.StatusDraft},
	{entity: bob.EntitySupplier, data: bob.CreateDetailInput{
		Code: "DEMO-SUP-001", Name: "自营物流平台", SupplierType: stringPointer(bob.SupplierTypeLogisticsPlatform),
	}, status: bob.StatusEffective},
	{entity: bob.EntitySupplier, data: bob.CreateDetailInput{Code: "DEMO-SUP-002", Name: "待审核供应商"}, status: bob.StatusPending},
	{entity: bob.EntityEmployee, data: bob.CreateDetailInput{Code: "DEMO-EMP-001", Name: "张伟"}, status: bob.StatusEffective},
	{entity: bob.EntityEmployee, data: bob.CreateDetailInput{Code: "DEMO-EMP-002", Name: "李娜（已驳回）"}, status: bob.StatusRejected},
	{entity: bob.EntityProduct, data: bob.CreateDetailInput{Code: "DEMO-PROD-001", Name: "标准零件 A", Unit: "件"}, status: bob.StatusEffective},
	{entity: bob.EntityProduct, data: bob.CreateDetailInput{Code: "DEMO-PROD-002", Name: "试制零件 B", Unit: "件"}, status: bob.StatusDraft},
	{entity: bob.EntityService, data: bob.CreateDetailInput{Code: "DEMO-SVC-001", Name: "设备巡检服务", Unit: "次"}, status: bob.StatusEffective},
	{entity: bob.EntityService, data: bob.CreateDetailInput{Code: "DEMO-SVC-002", Name: "年度维保服务", Unit: "年"}, status: bob.StatusPending},
	{entity: bob.EntityWarehouse, data: bob.CreateDetailInput{Code: "DEMO-WH-001", Name: "华东主仓"}, status: bob.StatusEffective},
	{entity: bob.EntityWarehouse, data: bob.CreateDetailInput{Code: "DEMO-WH-002", Name: "临时仓（已驳回）"}, status: bob.StatusRejected},
	{entity: bob.EntityVehicle, data: bob.CreateDetailInput{
		Code: "DEMO-VEH-001", Name: "自营配送一号车", PlateNumber: "沪A10001", VehicleType: "厢式货车",
	}, status: bob.StatusEffective, platformCode: "DEMO-SUP-001"},
	{entity: bob.EntityVehicle, data: bob.CreateDetailInput{
		Code: "DEMO-VEH-002", Name: "自营配送二号车", PlateNumber: "沪A10002", VehicleType: "厢式货车",
	}, status: bob.StatusDraft, platformCode: "DEMO-SUP-001"},
	{entity: bob.EntityFundAccount, data: bob.CreateDetailInput{Code: "DEMO-FA-001", Name: "人民币基本账户", Currency: "CNY"}, status: bob.StatusEffective},
	{entity: bob.EntityFundAccount, data: bob.CreateDetailInput{Code: "DEMO-FA-002", Name: "备用结算账户", Currency: "CNY"}, status: bob.StatusDraft},
}

func (s *Seeder) Seed(ctx context.Context) (Result, error) {
	var result Result
	for _, item := range samples {
		outcome, err := s.seedOne(ctx, item)
		if err != nil {
			return result, fmt.Errorf("seed %s %s: %w", item.entity, item.data.Code, err)
		}
		switch outcome {
		case outcomeCreated:
			result.Created++
		case outcomeResumed:
			result.Resumed++
		case outcomeSkipped:
			result.Skipped++
		}
	}
	return result, nil
}

type seedOutcome int

const (
	outcomeCreated seedOutcome = iota + 1
	outcomeResumed
	outcomeSkipped
)

func (s *Seeder) seedOne(ctx context.Context, item sample) (seedOutcome, error) {
	if item.platformCode != "" {
		platformObjectID, found, err := s.lookup.Find(ctx, bob.EntitySupplier, item.platformCode)
		if err != nil {
			return 0, fmt.Errorf("find logistics platform: %w", err)
		}
		if !found {
			return 0, fmt.Errorf("logistics platform %s is missing", item.platformCode)
		}
		item.data.PlatformObjectID = platformObjectID
	}

	objectID, found, err := s.lookup.Find(ctx, item.entity, item.data.Code)
	if err != nil {
		return 0, fmt.Errorf("find existing object: %w", err)
	}

	var current bob.MutationResult
	outcome := outcomeCreated
	if found {
		view, getErr := s.service.Get(ctx, item.entity, bob.GetInput{ObjectID: objectID})
		if getErr != nil {
			return 0, fmt.Errorf("get existing object: %w", getErr)
		}
		if !matches(item, view) {
			if !isLegacyPlatformSample(item, view) {
				return 0, fmt.Errorf("reserved demo code is occupied by different data")
			}
			if reconcileErr := s.reconcileLegacyPlatform(ctx, item, view); reconcileErr != nil {
				return 0, reconcileErr
			}
			return outcomeResumed, nil
		}
		if view.Version.Status == item.status {
			return outcomeSkipped, nil
		}
		current = bob.MutationResult{
			ObjectID:  view.ObjectID,
			VersionID: view.Version.VersionID,
			Status:    view.Version.Status,
			Revision:  view.Version.Revision,
		}
		outcome = outcomeResumed
	} else {
		current, err = s.service.Create(
			ctx,
			item.entity,
			bob.CreateInput{Data: item.data},
			submitterID,
			requestID(item.data.Code, "create"),
		)
		if err != nil {
			return 0, fmt.Errorf("create object: %w", err)
		}
	}

	if current.Status == bob.StatusDraft && item.status != bob.StatusDraft {
		current, err = s.service.Submit(ctx, item.entity, bob.VersionRevisionInput{
			ObjectID:  current.ObjectID,
			VersionID: current.VersionID,
			Revision:  current.Revision,
		}, submitterID, requestID(item.data.Code, "submit"))
		if err != nil {
			return 0, fmt.Errorf("submit object: %w", err)
		}
	}

	switch {
	case current.Status == item.status:
		return outcome, nil
	case current.Status == bob.StatusPending && item.status == bob.StatusEffective:
		comment := "演示数据：审核通过"
		_, err = s.service.Approve(ctx, item.entity, bob.ReviewInput{
			ObjectID:  current.ObjectID,
			VersionID: current.VersionID,
			Revision:  current.Revision,
			Comment:   &comment,
		}, reviewerID, requestID(item.data.Code, "approve"))
	case current.Status == bob.StatusPending && item.status == bob.StatusRejected:
		comment := "演示数据：审核驳回"
		_, err = s.service.Reject(ctx, item.entity, bob.ReviewInput{
			ObjectID:  current.ObjectID,
			VersionID: current.VersionID,
			Revision:  current.Revision,
			Comment:   &comment,
		}, reviewerID, requestID(item.data.Code, "reject"))
	default:
		return 0, fmt.Errorf("cannot advance status %s to %s", current.Status, item.status)
	}
	if err != nil {
		return 0, fmt.Errorf("review object: %w", err)
	}
	return outcome, nil
}

func matches(item sample, view bob.ObjectView) bool {
	expectedSupplierType := deref(item.data.SupplierType)
	if item.entity == bob.EntitySupplier && expectedSupplierType == "" {
		expectedSupplierType = bob.SupplierTypeGeneral
	}
	return view.Entity == item.entity &&
		view.Code == item.data.Code &&
		view.Data.Name == item.data.Name &&
		view.Data.Unit == item.data.Unit &&
		view.Data.Currency == item.data.Currency &&
		view.Data.SupplierType == expectedSupplierType &&
		view.Data.PlateNumber == item.data.PlateNumber &&
		view.Data.VehicleType == item.data.VehicleType &&
		view.Data.PlatformObjectID == item.data.PlatformObjectID
}

func requestID(code, action string) string {
	return "seed-bob-" + code + "-" + action
}

func isLegacyPlatformSample(item sample, view bob.ObjectView) bool {
	return item.entity == bob.EntitySupplier &&
		item.data.Code == "DEMO-SUP-001" &&
		view.Code == item.data.Code &&
		view.Data.Name == "远山供应链有限公司" &&
		(view.Data.SupplierType == "" || view.Data.SupplierType == bob.SupplierTypeGeneral) &&
		view.Version.Status == bob.StatusEffective
}

func (s *Seeder) reconcileLegacyPlatform(ctx context.Context, item sample, view bob.ObjectView) error {
	edited, err := s.service.Edit(ctx, item.entity, bob.ObjectRevisionInput{
		ObjectID: view.ObjectID, ObjectRevision: view.ObjectRevision,
	}, submitterID, requestID(item.data.Code, "upgrade-edit"))
	if err != nil {
		return fmt.Errorf("edit legacy logistics platform: %w", err)
	}
	saved, err := s.service.Save(ctx, item.entity, bob.SaveInput{
		ObjectID: edited.ObjectID, VersionID: edited.VersionID, Revision: edited.Revision,
		Data: detailInput(item.data),
	}, submitterID, requestID(item.data.Code, "upgrade-save"))
	if err != nil {
		return fmt.Errorf("save legacy logistics platform: %w", err)
	}
	submitted, err := s.service.Submit(ctx, item.entity, bob.VersionRevisionInput{
		ObjectID: saved.ObjectID, VersionID: saved.VersionID, Revision: saved.Revision,
	}, submitterID, requestID(item.data.Code, "upgrade-submit"))
	if err != nil {
		return fmt.Errorf("submit legacy logistics platform: %w", err)
	}
	if _, err = s.service.Approve(ctx, item.entity, bob.ReviewInput{
		ObjectID: submitted.ObjectID, VersionID: submitted.VersionID, Revision: submitted.Revision,
	}, reviewerID, requestID(item.data.Code, "upgrade-approve")); err != nil {
		return fmt.Errorf("approve legacy logistics platform: %w", err)
	}
	return nil
}

func detailInput(input bob.CreateDetailInput) bob.DetailInput {
	return bob.DetailInput{
		Name: input.Name, Unit: input.Unit, Currency: input.Currency,
		SupplierType: input.SupplierType, PlateNumber: input.PlateNumber,
		VehicleType: input.VehicleType, PlatformObjectID: input.PlatformObjectID,
	}
}

func stringPointer(value string) *string {
	return &value
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
