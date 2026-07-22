package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hansonyu183/zerp-back/internal/api/response"
	"github.com/hansonyu183/zerp-back/internal/config"
)

const principalContextKey = "appPrincipal"

type applicationService interface {
	Signin(context.Context, string, string, string) (SessionResult, error)
	RestoreSession(context.Context, string) (SessionResult, error)
	Authorize(context.Context, string, string, string) (Principal, error)
	Signout(context.Context, Principal, string) error
	GetProfile(context.Context, string) (ProfileView, error)
	ChangePassword(context.Context, Principal, ChangePasswordInput, string) error
	QueryUsers(context.Context, PageRequest) (Page[UserView], error)
	GetUser(context.Context, string) (UserView, error)
	CreateUser(context.Context, CreateUserInput, string, string) (UserView, error)
	SaveUser(context.Context, SaveUserInput, string, string) (UserView, error)
	SetUserStatus(context.Context, string, int64, string, string, string) (UserView, error)
	QueryRoles(context.Context, PageRequest) (Page[RoleView], error)
	GetRole(context.Context, string) (RoleView, error)
	CreateRole(context.Context, CreateRoleInput, string, string) (RoleView, error)
	SaveRole(context.Context, SaveRoleInput, string, string) (RoleView, error)
	SetRoleStatus(context.Context, string, int64, string, string, string) (RoleView, error)
	QueryPermissions(context.Context, PageRequest) (Page[PermissionView], error)
	GetPermission(context.Context, string) (PermissionView, error)
}

type Handler struct {
	service applicationService
	cfg     config.Config
	logger  *slog.Logger
	limiter *signinLimiter
}

func NewHandler(service applicationService, cfg config.Config, logger *slog.Logger) *Handler {
	return &Handler{service: service, cfg: cfg, logger: logger, limiter: newSigninLimiter()}
}

func (h *Handler) Register(router *gin.Engine) {
	appGroup := router.Group("/app")
	user := appGroup.Group("/user")
	user.POST("/signin", h.signin)
	user.POST("/session", h.session)
	user.POST("/signout", h.signout)

	protectedUser := user.Group("")
	protectedUser.Use(h.authorize())
	protectedUser.POST("/query", h.queryUsers)
	protectedUser.POST("/profile", h.profile)
	protectedUser.POST("/change-password", h.changePassword)
	protectedUser.POST("/get", h.getUser)
	protectedUser.POST("/create", h.createUser)
	protectedUser.POST("/save", h.saveUser)
	protectedUser.POST("/enable", h.setUserStatus(StatusEnabled))
	protectedUser.POST("/disable", h.setUserStatus(StatusDisabled))

	role := appGroup.Group("/role")
	role.Use(h.authorize())
	role.POST("/query", h.queryRoles)
	role.POST("/get", h.getRole)
	role.POST("/create", h.createRole)
	role.POST("/save", h.saveRole)
	role.POST("/enable", h.setRoleStatus(StatusEnabled))
	role.POST("/disable", h.setRoleStatus(StatusDisabled))

	permission := appGroup.Group("/permission")
	permission.Use(h.authorize())
	permission.POST("/query", h.queryPermissions)
	permission.POST("/get", h.getPermission)
}

func (h *Handler) authorize() gin.HandlerFunc {
	return func(c *gin.Context) {
		rawToken, _ := c.Cookie(h.cfg.SessionCookieName)
		principal, err := h.service.Authorize(c.Request.Context(), rawToken, c.GetHeader("X-CSRF-Token"), c.Request.URL.Path)
		if err != nil {
			if errorIsKind(err, ErrorUnauthenticated) {
				h.clearSessionCookie(c)
			}
			h.writeError(c, err)
			c.Abort()
			return
		}
		c.Set(principalContextKey, principal)
		c.Next()
	}
}

