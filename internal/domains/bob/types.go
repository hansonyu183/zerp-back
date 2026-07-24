package bob

import (
	"encoding/json"
	"time"
)

const (
	EntityCustomer    = "customer"
	EntitySupplier    = "supplier"
	EntityEmployee    = "employee"
	EntityProduct     = "product"
	EntityService     = "service"
	EntityWarehouse   = "warehouse"
	EntityFundAccount = "fund-account"

	StatusDraft     = "DRAFT"
	StatusPending   = "PENDING"
	StatusRejected  = "REJECTED"
	StatusEffective = "EFFECTIVE"
	StatusInvalid   = "INVALID"
)

var entities = [...]string{
	EntityCustomer,
	EntitySupplier,
	EntityEmployee,
	EntityProduct,
	EntityService,
	EntityWarehouse,
	EntityFundAccount,
}

type ErrorKind int

const (
	ErrorValidation ErrorKind = iota + 1
	ErrorConflict
	ErrorInternal
)

type DomainError struct {
	Kind    ErrorKind
	Message string
	Data    any
	Cause   error
}

func (e *DomainError) Error() string { return e.Message }
func (e *DomainError) Unwrap() error { return e.Cause }

func domainError(kind ErrorKind, message string, data any, cause error) error {
	return &DomainError{Kind: kind, Message: message, Data: data, Cause: cause}
}

type DetailInput struct {
	Name     string `json:"name"`
	Unit     string `json:"unit,omitempty"`
	Currency string `json:"currency,omitempty"`
}

type CreateDetailInput struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Unit     string `json:"unit,omitempty"`
	Currency string `json:"currency,omitempty"`
}

type CreateInput struct {
	Data CreateDetailInput `json:"data"`
}

type SaveInput struct {
	ObjectID  string      `json:"objectId"`
	VersionID string      `json:"versionId"`
	Revision  int64       `json:"revision"`
	Data      DetailInput `json:"data"`
}

type ObjectRevisionInput struct {
	ObjectID       string `json:"objectId"`
	ObjectRevision int64  `json:"objectRevision"`
}

type VersionRevisionInput struct {
	ObjectID  string `json:"objectId"`
	VersionID string `json:"versionId"`
	Revision  int64  `json:"revision"`
}

type ReviewInput struct {
	ObjectID  string  `json:"objectId"`
	VersionID string  `json:"versionId"`
	Revision  int64   `json:"revision"`
	Comment   *string `json:"comment"`
}

type GetInput struct {
	ObjectID  string `json:"objectId"`
	VersionID string `json:"versionId,omitempty"`
}

type QueryFilters struct {
	Keyword string   `json:"keyword,omitempty"`
	Status  []string `json:"status,omitempty"`
}

type SortItem struct {
	Field string `json:"field"`
	Order string `json:"order"`
}

type QueryInput struct {
	Page     int          `json:"page"`
	PageSize int          `json:"pageSize"`
	Filters  QueryFilters `json:"filters"`
	Sort     []SortItem   `json:"sort"`
}

type HistoryInput struct {
	ObjectID string `json:"objectId"`
	Page     int    `json:"page"`
	PageSize int    `json:"pageSize"`
}

type DetailView struct {
	Name     string `json:"name"`
	Unit     string `json:"unit,omitempty"`
	Currency string `json:"currency,omitempty"`
}

type MutationResult struct {
	ObjectID       string `json:"objectId"`
	ObjectRevision int64  `json:"objectRevision"`
	VersionID      string `json:"versionId"`
	Version        int32  `json:"version"`
	Status         string `json:"status"`
	Revision       int64  `json:"revision"`
}

type VersionMeta struct {
	VersionID     string     `json:"versionId"`
	Version       int32      `json:"version"`
	Status        string     `json:"status"`
	Revision      int64      `json:"revision"`
	CreatedAt     time.Time  `json:"createdAt"`
	CreatedBy     string     `json:"createdBy"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	UpdatedBy     string     `json:"updatedBy"`
	SubmittedAt   *time.Time `json:"submittedAt"`
	SubmittedBy   *string    `json:"submittedBy"`
	ReviewedAt    *time.Time `json:"reviewedAt"`
	ReviewedBy    *string    `json:"reviewedBy"`
	ReviewComment *string    `json:"reviewComment"`
}

type ObjectView struct {
	ObjectID           string      `json:"objectId"`
	Entity             string      `json:"entity"`
	Code               string      `json:"code"`
	ObjectRevision     int64       `json:"objectRevision"`
	CurrentVersionID   string      `json:"currentVersionId"`
	EffectiveVersionID *string     `json:"effectiveVersionId"`
	UpdatedAt          time.Time   `json:"updatedAt"`
	Version            VersionMeta `json:"version"`
	Data               DetailView  `json:"data"`
}

type VersionSummary struct {
	VersionID string     `json:"versionId"`
	Version   int32      `json:"version"`
	Status    string     `json:"status"`
	Revision  int64      `json:"revision"`
	Summary   DetailView `json:"summary"`
}

type VersionHistoryItem struct {
	VersionID     string     `json:"versionId"`
	Version       int32      `json:"version"`
	Status        string     `json:"status"`
	Revision      int64      `json:"revision"`
	CreatedAt     time.Time  `json:"createdAt"`
	CreatedBy     string     `json:"createdBy"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	UpdatedBy     string     `json:"updatedBy"`
	SubmittedAt   *time.Time `json:"submittedAt"`
	SubmittedBy   *string    `json:"submittedBy"`
	ReviewedAt    *time.Time `json:"reviewedAt"`
	ReviewedBy    *string    `json:"reviewedBy"`
	ReviewComment *string    `json:"reviewComment"`
	Summary       DetailView `json:"summary"`
}

type QueryItem struct {
	ObjectID           string         `json:"objectId"`
	Entity             string         `json:"entity"`
	Code               string         `json:"code"`
	ObjectRevision     int64          `json:"objectRevision"`
	CurrentVersion     VersionSummary `json:"currentVersion"`
	EffectiveVersionID *string        `json:"effectiveVersionId"`
	UpdatedAt          time.Time      `json:"updatedAt"`
}

type Page[T any] struct {
	Items    []T   `json:"items"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"pageSize"`
}

type AuditEventView struct {
	ID         string          `json:"id"`
	ObjectID   string          `json:"objectId"`
	VersionID  string          `json:"versionId"`
	Entity     string          `json:"entity"`
	EventType  string          `json:"eventType"`
	FromStatus *string         `json:"fromStatus"`
	ToStatus   string          `json:"toStatus"`
	ActorID    string          `json:"actorId"`
	OccurredAt time.Time       `json:"occurredAt"`
	Comment    *string         `json:"comment"`
	RequestID  string          `json:"requestId"`
	Summary    json.RawMessage `json:"summary"`
}

type EffectiveReference struct {
	ObjectID  string     `json:"objectId"`
	Entity    string     `json:"entity"`
	Code      string     `json:"code"`
	VersionID string     `json:"versionId"`
	Data      DetailView `json:"data"`
}
