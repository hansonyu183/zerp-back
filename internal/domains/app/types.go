package app

import (
	"errors"
	"time"
)

const (
	StatusEnabled  = "ENABLED"
	StatusDisabled = "DISABLED"
	signoutPath    = "/app/user/signout"
)

type ErrorKind int

const (
	ErrorValidation ErrorKind = iota + 1
	ErrorUnauthenticated
	ErrorForbidden
	ErrorConflict
	ErrorNotFound
	ErrorInternal
)

type DomainError struct {
	Kind    ErrorKind
	Message string
	Cause   error
}

func (e *DomainError) Error() string { return e.Message }
func (e *DomainError) Unwrap() error { return e.Cause }

func domainError(kind ErrorKind, message string, cause error) error {
	return &DomainError{Kind: kind, Message: message, Cause: cause}
}

func errorIsKind(err error, kind ErrorKind) bool {
	var target *DomainError
	return errors.As(err, &target) && target.Kind == kind
}

type UserSummary struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
}

type ProfileView struct {
	ID                string    `json:"id"`
	Username          string    `json:"username"`
	DisplayName       string    `json:"displayName"`
	PasswordChangedAt time.Time `json:"passwordChangedAt"`
	Revision          int64     `json:"revision"`
}

type ChangePasswordInput struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

type SessionData struct {
	User        UserSummary `json:"user"`
	CSRFToken   string      `json:"csrfToken"`
	Permissions []string    `json:"permissions"`
}

type SessionResult struct {
	Data         SessionData
	SessionToken string
	ExpiresAt    time.Time
}

type Principal struct {
	SessionID    string
	User         UserSummary
	CSRFHash     []byte
	Permissions  []string
	IdleExpires  time.Time
	AbsoluteEnds time.Time
}

type PageRequest struct {
	Page     int               `json:"page"`
	PageSize int               `json:"pageSize"`
	Filters  map[string]string `json:"filters"`
	Sort     []SortItem        `json:"sort"`
}

type SortItem struct {
	Field string `json:"field"`
	Order string `json:"order"`
}

type Page[T any] struct {
	Items    []T   `json:"items"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"pageSize"`
}

type UserView struct {
	ID                string     `json:"id"`
	Username          string     `json:"username"`
	DisplayName       string     `json:"displayName"`
	Status            string     `json:"status"`
	FailedSigninCount int32      `json:"failedSigninCount"`
	LockedUntil       *time.Time `json:"lockedUntil"`
	PasswordChangedAt time.Time  `json:"passwordChangedAt"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
	Revision          int64      `json:"revision"`
	RoleIDs           []string   `json:"roleIds,omitempty"`
}

type RoleView struct {
	ID            string    `json:"id"`
	Code          string    `json:"code"`
	Name          string    `json:"name"`
	Description   *string   `json:"description"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
	Revision      int64     `json:"revision"`
	PermissionIDs []string  `json:"permissionIds,omitempty"`
}

type PermissionView struct {
	ID          string  `json:"id"`
	Path        string  `json:"path"`
	Domain      string  `json:"domain"`
	Entity      string  `json:"entity"`
	Action      string  `json:"action"`
	Description *string `json:"description"`
	Status      string  `json:"status"`
	Revision    int64   `json:"revision"`
	RoleCount   *int64  `json:"roleCount,omitempty"`
}