func (h *Handler) signin(c *gin.Context) {
	if !h.limiter.allow(c.Request) {
		h.writeError(c, domainError(ErrorUnauthenticated, "authentication failed", nil))
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(c, &input); err != nil {
		h.writeError(c, domainError(ErrorValidation, "invalid request", err))
		return
	}
	result, err := h.service.Signin(c.Request.Context(), input.Username, input.Password, response.RequestID(c))
	if err != nil {
		h.writeError(c, err)
		return
	}
	h.setSessionCookie(c, result.SessionToken, result.ExpiresAt)
	response.OK(c, result.Data)
}

func (h *Handler) session(c *gin.Context) {
	if err := decodeEmptyObject(c); err != nil {
		h.writeError(c, domainError(ErrorValidation, "request body must be an empty object", err))
		return
	}
	rawToken, _ := c.Cookie(h.cfg.SessionCookieName)
	result, err := h.service.RestoreSession(c.Request.Context(), rawToken)
	if err != nil {
		if errorIsKind(err, ErrorUnauthenticated) {
			h.clearSessionCookie(c)
		}
		h.writeError(c, err)
		return
	}
	response.OK(c, result.Data)
}

func (h *Handler) signout(c *gin.Context) {
	if err := decodeEmptyObject(c); err != nil {
		h.writeError(c, domainError(ErrorValidation, "request body must be an empty object", err))
		return
	}
	rawToken, _ := c.Cookie(h.cfg.SessionCookieName)
	if rawToken == "" {
		h.clearSessionCookie(c)
		response.OK(c, map[string]any{})
		return
	}
	principal, err := h.service.Authorize(c.Request.Context(), rawToken, c.GetHeader("X-CSRF-Token"), signoutPath)
	if err != nil {
		if errorIsKind(err, ErrorUnauthenticated) {
			h.clearSessionCookie(c)
			response.OK(c, map[string]any{})
			return
		}
		h.writeError(c, err)
		return
	}
	if err = h.service.Signout(c.Request.Context(), principal, response.RequestID(c)); err != nil {
		h.writeError(c, err)
		return
	}
	h.clearSessionCookie(c)
	response.OK(c, map[string]any{})
}

func (h *Handler) queryUsers(c *gin.Context) {
	var input PageRequest
	if !h.bind(c, &input) {
		return
	}
	result, err := h.service.QueryUsers(c.Request.Context(), input)
	h.result(c, result, err)
}

func (h *Handler) profile(c *gin.Context) {
	if err := decodeEmptyObject(c); err != nil {
		h.writeError(c, domainError(ErrorValidation, "request body must be an empty object", err))
		return
	}
	principal := currentPrincipal(c)
	result, err := h.service.GetProfile(c.Request.Context(), principal.User.ID)
	h.result(c, result, err)
}

func (h *Handler) changePassword(c *gin.Context) {
	var input ChangePasswordInput
	if !h.bind(c, &input) {
		return
	}
	principal := currentPrincipal(c)
	if err := h.service.ChangePassword(c.Request.Context(), principal, input, response.RequestID(c)); err != nil {
		h.writeError(c, err)
		return
	}
	h.clearSessionCookie(c)
	response.OK(c, map[string]any{})
}

func (h *Handler) getUser(c *gin.Context) {
	var input idInput
	if !h.bind(c, &input) {
		return
	}
	result, err := h.service.GetUser(c.Request.Context(), input.ID)
	h.result(c, result, err)
}

func (h *Handler) createUser(c *gin.Context) {
	var input CreateUserInput
	if !h.bind(c, &input) {
		return
	}
	result, err := h.service.CreateUser(c.Request.Context(), input, actorID(c), response.RequestID(c))
	h.result(c, result, err)
}

func (h *Handler) saveUser(c *gin.Context) {
	var input SaveUserInput
	if !h.bind(c, &input) {
		return
	}
	result, err := h.service.SaveUser(c.Request.Context(), input, actorID(c), response.RequestID(c))
	h.result(c, result, err)
}

func (h *Handler) setUserStatus(status string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input revisionInput
		if !h.bind(c, &input) {
			return
		}
		result, err := h.service.SetUserStatus(c.Request.Context(), input.ID, input.Revision, status, actorID(c), response.RequestID(c))
		h.result(c, result, err)
	}
}

func (h *Handler) queryRoles(c *gin.Context) {
	var input PageRequest
	if !h.bind(c, &input) {
		return
	}
	result, err := h.service.QueryRoles(c.Request.Context(), input)
	h.result(c, result, err)
}

func (h *Handler) getRole(c *gin.Context) {
	var input idInput
	if !h.bind(c, &input) {
		return
	}
	result, err := h.service.GetRole(c.Request.Context(), input.ID)
	h.result(c, result, err)
}

