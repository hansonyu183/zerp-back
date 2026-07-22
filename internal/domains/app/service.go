package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/hansonyu183/zerp-back/internal/config"
	dbsqlc "github.com/hansonyu183/zerp-back/internal/database/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"
)

type Service struct {
	pool          *pgxpool.Pool
	queries       *dbsqlc.Queries
	cfg           config.Config
	logger        *slog.Logger
	dummyPassword string
}

func NewService(pool *pgxpool.Pool, cfg config.Config, logger *slog.Logger) *Service {
	dummy, err := hashPassword("Dummy-login-password-1!")
	if err != nil {
		panic(fmt.Sprintf("initialize password verifier: %v", err))
	}
	return &Service{pool: pool, queries: dbsqlc.New(pool), cfg: cfg, logger: logger, dummyPassword: dummy}
}

// BootstrapAdmin creates the first user and a superadmin role. It refuses to run
// once any user exists so it cannot become a general-purpose privilege bypass.
func (s *Service) BootstrapAdmin(ctx context.Context, username, displayName, password string) (UserView, error) {
	username = normalizeUsername(username)
	displayName = strings.TrimSpace(displayName)
	if len(username) < 3 || len(username) > 64 || len(displayName) == 0 || len(displayName) > 128 {
		return UserView{}, domainError(ErrorValidation, "invalid bootstrap user fields", nil)
	}
	if err := validatePassword(password, s.cfg.PasswordMinLength); err != nil {
		return UserView{}, domainError(ErrorValidation, err.Error(), nil)
	}
	passwordHash, err := hashPassword(password)
	if err != nil {
		return UserView{}, s.internal("hash bootstrap password", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return UserView{}, s.internal("begin bootstrap", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := s.queries.WithTx(tx)
	count, err := qtx.CountAllAppUsers(ctx)
	if err != nil {
		return UserView{}, s.internal("count bootstrap users", err)
	}
	if count != 0 {
		return UserView{}, domainError(ErrorConflict, "bootstrap is disabled after the first user exists", nil)
	}
	permissionIDs, err := qtx.ListAllEnabledAppPermissionIDs(ctx)
	if err != nil {
		return UserView{}, s.internal("list bootstrap permissions", err)
	}
	if len(permissionIDs) == 0 {
		return UserView{}, domainError(ErrorConflict, "permission catalog is empty", nil)
	}
	roleID, userID := newID(), newID()
	if err = qtx.InsertAppRole(ctx, dbsqlc.InsertAppRoleParams{ID: roleID, Code: "superadmin", Name: "Super Administrator", Description: stringPointer("Initial system administrator"), ActorID: &userID}); err != nil {
		return UserView{}, s.writeError("create bootstrap role", err)
	}
	if err = replaceRolePermissions(ctx, qtx, roleID, permissionIDs, userID); err != nil {
		return UserView{}, err
	}
	if err = qtx.InsertAppUser(ctx, dbsqlc.InsertAppUserParams{ID: userID, Username: username, DisplayName: displayName, PasswordHash: passwordHash, ActorID: &userID}); err != nil {
		return UserView{}, s.writeError("create bootstrap user", err)
	}
	if err = replaceUserRoles(ctx, qtx, userID, []string{roleID}, userID); err != nil {
		return UserView{}, err
	}
	if err = ensureSafeSignout(ctx, qtx, userID); err != nil {
		return UserView{}, err
	}
	if err = s.audit(ctx, qtx, "SYSTEM_BOOTSTRAP", &userID, "user", &userID, "SUCCESS", "bootstrap-admin", map[string]any{"roleId": roleID}); err != nil {
		return UserView{}, s.internal("audit bootstrap", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return UserView{}, s.internal("commit bootstrap", err)
	}
	return s.GetUser(ctx, userID)
}

func newID() string { return ulid.Make().String() }

func (s *Service) Signin(ctx context.Context, username, password, requestID string) (SessionResult, error) {
	username = normalizeUsername(username)
	if len(username) < 3 || len(username) > 64 || password == "" || len(password) > 1024 {
		_ = verifyPassword(s.dummyPassword, password)
		return SessionResult{}, domainError(ErrorUnauthenticated, "authentication failed", nil)
	}

	user, err := s.queries.GetAppUserByUsername(ctx, username)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = verifyPassword(s.dummyPassword, password)
		_ = s.audit(ctx, s.queries, "USER_SIGNIN", nil, "user", nil, "FAILURE", requestID, map[string]any{"reason": "unknown_or_invalid"})
		return SessionResult{}, domainError(ErrorUnauthenticated, "authentication failed", nil)
	}
	if err != nil {
		return SessionResult{}, s.internal("read signin user", err)
	}

	passwordOK := verifyPassword(user.PasswordHash, password)
	now := time.Now().UTC()
	locked := user.LockedUntil.Valid && user.LockedUntil.Time.After(now)
	if !passwordOK || user.Status != StatusEnabled || locked {
		tx, beginErr := s.pool.Begin(ctx)
		if beginErr == nil {
			qtx := s.queries.WithTx(tx)
			if !passwordOK && user.Status == StatusEnabled && !locked {
				_, _ = qtx.RecordSigninFailure(ctx, dbsqlc.RecordSigninFailureParams{
					ID: user.ID, LockThreshold: int32(s.cfg.SigninLockThreshold),
					LockDuration: pgtype.Interval{Microseconds: s.cfg.SigninLockDuration.Microseconds(), Valid: true},
				})
			}
			if auditErr := s.audit(ctx, qtx, "USER_SIGNIN", &user.ID, "user", &user.ID, "FAILURE", requestID, map[string]any{"reason": "unknown_or_invalid"}); auditErr == nil {
				_ = tx.Commit(ctx)
			}
		}
		return SessionResult{}, domainError(ErrorUnauthenticated, "authentication failed", nil)
	}

	sessionToken, err := newRawToken()
	if err != nil {
		return SessionResult{}, s.internal("generate session token", err)
	}
	csrfToken, err := newRawToken()
	if err != nil {
		return SessionResult{}, s.internal("generate csrf token", err)
	}
	idleEnds := now.Add(s.cfg.SessionIdleTimeout)
	absoluteEnds := now.Add(s.cfg.SessionAbsoluteTimeout)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SessionResult{}, s.internal("begin signin transaction", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := s.queries.WithTx(tx)
	permissions, err := qtx.GetAppUserPermissions(ctx, user.ID)
	if err != nil {
		return SessionResult{}, s.internal("load signin permissions", err)
	}
	if !slices.Contains(permissions, signoutPath) {
		return SessionResult{}, domainError(ErrorForbidden, "account has no safe signout permission", nil)
	}
	if err = qtx.ResetSigninFailures(ctx, user.ID); err != nil {
		return SessionResult{}, s.internal("reset signin failures", err)
	}
	if err = qtx.CreateAppSession(ctx, dbsqlc.CreateAppSessionParams{
		ID: newID(), UserID: user.ID, TokenHash: tokenHash(sessionToken), CsrfTokenHash: tokenHash(csrfToken),
		IdleExpiresAt: timestamptz(idleEnds), AbsoluteExpiresAt: timestamptz(absoluteEnds),
	}); err != nil {
		return SessionResult{}, s.internal("create session", err)
	}
	if err = s.audit(ctx, qtx, "USER_SIGNIN", &user.ID, "user", &user.ID, "SUCCESS", requestID, nil); err != nil {
		return SessionResult{}, s.internal("audit signin", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return SessionResult{}, s.internal("commit signin", err)
	}
	return SessionResult{
		Data:         SessionData{User: userSummary(user), CSRFToken: csrfToken, Permissions: permissions},
		SessionToken: sessionToken, ExpiresAt: absoluteEnds,
	}, nil
}

func (s *Service) RestoreSession(ctx context.Context, rawToken string) (SessionResult, error) {
	principal, err := s.loadPrincipal(ctx, rawToken)
	if err != nil {
		return SessionResult{}, err
	}
	csrfToken, err := newRawToken()
	if err != nil {
		return SessionResult{}, s.internal("generate csrf token", err)
	}
	idleEnds := time.Now().UTC().Add(s.cfg.SessionIdleTimeout)
	rows, err := s.queries.RotateAppSessionCSRF(ctx, dbsqlc.RotateAppSessionCSRFParams{
		ID: principal.SessionID, CsrfTokenHash: tokenHash(csrfToken), IdleExpiresAt: timestamptz(idleEnds),
	})
	if err != nil {
		return SessionResult{}, s.internal("rotate csrf token", err)
	}
	if rows != 1 {
		return SessionResult{}, domainError(ErrorUnauthenticated, "session expired", nil)
	}
	return SessionResult{Data: SessionData{User: principal.User, CSRFToken: csrfToken, Permissions: principal.Permissions}, ExpiresAt: principal.AbsoluteEnds}, nil
}

func (s *Service) Authorize(ctx context.Context, rawToken, csrfToken, path string) (Principal, error) {
	principal, err := s.loadPrincipal(ctx, rawToken)
	if err != nil {
		return Principal{}, err
	}
	if csrfToken == "" || !constantTimeHashEqual(principal.CSRFHash, csrfToken) {
		return Principal{}, domainError(ErrorForbidden, "csrf validation failed", nil)
	}
	if !slices.Contains(principal.Permissions, path) {
		return Principal{}, domainError(ErrorForbidden, "permission denied", nil)
	}
	idleEnds := time.Now().UTC().Add(s.cfg.SessionIdleTimeout)
	if err = s.queries.TouchAppSession(ctx, dbsqlc.TouchAppSessionParams{ID: principal.SessionID, IdleExpiresAt: timestamptz(idleEnds)}); err != nil {
		return Principal{}, s.internal("touch session", err)
	}
	return principal, nil
}

func (s *Service) loadPrincipal(ctx context.Context, rawToken string) (Principal, error) {
	if rawToken == "" || len(rawToken) > 256 {
		return Principal{}, domainError(ErrorUnauthenticated, "session expired", nil)
	}
	session, err := s.queries.GetAppSessionByTokenHash(ctx, tokenHash(rawToken))
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, domainError(ErrorUnauthenticated, "session expired", nil)
	}
	if err != nil {
		return Principal{}, s.internal("read session", err)
	}
	now := time.Now().UTC()
	if session.RevokedAt.Valid || !session.IdleExpiresAt.Valid || !session.AbsoluteExpiresAt.Valid ||
		!session.IdleExpiresAt.Time.After(now) || !session.AbsoluteExpiresAt.Time.After(now) || session.UserStatus != StatusEnabled {
		return Principal{}, domainError(ErrorUnauthenticated, "session expired", nil)
	}
	permissions, err := s.queries.GetAppUserPermissions(ctx, session.UserID)
	if err != nil {
		return Principal{}, s.internal("load current permissions", err)
	}
	if !slices.Contains(permissions, signoutPath) {
		_ = s.queries.RevokeAppSession(ctx, dbsqlc.RevokeAppSessionParams{ID: session.ID, Reason: stringPointer("unsafe_authorization")})
		return Principal{}, domainError(ErrorUnauthenticated, "session expired", nil)
	}
	return Principal{
		SessionID: session.ID, User: UserSummary{ID: session.UserID, Username: session.Username, DisplayName: session.DisplayName},
		CSRFHash: session.CsrfTokenHash, Permissions: permissions,
		IdleExpires: session.IdleExpiresAt.Time, AbsoluteEnds: session.AbsoluteExpiresAt.Time,
	}, nil
}

func (s *Service) Signout(ctx context.Context, principal Principal, requestID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return s.internal("begin signout", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := s.queries.WithTx(tx)
	if err = qtx.RevokeAppSession(ctx, dbsqlc.RevokeAppSessionParams{ID: principal.SessionID, Reason: stringPointer("signout")}); err != nil {
		return s.internal("revoke session", err)
	}
	if err = s.audit(ctx, qtx, "USER_SIGNOUT", &principal.User.ID, "session", &principal.SessionID, "SUCCESS", requestID, nil); err != nil {
		return s.internal("audit signout", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return s.internal("commit signout", err)
	}
	return nil
}

func (s *Service) GetProfile(ctx context.Context, userID string) (ProfileView, error) {
	user, err := s.queries.GetAppUserByID(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && user.Status != StatusEnabled) {
		return ProfileView{}, domainError(ErrorUnauthenticated, "session expired", nil)
	}
	if err != nil {
		return ProfileView{}, s.internal("get current user profile", err)
	}
	return ProfileView{
		ID: user.ID, Username: user.Username, DisplayName: user.DisplayName,
		PasswordChangedAt: user.PasswordChangedAt.Time, Revision: user.Revision,
	}, nil
}

func (s *Service) ChangePassword(ctx context.Context, principal Principal, input ChangePasswordInput, requestID string) error {
	if input.CurrentPassword == "" || len(input.CurrentPassword) > 1024 {
		return domainError(ErrorValidation, "current password is incorrect", nil)
	}
	if err := validatePassword(input.NewPassword, s.cfg.PasswordMinLength); err != nil {
		return domainError(ErrorValidation, err.Error(), nil)
	}

	current, err := s.queries.GetAppUserByID(ctx, principal.User.ID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && current.Status != StatusEnabled) {
		return domainError(ErrorUnauthenticated, "session expired", nil)
	}
	if err != nil {
		return s.internal("read password user", err)
	}
	if !verifyPassword(current.PasswordHash, input.CurrentPassword) {
		_ = s.audit(ctx, s.queries, "USER_CHANGE_PASSWORD", &current.ID, "user", &current.ID, "FAILURE", requestID, map[string]any{"reason": "invalid_current_password"})
		return domainError(ErrorValidation, "current password is incorrect", nil)
	}
	if verifyPassword(current.PasswordHash, input.NewPassword) {
		return domainError(ErrorValidation, "new password must differ from current password", nil)
	}
	newHash, err := hashPassword(input.NewPassword)
	if err != nil {
		return s.internal("hash new password", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return s.internal("begin password change", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := s.queries.WithTx(tx)
	locked, err := qtx.GetAppUserByIDForUpdate(ctx, current.ID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && locked.Status != StatusEnabled) {
		return domainError(ErrorUnauthenticated, "session expired", nil)
	}
	if err != nil {
		return s.internal("lock password user", err)
	}
	if locked.Revision != current.Revision || locked.PasswordHash != current.PasswordHash {
		return domainError(ErrorConflict, "user changed concurrently; retry with the current password", nil)
	}
	rows, err := qtx.UpdateAppUserPassword(ctx, dbsqlc.UpdateAppUserPasswordParams{
		ID: locked.ID, Revision: locked.Revision, PasswordHash: newHash, ActorID: &locked.ID,
	})
	if err != nil {
		return s.writeError("update password", err)
	}
	if rows != 1 {
		return domainError(ErrorConflict, "user changed concurrently", nil)
	}
	if err = qtx.RevokeAppUserSessions(ctx, dbsqlc.RevokeAppUserSessionsParams{UserID: locked.ID, Reason: stringPointer("password_changed")}); err != nil {
		return s.internal("revoke sessions after password change", err)
	}
	if err = s.audit(ctx, qtx, "USER_CHANGE_PASSWORD", &locked.ID, "user", &locked.ID, "SUCCESS", requestID, nil); err != nil {
		return s.internal("audit password change", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return s.internal("commit password change", err)
	}
	return nil
}

func (s *Service) QueryUsers(ctx context.Context, request PageRequest) (Page[UserView], error) {
	page, pageSize, sortField, sortOrder, err := validatePage(request, map[string]bool{"createdAt": true, "username": true, "displayName": true}, "createdAt")
	if err != nil {
		return Page[UserView]{}, err
	}
	status, err := optionalStatus(request.Filters["status"])
	if err != nil {
		return Page[UserView]{}, err
	}
	search := optionalTrimmed(request.Filters["search"])
	total, err := s.queries.CountAppUsers(ctx, dbsqlc.CountAppUsersParams{Status: status, Search: search})
	if err != nil {
		return Page[UserView]{}, s.internal("count users", err)
	}
	rows, err := s.queries.ListAppUsers(ctx, dbsqlc.ListAppUsersParams{Status: status, Search: search, SortField: sortField, SortOrder: sortOrder, PageOffset: int32((page - 1) * pageSize), PageSize: int32(pageSize)})
	if err != nil {
		return Page[UserView]{}, s.internal("list users", err)
	}
	items := make([]UserView, 0, len(rows))
	for _, row := range rows {
		items = append(items, userListView(row))
	}
	return Page[UserView]{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

func (s *Service) GetUser(ctx context.Context, id string) (UserView, error) {
	user, err := s.queries.GetAppUserByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return UserView{}, domainError(ErrorNotFound, "user not found", nil)
	}
	if err != nil {
		return UserView{}, s.internal("get user", err)
	}
	roles, err := s.queries.GetAppUserRoleIDs(ctx, id)
	if err != nil {
		return UserView{}, s.internal("get user roles", err)
	}
	view := userView(user)
	view.RoleIDs = roles
	return view, nil
}

type CreateUserInput struct {
	Username    string   `json:"username"`
	DisplayName string   `json:"displayName"`
	Password    string   `json:"password"`
	RoleIDs     []string `json:"roleIds"`
}

func (s *Service) CreateUser(ctx context.Context, input CreateUserInput, actorID, requestID string) (UserView, error) {
	input.Username = normalizeUsername(input.Username)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.RoleIDs = uniqueStrings(input.RoleIDs)
	if len(input.Username) < 3 || len(input.Username) > 64 || len(input.DisplayName) == 0 || len(input.DisplayName) > 128 || len(input.RoleIDs) == 0 {
		return UserView{}, domainError(ErrorValidation, "invalid user fields", nil)
	}
	if err := validatePassword(input.Password, s.cfg.PasswordMinLength); err != nil {
		return UserView{}, domainError(ErrorValidation, err.Error(), nil)
	}
	passwordHash, err := hashPassword(input.Password)
	if err != nil {
		return UserView{}, s.internal("hash password", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return UserView{}, s.internal("begin create user", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := s.queries.WithTx(tx)
	if err = qtx.AcquireAppAuthorizationLock(ctx); err != nil {
		return UserView{}, s.internal("lock user authorization update", err)
	}
	if err = validateRoles(ctx, qtx, input.RoleIDs); err != nil {
		return UserView{}, err
	}
	id := newID()
	if err = qtx.InsertAppUser(ctx, dbsqlc.InsertAppUserParams{ID: id, Username: input.Username, DisplayName: input.DisplayName, PasswordHash: passwordHash, ActorID: &actorID}); err != nil {
		return UserView{}, s.writeError("create user", err)
	}
	if err = replaceUserRoles(ctx, qtx, id, input.RoleIDs, actorID); err != nil {
		return UserView{}, err
	}
	if err = ensureSafeSignout(ctx, qtx, id); err != nil {
		return UserView{}, err
	}
	if err = s.audit(ctx, qtx, "USER_CREATE", &actorID, "user", &id, "SUCCESS", requestID, map[string]any{"roleCount": len(input.RoleIDs)}); err != nil {
		return UserView{}, s.internal("audit create user", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return UserView{}, s.internal("commit create user", err)
	}
	return s.GetUser(ctx, id)
}

type SaveUserInput struct {
	ID          string   `json:"id"`
	DisplayName string   `json:"displayName"`
	RoleIDs     []string `json:"roleIds"`
	Revision    int64    `json:"revision"`
}

func (s *Service) SaveUser(ctx context.Context, input SaveUserInput, actorID, requestID string) (UserView, error) {
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.RoleIDs = uniqueStrings(input.RoleIDs)
	if input.ID == "" || input.Revision < 1 || len(input.DisplayName) == 0 || len(input.DisplayName) > 128 || len(input.RoleIDs) == 0 {
		return UserView{}, domainError(ErrorValidation, "invalid user fields", nil)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return UserView{}, s.internal("begin save user", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := s.queries.WithTx(tx)
	if err = qtx.AcquireAppAuthorizationLock(ctx); err != nil {
		return UserView{}, s.internal("lock user authorization update", err)
	}
	if err = validateRoles(ctx, qtx, input.RoleIDs); err != nil {
		return UserView{}, err
	}
	rows, err := qtx.UpdateAppUser(ctx, dbsqlc.UpdateAppUserParams{ID: input.ID, DisplayName: input.DisplayName, Revision: input.Revision, ActorID: &actorID})
	if err != nil {
		return UserView{}, s.writeError("save user", err)
	}
	if rows != 1 {
		return UserView{}, domainError(ErrorConflict, "user revision conflict", nil)
	}
	if err = replaceUserRoles(ctx, qtx, input.ID, input.RoleIDs, actorID); err != nil {
		return UserView{}, err
	}
	if err = ensureSafeSignout(ctx, qtx, input.ID); err != nil {
		return UserView{}, err
	}
	if err = ensureGlobalAuthorizationSafety(ctx, qtx); err != nil {
		return UserView{}, err
	}
	if err = s.audit(ctx, qtx, "USER_SAVE", &actorID, "user", &input.ID, "SUCCESS", requestID, map[string]any{"roleCount": len(input.RoleIDs)}); err != nil {
		return UserView{}, s.internal("audit save user", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return UserView{}, s.internal("commit save user", err)
	}
	return s.GetUser(ctx, input.ID)
}

func (s *Service) SetUserStatus(ctx context.Context, id string, revision int64, status, actorID, requestID string) (UserView, error) {
	if id == "" || revision < 1 || (status != StatusEnabled && status != StatusDisabled) {
		return UserView{}, domainError(ErrorValidation, "invalid status request", nil)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return UserView{}, s.internal("begin user status", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := s.queries.WithTx(tx)
	if err = qtx.AcquireAppAuthorizationLock(ctx); err != nil {
		return UserView{}, s.internal("lock user status update", err)
	}
	if status == StatusDisabled {
		remaining, countErr := qtx.CountOtherEnabledUsersWithPermission(ctx, dbsqlc.CountOtherEnabledUsersWithPermissionParams{ExcludedUserID: id, Path: "/app/role/save"})
		if countErr != nil {
			return UserView{}, s.internal("check authorization lockout", countErr)
		}
		if remaining == 0 {
			return UserView{}, domainError(ErrorConflict, "cannot disable the last authorization administrator", nil)
		}
	}
	if status == StatusEnabled {
		if err = ensureSafeSignout(ctx, qtx, id); err != nil {
			return UserView{}, err
		}
	}
	rows, err := qtx.SetAppUserStatus(ctx, dbsqlc.SetAppUserStatusParams{ID: id, Revision: revision, Status: status, ActorID: &actorID})
	if err != nil {
		return UserView{}, s.writeError("set user status", err)
	}
	if rows != 1 {
		return UserView{}, domainError(ErrorConflict, "user revision conflict or status unchanged", nil)
	}
	if status == StatusDisabled {
		if err = qtx.RevokeAppUserSessions(ctx, dbsqlc.RevokeAppUserSessionsParams{UserID: id, Reason: stringPointer("user_disabled")}); err != nil {
			return UserView{}, s.internal("revoke disabled user sessions", err)
		}
	}
	if err = s.audit(ctx, qtx, "USER_"+status, &actorID, "user", &id, "SUCCESS", requestID, nil); err != nil {
		return UserView{}, s.internal("audit user status", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return UserView{}, s.internal("commit user status", err)
	}
	return s.GetUser(ctx, id)
}

func (s *Service) QueryRoles(ctx context.Context, request PageRequest) (Page[RoleView], error) {
	page, pageSize, sortField, sortOrder, err := validatePage(request, map[string]bool{"createdAt": true, "code": true, "name": true}, "createdAt")
	if err != nil {
		return Page[RoleView]{}, err
	}
	status, err := optionalStatus(request.Filters["status"])
	if err != nil {
		return Page[RoleView]{}, err
	}
	search := optionalTrimmed(request.Filters["search"])
	total, err := s.queries.CountAppRoles(ctx, dbsqlc.CountAppRolesParams{Status: status, Search: search})
	if err != nil {
		return Page[RoleView]{}, s.internal("count roles", err)
	}
	rows, err := s.queries.ListAppRoles(ctx, dbsqlc.ListAppRolesParams{Status: status, Search: search, SortField: sortField, SortOrder: sortOrder, PageOffset: int32((page - 1) * pageSize), PageSize: int32(pageSize)})
	if err != nil {
		return Page[RoleView]{}, s.internal("list roles", err)
	}
	items := make([]RoleView, 0, len(rows))
	for _, row := range rows {
		items = append(items, roleView(row))
	}
	return Page[RoleView]{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

func (s *Service) GetRole(ctx context.Context, id string) (RoleView, error) {
	role, err := s.queries.GetAppRoleByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return RoleView{}, domainError(ErrorNotFound, "role not found", nil)
	}
	if err != nil {
		return RoleView{}, s.internal("get role", err)
	}
	permissions, err := s.queries.GetAppRolePermissionIDs(ctx, id)
	if err != nil {
		return RoleView{}, s.internal("get role permissions", err)
	}
	view := roleView(role)
	view.PermissionIDs = permissions
	return view, nil
}

type CreateRoleInput struct {
	Code          string   `json:"code"`
	Name          string   `json:"name"`
	Description   *string  `json:"description"`
	PermissionIDs []string `json:"permissionIds"`
}

func (s *Service) CreateRole(ctx context.Context, input CreateRoleInput, actorID, requestID string) (RoleView, error) {
	input.Code = strings.ToLower(strings.TrimSpace(input.Code))
	input.Name = strings.TrimSpace(input.Name)
	input.PermissionIDs = uniqueStrings(input.PermissionIDs)
	if !validSegment(input.Code) || len(input.Code) > 64 || len(input.Name) == 0 || len(input.Name) > 128 || len(input.PermissionIDs) == 0 {
		return RoleView{}, domainError(ErrorValidation, "invalid role fields", nil)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RoleView{}, s.internal("begin create role", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := s.queries.WithTx(tx)
	if err = qtx.AcquireAppAuthorizationLock(ctx); err != nil {
		return RoleView{}, s.internal("lock role authorization update", err)
	}
	if err = validatePermissions(ctx, qtx, input.PermissionIDs); err != nil {
		return RoleView{}, err
	}
	id := newID()
	if err = qtx.InsertAppRole(ctx, dbsqlc.InsertAppRoleParams{ID: id, Code: input.Code, Name: input.Name, Description: trimOptional(input.Description), ActorID: &actorID}); err != nil {
		return RoleView{}, s.writeError("create role", err)
	}
	if err = replaceRolePermissions(ctx, qtx, id, input.PermissionIDs, actorID); err != nil {
		return RoleView{}, err
	}
	if err = s.audit(ctx, qtx, "ROLE_CREATE", &actorID, "role", &id, "SUCCESS", requestID, map[string]any{"permissionCount": len(input.PermissionIDs)}); err != nil {
		return RoleView{}, s.internal("audit create role", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return RoleView{}, s.internal("commit create role", err)
	}
	return s.GetRole(ctx, id)
}

type SaveRoleInput struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   *string  `json:"description"`
	PermissionIDs []string `json:"permissionIds"`
	Revision      int64    `json:"revision"`
}

func (s *Service) SaveRole(ctx context.Context, input SaveRoleInput, actorID, requestID string) (RoleView, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.PermissionIDs = uniqueStrings(input.PermissionIDs)
	if input.ID == "" || input.Revision < 1 || len(input.Name) == 0 || len(input.Name) > 128 || len(input.PermissionIDs) == 0 {
		return RoleView{}, domainError(ErrorValidation, "invalid role fields", nil)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RoleView{}, s.internal("begin save role", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := s.queries.WithTx(tx)
	if err = qtx.AcquireAppAuthorizationLock(ctx); err != nil {
		return RoleView{}, s.internal("lock role authorization update", err)
	}
	if err = validatePermissions(ctx, qtx, input.PermissionIDs); err != nil {
		return RoleView{}, err
	}
	rows, err := qtx.UpdateAppRole(ctx, dbsqlc.UpdateAppRoleParams{ID: input.ID, Name: input.Name, Description: trimOptional(input.Description), Revision: input.Revision, ActorID: &actorID})
	if err != nil {
		return RoleView{}, s.writeError("save role", err)
	}
	if rows != 1 {
		return RoleView{}, domainError(ErrorConflict, "role revision conflict", nil)
	}
	if err = replaceRolePermissions(ctx, qtx, input.ID, input.PermissionIDs, actorID); err != nil {
		return RoleView{}, err
	}
	if err = ensureGlobalAuthorizationSafety(ctx, qtx); err != nil {
		return RoleView{}, err
	}
	if err = s.audit(ctx, qtx, "ROLE_SAVE", &actorID, "role", &input.ID, "SUCCESS", requestID, map[string]any{"permissionCount": len(input.PermissionIDs)}); err != nil {
		return RoleView{}, s.internal("audit save role", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return RoleView{}, s.internal("commit save role", err)
	}
	return s.GetRole(ctx, input.ID)
}

func (s *Service) SetRoleStatus(ctx context.Context, id string, revision int64, status, actorID, requestID string) (RoleView, error) {
	if id == "" || revision < 1 || (status != StatusEnabled && status != StatusDisabled) {
		return RoleView{}, domainError(ErrorValidation, "invalid status request", nil)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RoleView{}, s.internal("begin role status", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := s.queries.WithTx(tx)
	if err = qtx.AcquireAppAuthorizationLock(ctx); err != nil {
		return RoleView{}, s.internal("lock role status update", err)
	}
	if status == StatusDisabled {
		remaining, countErr := qtx.CountEnabledUsersWithPermissionExcludingRole(ctx, dbsqlc.CountEnabledUsersWithPermissionExcludingRoleParams{ExcludedRoleID: id, Path: "/app/role/save"})
		if countErr != nil {
			return RoleView{}, s.internal("check role authorization lockout", countErr)
		}
		if remaining == 0 {
			return RoleView{}, domainError(ErrorConflict, "cannot disable the last authorization administrator role", nil)
		}
	}
	rows, err := qtx.SetAppRoleStatus(ctx, dbsqlc.SetAppRoleStatusParams{ID: id, Revision: revision, Status: status, ActorID: &actorID})
	if err != nil {
		return RoleView{}, s.writeError("set role status", err)
	}
	if rows != 1 {
		return RoleView{}, domainError(ErrorConflict, "role revision conflict or status unchanged", nil)
	}
	if err = ensureGlobalAuthorizationSafety(ctx, qtx); err != nil {
		return RoleView{}, err
	}
	if err = s.audit(ctx, qtx, "ROLE_"+status, &actorID, "role", &id, "SUCCESS", requestID, nil); err != nil {
		return RoleView{}, s.internal("audit role status", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return RoleView{}, s.internal("commit role status", err)
	}
	return s.GetRole(ctx, id)
}

func (s *Service) QueryPermissions(ctx context.Context, request PageRequest) (Page[PermissionView], error) {
	page, pageSize, _, _, err := validatePage(request, map[string]bool{"path": true}, "path")
	if err != nil {
		return Page[PermissionView]{}, err
	}
	status, err := optionalStatus(request.Filters["status"])
	if err != nil {
		return Page[PermissionView]{}, err
	}
	domain, entity := optionalTrimmed(request.Filters["domain"]), optionalTrimmed(request.Filters["entity"])
	total, err := s.queries.CountAppPermissions(ctx, dbsqlc.CountAppPermissionsParams{Domain: domain, Entity: entity, Status: status})
	if err != nil {
		return Page[PermissionView]{}, s.internal("count permissions", err)
	}
	rows, err := s.queries.ListAppPermissions(ctx, dbsqlc.ListAppPermissionsParams{Domain: domain, Entity: entity, Status: status, PageOffset: int32((page - 1) * pageSize), PageSize: int32(pageSize)})
	if err != nil {
		return Page[PermissionView]{}, s.internal("list permissions", err)
	}
	items := make([]PermissionView, 0, len(rows))
	for _, row := range rows {
		items = append(items, permissionView(row))
	}
	return Page[PermissionView]{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

func (s *Service) GetPermission(ctx context.Context, id string) (PermissionView, error) {
	permission, err := s.queries.GetAppPermissionByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return PermissionView{}, domainError(ErrorNotFound, "permission not found", nil)
	}
	if err != nil {
		return PermissionView{}, s.internal("get permission", err)
	}
	count, err := s.queries.CountAppRolesUsingPermission(ctx, id)
	if err != nil {
		return PermissionView{}, s.internal("count permission references", err)
	}
	view := permissionView(permission)
	view.RoleCount = &count
	return view, nil
}

func validateRoles(ctx context.Context, q *dbsqlc.Queries, ids []string) error {
	count, err := q.CountEnabledAppRolesByIDs(ctx, ids)
	if err != nil {
		return domainError(ErrorInternal, "internal server error", err)
	}
	if count != int64(len(ids)) {
		return domainError(ErrorValidation, "one or more roles do not exist or are disabled", nil)
	}
	return nil
}

func replaceUserRoles(ctx context.Context, q *dbsqlc.Queries, userID string, roleIDs []string, actorID string) error {
	if err := q.DeleteAppUserRoles(ctx, userID); err != nil {
		return domainError(ErrorInternal, "internal server error", err)
	}
	for _, roleID := range roleIDs {
		if err := q.InsertAppUserRole(ctx, dbsqlc.InsertAppUserRoleParams{UserID: userID, RoleID: roleID, ActorID: &actorID}); err != nil {
			return domainError(ErrorInternal, "internal server error", err)
		}
	}
	return nil
}

func ensureSafeSignout(ctx context.Context, q *dbsqlc.Queries, userID string) error {
	paths, err := q.GetAppUserPermissions(ctx, userID)
	if err != nil {
		return domainError(ErrorInternal, "internal server error", err)
	}
	if !slices.Contains(paths, signoutPath) {
		return domainError(ErrorValidation, "assigned roles must grant /app/user/signout", nil)
	}
	return nil
}

func ensureGlobalAuthorizationSafety(ctx context.Context, q *dbsqlc.Queries) error {
	admins, err := q.CountEnabledUsersWithPermission(ctx, "/app/role/save")
	if err != nil {
		return domainError(ErrorInternal, "internal server error", err)
	}
	if admins == 0 {
		return domainError(ErrorConflict, "change would remove the last authorization administrator", nil)
	}
	missingSignout, err := q.CountEnabledUsersMissingPermission(ctx, signoutPath)
	if err != nil {
		return domainError(ErrorInternal, "internal server error", err)
	}
	if missingSignout != 0 {
		return domainError(ErrorConflict, "every enabled user must retain /app/user/signout", nil)
	}
	return nil
}

func validatePermissions(ctx context.Context, q *dbsqlc.Queries, ids []string) error {
	count, err := q.CountEnabledAppPermissionsByIDs(ctx, ids)
	if err != nil {
		return domainError(ErrorInternal, "internal server error", err)
	}
	if count != int64(len(ids)) {
		return domainError(ErrorValidation, "one or more permissions do not exist or are disabled", nil)
	}
	paths, err := q.ListAppPermissionPathsByIDs(ctx, ids)
	if err != nil {
		return domainError(ErrorInternal, "internal server error", err)
	}
	pathSet := make(map[string]bool, len(paths))
	for _, path := range paths {
		pathSet[path] = true
	}
	for _, path := range paths {
		parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
		if len(parts) != 3 || parts[2] == "query" || path == signoutPath || path == "/app/user/profile" || path == "/app/user/change-password" {
			continue
		}
		queryPath := "/" + parts[0] + "/" + parts[1] + "/query"
		if !pathSet[queryPath] {
			return domainError(ErrorValidation, fmt.Sprintf("permission %s requires %s", path, queryPath), nil)
		}
	}
	return nil
}

func replaceRolePermissions(ctx context.Context, q *dbsqlc.Queries, roleID string, permissionIDs []string, actorID string) error {
	if err := q.DeleteAppRolePermissions(ctx, roleID); err != nil {
		return domainError(ErrorInternal, "internal server error", err)
	}
	for _, permissionID := range permissionIDs {
		if err := q.InsertAppRolePermission(ctx, dbsqlc.InsertAppRolePermissionParams{RoleID: roleID, PermissionID: permissionID, ActorID: &actorID}); err != nil {
			return domainError(ErrorInternal, "internal server error", err)
		}
	}
	return nil
}

func validatePage(request PageRequest, allowedSort map[string]bool, defaultSort string) (int, int, string, string, error) {
	page, pageSize := request.Page, request.PageSize
	if page == 0 {
		page = 1
	}
	if pageSize == 0 {
		pageSize = 20
	}
	if page < 1 || pageSize < 1 || pageSize > 200 || len(request.Sort) > 1 {
		return 0, 0, "", "", domainError(ErrorValidation, "invalid pagination", nil)
	}
	field, order := defaultSort, "desc"
	if len(request.Sort) == 1 {
		field, order = request.Sort[0].Field, strings.ToLower(request.Sort[0].Order)
	}
	if !allowedSort[field] || (order != "asc" && order != "desc") {
		return 0, 0, "", "", domainError(ErrorValidation, "invalid sort", nil)
	}
	return page, pageSize, field, order, nil
}

func optionalStatus(value string) (*string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if value != StatusEnabled && value != StatusDisabled {
		return nil, domainError(ErrorValidation, "invalid status filter", nil)
	}
	return &value, nil
}

func optionalTrimmed(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func trimOptional(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func uniqueStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	slices.Sort(result)
	return result
}

func validSegment(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") || strings.HasSuffix(value, "-") || strings.Contains(value, "--") {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return false
		}
	}
	return true
}

func (s *Service) audit(ctx context.Context, q *dbsqlc.Queries, event string, actorID *string, targetType string, targetID *string, result, requestID string, summary map[string]any) error {
	if summary == nil {
		summary = map[string]any{}
	}
	payload, _ := json.Marshal(summary)
	err := q.CreateAppAuditEvent(ctx, dbsqlc.CreateAppAuditEventParams{
		ID: newID(), EventType: event, ActorUserID: actorID, TargetType: &targetType, TargetID: targetID,
		Result: result, RequestID: optionalTrimmed(requestID), Summary: payload, CreatedBy: actorID,
	})
	if err != nil {
		s.logger.Error("write app audit event", "event", event, "requestId", requestID, "error", err)
		return err
	}
	return nil
}

func (s *Service) internal(operation string, err error) error {
	s.logger.Error("app domain failure", "operation", operation, "error", err)
	return domainError(ErrorInternal, "internal server error", err)
}

func (s *Service) writeError(operation string, err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && (postgresError.Code == "23505" || postgresError.Code == "23503") {
		return domainError(ErrorConflict, "data conflict", err)
	}
	return s.internal(operation, err)
}

func timestamptz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}
func stringPointer(value string) *string { return &value }

func userSummary(user dbsqlc.AppUser) UserSummary {
	return UserSummary{ID: user.ID, Username: user.Username, DisplayName: user.DisplayName}
}

func userView(user dbsqlc.AppUser) UserView {
	return UserView{ID: user.ID, Username: user.Username, DisplayName: user.DisplayName, Status: user.Status,
		FailedSigninCount: user.FailedSigninCount, LockedUntil: nullableTime(user.LockedUntil),
		PasswordChangedAt: user.PasswordChangedAt.Time, CreatedAt: user.CreatedAt.Time, UpdatedAt: user.UpdatedAt.Time, Revision: user.Revision}
}

func userListView(user dbsqlc.ListAppUsersRow) UserView {
	return UserView{ID: user.ID, Username: user.Username, DisplayName: user.DisplayName, Status: user.Status,
		FailedSigninCount: user.FailedSigninCount, LockedUntil: nullableTime(user.LockedUntil),
		PasswordChangedAt: user.PasswordChangedAt.Time, CreatedAt: user.CreatedAt.Time, UpdatedAt: user.UpdatedAt.Time, Revision: user.Revision}
}

func roleView(role dbsqlc.AppRole) RoleView {
	return RoleView{ID: role.ID, Code: role.Code, Name: role.Name, Description: role.Description, Status: role.Status,
		CreatedAt: role.CreatedAt.Time, UpdatedAt: role.UpdatedAt.Time, Revision: role.Revision}
}

func permissionView(permission dbsqlc.AppPermission) PermissionView {
	return PermissionView{ID: permission.ID, Path: permission.Path, Domain: permission.Domain, Entity: permission.Entity,
		Action: permission.Action, Description: permission.Description, Status: permission.Status, Revision: permission.Revision}
}

func nullableTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	timeValue := value.Time
	return &timeValue
}
