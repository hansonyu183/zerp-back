package bob

import (
	"encoding/json"
	"time"

	dbsqlc "github.com/hansonyu183/zerp-back/internal/database/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

func mutation(object dbsqlc.LockBobObjectRow, version dbsqlc.LockBobVersionRow, status string, revision int64) MutationResult {
	return MutationResult{
		ObjectID: object.ID, ObjectRevision: object.Revision, VersionID: version.ID,
		Version: version.VersionNo, Status: status, Revision: revision,
	}
}

func conflict(object dbsqlc.LockBobObjectRow, version dbsqlc.LockBobVersionRow, message string) error {
	return domainError(ErrorConflict, message, conflictData(object, version), nil)
}

func conflictData(object dbsqlc.LockBobObjectRow, version dbsqlc.LockBobVersionRow) map[string]any {
	return map[string]any{
		"objectRevision": object.Revision,
		"versionId":      version.ID,
		"revision":       version.Revision,
		"status":         version.Status,
	}
}

func detailFields(entity string) []string {
	fields := []string{"name"}
	if entity == EntityProduct || entity == EntityService {
		fields = append(fields, "unit")
	}
	if entity == EntityFundAccount {
		fields = append(fields, "currency")
	}
	if entity == EntitySupplier {
		fields = append(fields, "supplierType")
	}
	if entity == EntityVehicle {
		fields = append(fields, "plateNumber", "vehicleType", "platformObjectId")
	}
	return fields
}

func queryItem(row dbsqlc.BobVersionView) QueryItem {
	return QueryItem{
		ObjectID: row.ObjectID, Entity: row.Entity, Code: row.Code, ObjectRevision: row.ObjectRevision,
		CurrentVersion: versionSummary(row), EffectiveVersionID: row.EffectiveVersionID, UpdatedAt: row.ObjectUpdatedAt.Time,
	}
}

func versionSummary(row dbsqlc.BobVersionView) VersionSummary {
	return VersionSummary{
		VersionID: row.VersionID, Version: row.VersionNo, Status: row.Status, Revision: row.VersionRevision,
		Summary: detailView(row),
	}
}

func versionHistoryItem(row dbsqlc.BobVersionView) VersionHistoryItem {
	return VersionHistoryItem{
		VersionID: row.VersionID, Version: row.VersionNo, Status: row.Status, Revision: row.VersionRevision,
		CreatedAt: row.CreatedAt.Time, CreatedBy: row.CreatedBy, UpdatedAt: row.UpdatedAt.Time, UpdatedBy: row.UpdatedBy,
		SubmittedAt: timePointer(row.SubmittedAt), SubmittedBy: row.SubmittedBy,
		ReviewedAt: timePointer(row.ReviewedAt), ReviewedBy: row.ReviewedBy, ReviewComment: row.ReviewComment,
		Summary: detailView(row),
	}
}

func objectView(row dbsqlc.BobVersionView) ObjectView {
	return ObjectView{
		ObjectID: row.ObjectID, Entity: row.Entity, Code: row.Code, ObjectRevision: row.ObjectRevision,
		CurrentVersionID: row.CurrentVersionID, EffectiveVersionID: row.EffectiveVersionID, UpdatedAt: row.ObjectUpdatedAt.Time,
		Version: VersionMeta{
			VersionID: row.VersionID, Version: row.VersionNo, Status: row.Status, Revision: row.VersionRevision,
			CreatedAt: row.CreatedAt.Time, CreatedBy: row.CreatedBy, UpdatedAt: row.UpdatedAt.Time, UpdatedBy: row.UpdatedBy,
			SubmittedAt: timePointer(row.SubmittedAt), SubmittedBy: row.SubmittedBy,
			ReviewedAt: timePointer(row.ReviewedAt), ReviewedBy: row.ReviewedBy, ReviewComment: row.ReviewComment,
		},
		Data: detailView(row),
	}
}

func detailView(row dbsqlc.BobVersionView) DetailView {
	return DetailView{
		Name: row.Name, Unit: row.Unit, Currency: deref(row.Currency),
		SupplierType: deref(row.SupplierType), PlateNumber: deref(row.PlateNumber),
		VehicleType: deref(row.VehicleType), PlatformObjectID: deref(row.PlatformObjectID),
	}
}

func auditEventView(row dbsqlc.BobAuditEvent) AuditEventView {
	return AuditEventView{
		ID: row.ID, ObjectID: row.ObjectID, VersionID: row.VersionID, Entity: row.Entity,
		EventType: row.EventType, FromStatus: row.FromStatus, ToStatus: row.ToStatus, ActorID: row.ActorID,
		OccurredAt: row.OccurredAt.Time, Comment: row.Comment, RequestID: row.RequestID, Summary: json.RawMessage(row.Summary),
	}
}

func timePointer(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