func (h *Handler) createRole(c *gin.Context) {
	var input CreateRoleInput
	if !h.bind(c, &input) {
		return
	}
	result, err := h.service.CreateRole(c.Request.Context(), input, actorID(c), response.RequestID(c))
	h.result(c, result, err)
}

func (h *Handler) saveRole(c *gin.Context) {
	var input SaveRoleInput
	if !h.bind(c, &input) {
		return
	}
	result, err := h.service.SaveRole(c.Request.Context(), input, actorID(c), response.RequestID(c))
	h.result(c, result, err)
}

func (h *Handler) setRoleStatus(status string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input revisionInput
		if !h.bind(c, &input) {
			return
		}
		result, err := h.service.SetRoleStatus(c.Request.Context(), input.ID, input.Revision, status, actorID(c), response.RequestID(c))
		h.result(c, result, err)
	}
}

func (h *Handler) queryPermissions(c *gin.Context) {
	var input PageRequest
	if !h.bind(c, &input) {
		return
	}
	result, err := h.service.QueryPermissions(c.Request.Context(), input)
	h.result(c, result, err)
}

func (h *Handler) getPermission(c *gin.Context) {
	var input idInput
	if !h.bind(c, &input) {
		return
	}
	result, err := h.service.GetPermission(c.Request.Context(), input.ID)
	h.result(c, result, err)
}

type idInput struct {
	ID string `json:"id"`
}

type revisionInput struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}

func (h *Handler) bind(c *gin.Context, target any) bool {
	if err := decodeJSON(c, target); err != nil {
		h.writeError(c, domainError(ErrorValidation, "invalid request", err))
		return false
	}
	return true
}

func (h *Handler) result(c *gin.Context, data any, err error) {
	if err != nil {
		h.writeError(c, err)
		return
	}
	response.OK(c, data)
}

func (h *Handler) writeError(c *gin.Context, err error) {
	var domainErr *DomainError
	if !errors.As(err, &domainErr) {
		domainErr = &DomainError{Kind: ErrorInternal, Message: "internal server error", Cause: err}
	}
	code := response.CodeInternal
	switch domainErr.Kind {
	case ErrorUnauthenticated:
		code = response.CodeUnauthenticated
	case ErrorForbidden:
		code = response.CodeForbidden
	case ErrorValidation, ErrorNotFound:
		code = response.CodeValidation
	case ErrorConflict:
		code = response.CodeConflict
	}
	if domainErr.Kind == ErrorInternal {
		h.logger.Error("app handler failure", "requestId", response.RequestID(c), "path", c.Request.URL.Path, "error", domainErr.Cause)
	}
	response.BusinessError(c, code, domainErr.Message, nil)
}

func (h *Handler) setSessionCookie(c *gin.Context, value string, expiresAt time.Time) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: h.cfg.SessionCookieName, Value: value, Path: "/", Expires: expiresAt,
		MaxAge: int(time.Until(expiresAt).Seconds()), HttpOnly: true, Secure: h.cfg.SessionCookieSecure, SameSite: h.cookieSameSite(),
	})
}

func (h *Handler) clearSessionCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: h.cfg.SessionCookieName, Value: "", Path: "/", Expires: time.Unix(1, 0),
		MaxAge: -1, HttpOnly: true, Secure: h.cfg.SessionCookieSecure, SameSite: h.cookieSameSite(),
	})
}

func (h *Handler) cookieSameSite() http.SameSite {
	switch h.cfg.SessionCookieSameSite {
	case "strict":
		return http.SameSiteStrictMode
	case "none":
		return http.SameSiteNoneMode
	default:
		return http.SameSiteLaxMode
	}
}

func actorID(c *gin.Context) string {
	return currentPrincipal(c).User.ID
}

func currentPrincipal(c *gin.Context) Principal {
	value, exists := c.Get(principalContextKey)
	if !exists {
		return Principal{}
	}
	principal, ok := value.(Principal)
	if !ok {
		return Principal{}
	}
	return principal
}

func decodeJSON(c *gin.Context, target any) error {
	mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errors.New("content type must be application/json")
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("request body must contain exactly one JSON value")
	}
	return nil
}

func decodeEmptyObject(c *gin.Context) error {
	var raw json.RawMessage
	if err := decodeJSON(c, &raw); err != nil {
		return err
	}
	compact := bytes.TrimSpace(raw)
	if !bytes.Equal(compact, []byte("{}")) {
		return errors.New("not an empty object")
	}
	return nil
}
