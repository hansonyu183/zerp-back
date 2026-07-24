package bob

import (
	"context"
	"encoding/json"

	dbsqlc "github.com/hansonyu183/zerp-back/internal/database/sqlc"
	"github.com/oklog/ulid/v2"
)

func insertDetail(ctx context.Context, q *dbsqlc.Queries, entity, versionID string, data DetailInput) error {
	switch entity {
	case EntityCustomer:
		return q.InsertBobCustomerDetail(ctx, dbsqlc.InsertBobCustomerDetailParams{VersionID: versionID, Name: data.Name})
	case EntitySupplier:
		return q.InsertBobSupplierDetail(ctx, dbsqlc.InsertBobSupplierDetailParams{VersionID: versionID, Name: data.Name})
	case EntityEmployee:
		return q.InsertBobEmployeeDetail(ctx, dbsqlc.InsertBobEmployeeDetailParams{VersionID: versionID, Name: data.Name})
	case EntityProduct:
		return q.InsertBobProductDetail(ctx, dbsqlc.InsertBobProductDetailParams{VersionID: versionID, Name: data.Name, Unit: data.Unit})
	case EntityService:
		return q.InsertBobServiceDetail(ctx, dbsqlc.InsertBobServiceDetailParams{VersionID: versionID, Name: data.Name, Unit: data.Unit})
	case EntityWarehouse:
		return q.InsertBobWarehouseDetail(ctx, dbsqlc.InsertBobWarehouseDetailParams{VersionID: versionID, Name: data.Name})
	case EntityFundAccount:
		return q.InsertBobFundAccountDetail(ctx, dbsqlc.InsertBobFundAccountDetailParams{VersionID: versionID, Name: data.Name, Currency: data.Currency})
	default:
		return domainError(ErrorValidation, "invalid entity", nil, nil)
	}
}

func updateDetail(ctx context.Context, q *dbsqlc.Queries, entity, versionID string, data DetailInput) error {
	var rows int64
	var err error
	switch entity {
	case EntityCustomer:
		rows, err = q.UpdateBobCustomerDetail(ctx, dbsqlc.UpdateBobCustomerDetailParams{Name: data.Name, VersionID: versionID})
	case EntitySupplier:
		rows, err = q.UpdateBobSupplierDetail(ctx, dbsqlc.UpdateBobSupplierDetailParams{Name: data.Name, VersionID: versionID})
	case EntityEmployee:
		rows, err = q.UpdateBobEmployeeDetail(ctx, dbsqlc.UpdateBobEmployeeDetailParams{Name: data.Name, VersionID: versionID})
	case EntityProduct:
		rows, err = q.UpdateBobProductDetail(ctx, dbsqlc.UpdateBobProductDetailParams{Name: data.Name, Unit: data.Unit, VersionID: versionID})
	case EntityService:
		rows, err = q.UpdateBobServiceDetail(ctx, dbsqlc.UpdateBobServiceDetailParams{Name: data.Name, Unit: data.Unit, VersionID: versionID})
	case EntityWarehouse:
		rows, err = q.UpdateBobWarehouseDetail(ctx, dbsqlc.UpdateBobWarehouseDetailParams{Name: data.Name, VersionID: versionID})
	case EntityFundAccount:
		rows, err = q.UpdateBobFundAccountDetail(ctx, dbsqlc.UpdateBobFundAccountDetailParams{Name: data.Name, Currency: data.Currency, VersionID: versionID})
	default:
		return domainError(ErrorValidation, "invalid entity", nil, nil)
	}
	if err == nil && rows != 1 {
		return domainError(ErrorConflict, "version detail changed", nil, nil)
	}
	return err
}

func copyDetail(ctx context.Context, q *dbsqlc.Queries, entity, newVersionID, sourceVersionID string) error {
	switch entity {
	case EntityCustomer:
		return q.CopyBobCustomerDetail(ctx, dbsqlc.CopyBobCustomerDetailParams{NewVersionID: newVersionID, SourceVersionID: sourceVersionID})
	case EntitySupplier:
		return q.CopyBobSupplierDetail(ctx, dbsqlc.CopyBobSupplierDetailParams{NewVersionID: newVersionID, SourceVersionID: sourceVersionID})
	case EntityEmployee:
		return q.CopyBobEmployeeDetail(ctx, dbsqlc.CopyBobEmployeeDetailParams{NewVersionID: newVersionID, SourceVersionID: sourceVersionID})
	case EntityProduct:
		return q.CopyBobProductDetail(ctx, dbsqlc.CopyBobProductDetailParams{NewVersionID: newVersionID, SourceVersionID: sourceVersionID})
	case EntityService:
		return q.CopyBobServiceDetail(ctx, dbsqlc.CopyBobServiceDetailParams{NewVersionID: newVersionID, SourceVersionID: sourceVersionID})
	case EntityWarehouse:
		return q.CopyBobWarehouseDetail(ctx, dbsqlc.CopyBobWarehouseDetailParams{NewVersionID: newVersionID, SourceVersionID: sourceVersionID})
	case EntityFundAccount:
		return q.CopyBobFundAccountDetail(ctx, dbsqlc.CopyBobFundAccountDetailParams{NewVersionID: newVersionID, SourceVersionID: sourceVersionID})
	default:
		return domainError(ErrorValidation, "invalid entity", nil, nil)
	}
}

type auditInput struct {
	ObjectID, VersionID, Entity, Event, To, ActorID, RequestID string
	From, Comment                                              *string
	Summary                                                    map[string]any
}

func insertAudit(ctx context.Context, q *dbsqlc.Queries, input auditInput) error {
	summary := input.Summary
	if summary == nil {
		summary = map[string]any{}
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	return q.InsertBobAuditEvent(ctx, dbsqlc.InsertBobAuditEventParams{
		ID: newID(), ObjectID: input.ObjectID, VersionID: input.VersionID, Entity: input.Entity,
		EventType: input.Event, FromStatus: input.From, ToStatus: input.To, ActorID: input.ActorID,
		Comment: input.Comment, RequestID: input.RequestID, Summary: encoded,
	})
}

func newID() string { return ulid.Make().String() }
