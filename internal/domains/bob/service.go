package bob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"time"

	dbsqlc "github.com/hansonyu183/zerp-back/internal/database/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"
)

var codePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._-]*$`)
var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

type Service struct {
	pool    *pgxpool.Pool
	queries *dbsqlc.Queries
	logger  *slog.Logger
}

func NewService(pool *pgxpool.Pool, logger *slog.Logger) *Service {
	return &Service{pool: pool, queries: dbsqlc.New(pool), logger: logger}
}

func (s *Service) Query(ctx context.Context, entity string, input QueryInput) (Page[QueryItem], error) {
	offset, validPage := pageOffset(input.Page, input.PageSize)
	if !validEntity(entity) || !validPage {
		return Page[QueryItem]{}, domainError(ErrorValidation, "invalid query", nil, nil)
	}
	input.Filters.Keyword = strings.TrimSpace(input.Filters.Keyword)
	if len(input.Filters.Keyword) > 128 || len(input.Filters.Status) > 5 {
		return Page[QueryItem]{}, domainError(ErrorValidation, "invalid query filters", nil, nil)
	}
	statuses := uniqueStrings(input.Filters.Status)
	for _, status := range statuses {
		if !validStatus(status) {
			return Page[QueryItem]{}, domainError(ErrorValidation, "invalid status filter", nil, nil)
		}
	}
	sortField, sortOrder := "updatedAt", "desc"
	if len(input.Sort) > 1 {
		return Page[QueryItem]{}, domainError(ErrorValidation, "only one sort item is allowed", nil, nil)
	}
	if len(input.Sort) == 1 {
		sortField, sortOrder = input.Sort[0].Field, strings.ToLower(input.Sort[0].Order)
		if !slices.Contains([]string{"updatedAt", "code", "name", "status", "version"}, sortField) ||
			!slices.Contains([]string{"asc", "desc"}, sortOrder) {
			return Page[QueryItem]{}, domainError(ErrorValidation, "invalid sort", nil, nil)
		}
	}
	if statuses == nil {
		statuses = []string{}
	}
	countParams := dbsqlc.CountBobObjectsParams{Entity: entity, Statuses: statuses, Keyword: input.Filters.Keyword}
	total, err := s.queries.CountBobObjects(ctx, countParams)
	if err != nil {
		return Page[QueryItem]{}, s.internal("count objects", err)
	}
	rows, err := s.queries.ListBobObjects(ctx, dbsqlc.ListBobObjectsParams{
		Entity: entity, Statuses: statuses, Keyword: input.Filters.Keyword, SortField: sortField, SortOrder: sortOrder,
		PageOffset: offset, PageSize: int32(input.PageSize),
	})
	if err != nil {
		return Page[QueryItem]{}, s.internal("list objects", err)
	}
	items := make([]QueryItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, queryItem(row))
	}
	return Page[QueryItem]{Items: items, Total: total, Page: input.Page, PageSize: input.PageSize}, nil
}

func (s *Service) Get(ctx context.Context, entity string, input GetInput) (ObjectView, error) {
	if !validEntity(entity) || !validID(input.ObjectID) || (input.VersionID != "" && !validID(input.VersionID)) {
		return ObjectView{}, domainError(ErrorValidation, "invalid object or version", nil, nil)
	}
	row, err := s.queries.GetBobVersionView(ctx, dbsqlc.GetBobVersionViewParams{
		ObjectID: input.ObjectID, Entity: entity, VersionID: input.VersionID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ObjectView{}, domainError(ErrorValidation, "object or version not found", nil, nil)
	}
	if err != nil {
		return ObjectView{}, s.internal("get object", err)
	}
	return objectView(row), nil
}

func (s *Service) Create(ctx context.Context, entity string, input CreateInput, actorID, requestID string) (MutationResult, error) {
	data, code, err := validateCreate(entity, input.Data)
	if err != nil || !validActorAndRequest(actorID, requestID) {
		return MutationResult{}, domainError(ErrorValidation, "invalid create request", nil, err)
	}
	objectID, versionID := newID(), newID()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return MutationResult{}, s.internal("begin create", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := s.queries.WithTx(tx)
	if err = qtx.InsertBobObject(ctx, dbsqlc.InsertBobObjectParams{
		ID: objectID, Entity: entity, Code: code, CurrentVersionID: versionID, ActorID: actorID,
	}); err != nil {
		return MutationResult{}, s.writeError("insert object", err)
	}
	if err = qtx.InsertBobVersion(ctx, dbsqlc.InsertBobVersionParams{
		ID: versionID, ObjectID: objectID, Entity: entity, VersionNo: 1, ActorID: actorID,
	}); err != nil {
		return MutationResult{}, s.writeError("insert version", err)
	}
	if err = insertDetail(ctx, qtx, entity, versionID, data); err != nil {
		return MutationResult{}, s.writeError("insert detail", err)
	}
	if err = insertAudit(ctx, qtx, auditInput{
		ObjectID: objectID, VersionID: versionID, Entity: entity, Event: "CREATED", To: StatusDraft,
		ActorID: actorID, RequestID: requestID, Summary: map[string]any{"fields": append([]string{"code"}, detailFields(entity)...)},
	}); err != nil {
		return MutationResult{}, s.writeError("audit create", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return MutationResult{}, s.writeError("commit create", err)
	}
	return MutationResult{ObjectID: objectID, ObjectRevision: 1, VersionID: versionID, Version: 1, Status: StatusDraft, Revision: 1}, nil
}

func (s *Service) Save(ctx context.Context, entity string, input SaveInput, actorID, requestID string) (MutationResult, error) {
	data, err := validateDetail(entity, input.Data)
	if err != nil || !validWriteInput(entity, input.ObjectID, input.VersionID, input.Revision, actorID, requestID) {
		return MutationResult{}, domainError(ErrorValidation, "invalid save request", nil, err)
	}
	tx, qtx, object, version, err := s.lockTarget(ctx, entity, input.ObjectID, input.VersionID)
	if err != nil {
		return MutationResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if object.CurrentVersionID != input.VersionID || object.EffectiveVersionID != nil || version.Revision != input.Revision ||
		!slices.Contains([]string{StatusDraft, StatusRejected}, version.Status) {
		return MutationResult{}, conflict(object, version, "version changed before save")
	}
	if err = updateDetail(ctx, qtx, entity, input.VersionID, data); err != nil {
		return MutationResult{}, s.writeError("update detail", err)
	}
	rows, err := qtx.MarkBobVersionSaved(ctx, dbsqlc.MarkBobVersionSavedParams{
		ActorID: actorID, ID: input.VersionID, ObjectID: input.ObjectID, Entity: entity, Revision: input.Revision,
	})
	if err != nil {
		return MutationResult{}, s.writeError("mark version saved", err)
	}
	if rows != 1 {
		return MutationResult{}, conflict(object, version, "version changed before save")
	}
	if err = qtx.TouchBobObject(ctx, dbsqlc.TouchBobObjectParams{ActorID: actorID, ID: input.ObjectID, Entity: entity}); err != nil {
		return MutationResult{}, s.internal("touch object", err)
	}
	from := version.Status
	if err = insertAudit(ctx, qtx, auditInput{
		ObjectID: input.ObjectID, VersionID: input.VersionID, Entity: entity, Event: "SAVED", From: &from, To: from,
		ActorID: actorID, RequestID: requestID, Summary: map[string]any{"fields": detailFields(entity)},
	}); err != nil {
		return MutationResult{}, s.writeError("audit save", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return MutationResult{}, s.writeError("commit save", err)
	}
	return mutation(object, version, version.Status, input.Revision+1), nil
}

func (s *Service) Submit(ctx context.Context, entity string, input VersionRevisionInput, actorID, requestID string) (MutationResult, error) {
	if !validWriteInput(entity, input.ObjectID, input.VersionID, input.Revision, actorID, requestID) {
		return MutationResult{}, domainError(ErrorValidation, "invalid submit request", nil, nil)
	}
	tx, qtx, object, version, err := s.lockTarget(ctx, entity, input.ObjectID, input.VersionID)
	if err != nil {
		return MutationResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if object.CurrentVersionID != input.VersionID || object.EffectiveVersionID != nil || version.Revision != input.Revision ||
		!slices.Contains([]string{StatusDraft, StatusRejected}, version.Status) {
		return MutationResult{}, conflict(object, version, "version changed before submit")
	}
	if err = s.validateStoredDetail(ctx, qtx, entity, input.ObjectID, input.VersionID); err != nil {
		return MutationResult{}, err
	}
	rows, err := qtx.SubmitBobVersion(ctx, dbsqlc.SubmitBobVersionParams{
		ActorID: &actorID, ID: input.VersionID, ObjectID: input.ObjectID, Entity: entity, Revision: input.Revision,
	})
	if err != nil {
		return MutationResult{}, s.writeError("submit version", err)
	}
	if rows != 1 {
		return MutationResult{}, conflict(object, version, "version changed before submit")
	}
	if err = qtx.TouchBobObject(ctx, dbsqlc.TouchBobObjectParams{ActorID: actorID, ID: input.ObjectID, Entity: entity}); err != nil {
		return MutationResult{}, s.internal("touch object", err)
	}
	from := version.Status
	if err = insertAudit(ctx, qtx, auditInput{
		ObjectID: input.ObjectID, VersionID: input.VersionID, Entity: entity, Event: "SUBMITTED", From: &from, To: StatusPending,
		ActorID: actorID, RequestID: requestID,
	}); err != nil {
		return MutationResult{}, s.writeError("audit submit", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return MutationResult{}, s.writeError("commit submit", err)
	}
	return mutation(object, version, StatusPending, input.Revision+1), nil
}

func (s *Service) Approve(ctx context.Context, entity string, input ReviewInput, actorID, requestID string) (MutationResult, error) {
	comment, err := optionalComment(input.Comment)
	if err != nil || !validWriteInput(entity, input.ObjectID, input.VersionID, input.Revision, actorID, requestID) {
		return MutationResult{}, domainError(ErrorValidation, "invalid approval request", nil, err)
	}
	tx, qtx, object, version, err := s.lockTarget(ctx, entity, input.ObjectID, input.VersionID)
	if err != nil {
		return MutationResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if object.CurrentVersionID != input.VersionID || object.EffectiveVersionID != nil || version.Status != StatusPending || version.Revision != input.Revision {
		return MutationResult{}, conflict(object, version, "version changed before approval")
	}
	if version.SubmittedBy == nil || *version.SubmittedBy == actorID {
		return MutationResult{}, domainError(ErrorConflict, "submitter cannot review the same version", conflictData(object, version), nil)
	}
	if err = s.validateStoredDetail(ctx, qtx, entity, input.ObjectID, input.VersionID); err != nil {
		return MutationResult{}, err
	}
	rows, err := qtx.ApproveBobVersion(ctx, dbsqlc.ApproveBobVersionParams{
		ActorID: &actorID, Comment: comment, ID: input.VersionID, ObjectID: input.ObjectID, Entity: entity, Revision: input.Revision,
	})
	if err != nil {
		return MutationResult{}, s.writeError("approve version", err)
	}
	if rows != 1 {
		return MutationResult{}, conflict(object, version, "version changed before approval")
	}
	rows, err = qtx.SetBobObjectEffective(ctx, dbsqlc.SetBobObjectEffectiveParams{
		VersionID: &input.VersionID, ActorID: actorID, ID: input.ObjectID, Entity: entity, Revision: object.Revision,
	})
	if err != nil {
		return MutationResult{}, s.writeError("set effective version", err)
	}
	if rows != 1 {
		return MutationResult{}, conflict(object, version, "object changed before approval")
	}
	from := StatusPending
	if err = insertAudit(ctx, qtx, auditInput{
		ObjectID: input.ObjectID, VersionID: input.VersionID, Entity: entity, Event: "APPROVED", From: &from, To: StatusEffective,
		ActorID: actorID, RequestID: requestID, Comment: comment,
	}); err != nil {
		return MutationResult{}, s.writeError("audit approval", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return MutationResult{}, s.writeError("commit approval", err)
	}
	result := mutation(object, version, StatusEffective, input.Revision+1)
	result.ObjectRevision++
	return result, nil
}

func (s *Service) Reject(ctx context.Context, entity string, input ReviewInput, actorID, requestID string) (MutationResult, error) {
	comment, err := requiredComment(input.Comment)
	if err != nil || !validWriteInput(entity, input.ObjectID, input.VersionID, input.Revision, actorID, requestID) {
		return MutationResult{}, domainError(ErrorValidation, "invalid rejection request", nil, err)
	}
	tx, qtx, object, version, err := s.lockTarget(ctx, entity, input.ObjectID, input.VersionID)
	if err != nil {
		return MutationResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if object.CurrentVersionID != input.VersionID || object.EffectiveVersionID != nil || version.Status != StatusPending || version.Revision != input.Revision {
		return MutationResult{}, conflict(object, version, "version changed before rejection")
	}
	if version.SubmittedBy == nil || *version.SubmittedBy == actorID {
		return MutationResult{}, domainError(ErrorConflict, "submitter cannot review the same version", conflictData(object, version), nil)
	}
	rows, err := qtx.RejectBobVersion(ctx, dbsqlc.RejectBobVersionParams{
		ActorID: &actorID, Comment: comment, ID: input.VersionID, ObjectID: input.ObjectID, Entity: entity, Revision: input.Revision,
	})
	if err != nil {
		return MutationResult{}, s.writeError("reject version", err)
	}
	if rows != 1 {
		return MutationResult{}, conflict(object, version, "version changed before rejection")
	}
	if err = qtx.TouchBobObject(ctx, dbsqlc.TouchBobObjectParams{ActorID: actorID, ID: input.ObjectID, Entity: entity}); err != nil {
		return MutationResult{}, s.internal("touch object", err)
	}
	from := StatusPending
	if err = insertAudit(ctx, qtx, auditInput{
		ObjectID: input.ObjectID, VersionID: input.VersionID, Entity: entity, Event: "REJECTED", From: &from, To: StatusRejected,
		ActorID: actorID, RequestID: requestID, Comment: comment,
	}); err != nil {
		return MutationResult{}, s.writeError("audit rejection", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return MutationResult{}, s.writeError("commit rejection", err)
	}
	return mutation(object, version, StatusRejected, input.Revision+1), nil
}

func (s *Service) Edit(ctx context.Context, entity string, input ObjectRevisionInput, actorID, requestID string) (MutationResult, error) {
	if !validEntity(entity) || !validID(input.ObjectID) || input.ObjectRevision < 1 || !validActorAndRequest(actorID, requestID) {
		return MutationResult{}, domainError(ErrorValidation, "invalid edit request", nil, nil)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return MutationResult{}, s.internal("begin edit", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := s.queries.WithTx(tx)
	object, err := qtx.LockBobObject(ctx, dbsqlc.LockBobObjectParams{ID: input.ObjectID, Entity: entity})
	if errors.Is(err, pgx.ErrNoRows) {
		return MutationResult{}, domainError(ErrorValidation, "object not found", nil, nil)
	}
	if err != nil {
		return MutationResult{}, s.internal("lock object", err)
	}
	if object.Revision != input.ObjectRevision || object.EffectiveVersionID == nil || object.CurrentVersionID != *object.EffectiveVersionID {
		return MutationResult{}, domainError(ErrorConflict, "object cannot be edited in its current state", map[string]any{
			"objectRevision": object.Revision, "currentVersionId": object.CurrentVersionID,
		}, nil)
	}
	oldVersion, err := qtx.LockBobVersion(ctx, dbsqlc.LockBobVersionParams{
		ID: *object.EffectiveVersionID, ObjectID: input.ObjectID, Entity: entity,
	})
	if err != nil {
		return MutationResult{}, s.internal("lock effective version", err)
	}
	if oldVersion.Status != StatusEffective {
		return MutationResult{}, conflict(object, oldVersion, "effective version changed before edit")
	}
	newVersionID := newID()
	if err = qtx.InsertBobVersion(ctx, dbsqlc.InsertBobVersionParams{
		ID: newVersionID, ObjectID: input.ObjectID, Entity: entity, VersionNo: object.NextVersionNo, ActorID: actorID,
	}); err != nil {
		return MutationResult{}, s.writeError("insert edit version", err)
	}
	if err = copyDetail(ctx, qtx, entity, newVersionID, oldVersion.ID); err != nil {
		return MutationResult{}, s.writeError("copy edit detail", err)
	}
	rows, err := qtx.InvalidateBobVersion(ctx, dbsqlc.InvalidateBobVersionParams{
		ActorID: actorID, ID: oldVersion.ID, ObjectID: input.ObjectID, Entity: entity, Revision: oldVersion.Revision,
	})
	if err != nil {
		return MutationResult{}, s.writeError("invalidate effective version", err)
	}
	if rows != 1 {
		return MutationResult{}, conflict(object, oldVersion, "effective version changed before edit")
	}
	rows, err = qtx.AdvanceBobObjectForEdit(ctx, dbsqlc.AdvanceBobObjectForEditParams{
		NewVersionID: newVersionID, ActorID: actorID, ID: input.ObjectID, Entity: entity,
		Revision: input.ObjectRevision, OldVersionID: oldVersion.ID,
	})
	if err != nil {
		return MutationResult{}, s.writeError("advance object for edit", err)
	}
	if rows != 1 {
		return MutationResult{}, conflict(object, oldVersion, "object changed before edit")
	}
	fromEffective, fromNone := StatusEffective, (*string)(nil)
	if err = insertAudit(ctx, qtx, auditInput{
		ObjectID: input.ObjectID, VersionID: oldVersion.ID, Entity: entity, Event: "INVALIDATED", From: &fromEffective, To: StatusInvalid,
		ActorID: actorID, RequestID: requestID,
	}); err != nil {
		return MutationResult{}, s.writeError("audit invalidation", err)
	}
	if err = insertAudit(ctx, qtx, auditInput{
		ObjectID: input.ObjectID, VersionID: newVersionID, Entity: entity, Event: "EDIT_STARTED", From: fromNone, To: StatusDraft,
		ActorID: actorID, RequestID: requestID,
	}); err != nil {
		return MutationResult{}, s.writeError("audit edit", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return MutationResult{}, s.writeError("commit edit", err)
	}
	return MutationResult{
		ObjectID: input.ObjectID, ObjectRevision: input.ObjectRevision + 1, VersionID: newVersionID,
		Version: object.NextVersionNo, Status: StatusDraft, Revision: 1,
	}, nil
}

func (s *Service) Versions(ctx context.Context, entity string, input HistoryInput) (Page[VersionHistoryItem], error) {
	if !validHistoryInput(entity, input) {
		return Page[VersionHistoryItem]{}, domainError(ErrorValidation, "invalid versions request", nil, nil)
	}
	if _, err := s.Get(ctx, entity, GetInput{ObjectID: input.ObjectID}); err != nil {
		return Page[VersionHistoryItem]{}, err
	}
	total, err := s.queries.CountBobVersions(ctx, dbsqlc.CountBobVersionsParams{ObjectID: input.ObjectID, Entity: entity})
	if err != nil {
		return Page[VersionHistoryItem]{}, s.internal("count versions", err)
	}
	rows, err := s.queries.ListBobVersions(ctx, dbsqlc.ListBobVersionsParams{
		ObjectID: input.ObjectID, Entity: entity, PageOffset: mustPageOffset(input.Page, input.PageSize), PageSize: int32(input.PageSize),
	})
	if err != nil {
		return Page[VersionHistoryItem]{}, s.internal("list versions", err)
	}
	items := make([]VersionHistoryItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, versionHistoryItem(row))
	}
	return Page[VersionHistoryItem]{Items: items, Total: total, Page: input.Page, PageSize: input.PageSize}, nil
}

func (s *Service) AuditHistory(ctx context.Context, entity string, input HistoryInput) (Page[AuditEventView], error) {
	if !validHistoryInput(entity, input) {
		return Page[AuditEventView]{}, domainError(ErrorValidation, "invalid audit history request", nil, nil)
	}
	if _, err := s.Get(ctx, entity, GetInput{ObjectID: input.ObjectID}); err != nil {
		return Page[AuditEventView]{}, err
	}
	total, err := s.queries.CountBobAuditEvents(ctx, dbsqlc.CountBobAuditEventsParams{ObjectID: input.ObjectID, Entity: entity})
	if err != nil {
		return Page[AuditEventView]{}, s.internal("count audit events", err)
	}
	rows, err := s.queries.ListBobAuditEvents(ctx, dbsqlc.ListBobAuditEventsParams{
		ObjectID: input.ObjectID, Entity: entity, PageOffset: mustPageOffset(input.Page, input.PageSize), PageSize: int32(input.PageSize),
	})
	if err != nil {
		return Page[AuditEventView]{}, s.internal("list audit events", err)
	}
	items := make([]AuditEventView, 0, len(rows))
	for _, row := range rows {
		items = append(items, auditEventView(row))
	}
	return Page[AuditEventView]{Items: items, Total: total, Page: input.Page, PageSize: input.PageSize}, nil
}

// ResolveEffectiveReference must be called with the transaction that will write
// the consuming business record. The shared row lock is held until that
// transaction finishes, preventing a concurrent edit from invalidating the
// reference between validation and the consuming write.
func (s *Service) ResolveEffectiveReference(ctx context.Context, tx pgx.Tx, entity, objectID, versionID string) (EffectiveReference, error) {
	if !validEntity(entity) || !validID(objectID) || !validID(versionID) {
		return EffectiveReference{}, domainError(ErrorValidation, "invalid effective reference", nil, nil)
	}
	row, err := s.queries.WithTx(tx).ResolveBobEffectiveReference(ctx, dbsqlc.ResolveBobEffectiveReferenceParams{
		ObjectID: objectID, Entity: entity, VersionID: versionID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return EffectiveReference{}, domainError(ErrorConflict, "version is not currently effective", nil, nil)
	}
	if err != nil {
		return EffectiveReference{}, s.internal("resolve effective reference", err)
	}
	return EffectiveReference{
		ObjectID: row.ObjectID, Entity: row.Entity, Code: row.Code, VersionID: row.VersionID,
		Data: DetailView{Name: row.Name, Unit: row.Unit, Currency: deref(row.Currency)},
	}, nil
}

func (s *Service) lockTarget(ctx context.Context, entity, objectID, versionID string) (
	pgx.Tx, *dbsqlc.Queries, dbsqlc.LockBobObjectRow, dbsqlc.LockBobVersionRow, error,
) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, dbsqlc.LockBobObjectRow{}, dbsqlc.LockBobVersionRow{}, s.internal("begin transaction", err)
	}
	qtx := s.queries.WithTx(tx)
	object, err := qtx.LockBobObject(ctx, dbsqlc.LockBobObjectParams{ID: objectID, Entity: entity})
	if errors.Is(err, pgx.ErrNoRows) {
		tx.Rollback(ctx) //nolint:errcheck
		return nil, nil, object, dbsqlc.LockBobVersionRow{}, domainError(ErrorValidation, "object not found", nil, nil)
	}
	if err != nil {
		tx.Rollback(ctx) //nolint:errcheck
		return nil, nil, object, dbsqlc.LockBobVersionRow{}, s.internal("lock object", err)
	}
	version, err := qtx.LockBobVersion(ctx, dbsqlc.LockBobVersionParams{ID: versionID, ObjectID: objectID, Entity: entity})
	if errors.Is(err, pgx.ErrNoRows) {
		tx.Rollback(ctx) //nolint:errcheck
		return nil, nil, object, version, domainError(ErrorValidation, "version not found", nil, nil)
	}
	if err != nil {
		tx.Rollback(ctx) //nolint:errcheck
		return nil, nil, object, version, s.internal("lock version", err)
	}
	return tx, qtx, object, version, nil
}

func (s *Service) validateStoredDetail(ctx context.Context, q *dbsqlc.Queries, entity, objectID, versionID string) error {
	row, err := q.GetBobVersionView(ctx, dbsqlc.GetBobVersionViewParams{ObjectID: objectID, Entity: entity, VersionID: versionID})
	if err != nil {
		return s.internal("read stored detail", err)
	}
	_, err = validateDetail(entity, DetailInput{Name: row.Name, Unit: row.Unit, Currency: deref(row.Currency)})
	return err
}

func validateCreate(entity string, input CreateDetailInput) (DetailInput, string, error) {
	code := strings.ToUpper(strings.TrimSpace(input.Code))
	if len(code) < 1 || len(code) > 64 || !codePattern.MatchString(code) {
		return DetailInput{}, "", domainError(ErrorValidation, "invalid code", nil, nil)
	}
	data, err := validateDetail(entity, DetailInput{Name: input.Name, Unit: input.Unit, Currency: input.Currency})
	return data, code, err
}

func validateDetail(entity string, input DetailInput) (DetailInput, error) {
	if !validEntity(entity) {
		return DetailInput{}, domainError(ErrorValidation, "invalid entity", nil, nil)
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Unit = strings.TrimSpace(input.Unit)
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	if len(input.Name) < 1 || len(input.Name) > 200 {
		return DetailInput{}, domainError(ErrorValidation, "invalid name", nil, nil)
	}
	switch entity {
	case EntityProduct, EntityService:
		if len(input.Unit) < 1 || len(input.Unit) > 32 || input.Currency != "" {
			return DetailInput{}, domainError(ErrorValidation, "invalid unit or unexpected currency", nil, nil)
		}
	case EntityFundAccount:
		if input.Unit != "" || !currencyPattern.MatchString(input.Currency) {
			return DetailInput{}, domainError(ErrorValidation, "invalid currency or unexpected unit", nil, nil)
		}
	default:
		if input.Unit != "" || input.Currency != "" {
			return DetailInput{}, domainError(ErrorValidation, "unexpected entity fields", nil, nil)
		}
	}
	return input, nil
}

func validWriteInput(entity, objectID, versionID string, revision int64, actorID, requestID string) bool {
	return validEntity(entity) && validID(objectID) && validID(versionID) && revision >= 1 && validActorAndRequest(actorID, requestID)
}

func validActorAndRequest(actorID, requestID string) bool {
	return validID(actorID) && requestID != "" && len(requestID) <= 128
}

func validHistoryInput(entity string, input HistoryInput) bool {
	_, validPage := pageOffset(input.Page, input.PageSize)
	return validEntity(entity) && validID(input.ObjectID) && validPage
}

func pageOffset(page, pageSize int) (int32, bool) {
	if page < 1 || pageSize < 1 || pageSize > 100 {
		return 0, false
	}
	pageIndex := int64(page - 1)
	if pageIndex > int64(1<<31-1)/int64(pageSize) {
		return 0, false
	}
	offset := pageIndex * int64(pageSize)
	return int32(offset), true
}

func mustPageOffset(page, pageSize int) int32 {
	offset, _ := pageOffset(page, pageSize)
	return offset
}

func validEntity(entity string) bool { return slices.Contains(Entities, entity) }

func validStatus(status string) bool {
	return slices.Contains([]string{StatusDraft, StatusPending, StatusRejected, StatusEffective, StatusInvalid}, status)
}

func validID(id string) bool {
	parsed, err := ulid.ParseStrict(id)
	return err == nil && parsed.String() == id
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func optionalComment(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*value)
	if len(trimmed) > 1000 {
		return nil, domainError(ErrorValidation, "comment is too long", nil, nil)
	}
	if trimmed == "" {
		return nil, nil
	}
	return &trimmed, nil
}

func requiredComment(value *string) (*string, error) {
	comment, err := optionalComment(value)
	if err != nil {
		return nil, err
	}
	if comment == nil {
		return nil, domainError(ErrorValidation, "rejection comment is required", nil, nil)
	}
	return comment, nil
}

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
	return DetailView{Name: row.Name, Unit: row.Unit, Currency: deref(row.Currency)}
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

func (s *Service) internal(operation string, err error) error {
	return domainError(ErrorInternal, "internal server error", nil, fmt.Errorf("%s: %w", operation, err))
}

func (s *Service) writeError(operation string, err error) error {
	var domainErr *DomainError
	if errors.As(err, &domainErr) {
		return err
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505", "23P01", "40001", "40P01":
			return domainError(ErrorConflict, "data conflict", nil, err)
		}
	}
	return s.internal(operation, err)
}
