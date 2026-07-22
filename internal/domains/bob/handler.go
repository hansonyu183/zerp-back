package bob

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hansonyu183/zerp-back/internal/api/authorization"
	"github.com/hansonyu183/zerp-back/internal/api/response"
)

const principalContextKey = "bobPrincipal"

type applicationService interface {
	Query(context.Context, string, QueryInput) (Page[QueryItem], error)
	Get(context.Context, string, GetInput) (ObjectView, error)
	Create(context.Context, string, CreateInput, string, string) (MutationResult, error)
	Edit(context.Context, string, ObjectRevisionInput, string, string) (MutationResult, error)
	Save(context.Context, string, SaveInput, string, string) (MutationResult, error)
	Submit(context.Context, string, VersionRevisionInput, string, string) (MutationResult, error)
	Approve(context.Context, string, ReviewInput, string, string) (MutationResult, error)
	Reject(context.Context, string, ReviewInput, string, string) (MutationResult, error)
	Versions(context.Context, string, HistoryInput) (Page[VersionHistoryItem], error)
	AuditHistory(context.Context, string, HistoryInput) (Page[AuditEventView], error)
}

type Handler struct {
	service    applicationService
	authorizer authorization.Authorizer
	logger     *slog.Logger
}

func NewHandler(service applicationService, authorizer authorization.Authorizer, logger *slog.Logger) *Handler {
	if authorizer == nil {
		authorizer = authorization.FailClosed{}
	}
	return &Handler{service: service, authorizer: authorizer, logger: logger}
}

func (h *Handler) Register(router *gin.Engine) {
	group := router.Group("/bob")
	for _, registeredEntity := range Entities {
		entity := registeredEntity
		entityGroup := group.Group("/" + entity)
		for _, registeredAction := range Actions {
			action := registeredAction
			path := "/bob/" + entity + "/" + action
			entityGroup.POST("/"+action, h.authorize(path), h.endpoint(entity, action))
		}
	}
}

func (h *Handler) authorize(path string) gin.HandlerFunc {
	return func(c *gin.Context) {
		principal, err := h.authorizer.Authorize(c.Request.Context(), c.Request, path)
		if err != nil {
			h.writeAuthorizationError(c, err)
			c.Abort()
			return
		}
		if principal.ActorID == "" {
			h.writeAuthorizationError(c, authorization.NewError(authorization.ErrorUnauthenticated, "session expired", nil))
			c.Abort()
			return
		}
		c.Set(principalContextKey, principal)
		c.Next()
	}
}

func (h *Handler) endpoint(entity, action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		switch action {
		case "query":
			var input QueryInput
			if h.bind(c, &input) {
				result, err := h.service.Query(c.Request.Context(), entity, input)
				h.result(c, result, err)
			}
		case "get":
			var input GetInput
			if h.bind(c, &input) {
				result, err := h.service.Get(c.Request.Context(), entity, input)
				h.result(c, result, err)
			}
		case "create":
			var input CreateInput
			if h.bind(c, &input) {
				result, err := h.service.Create(c.Request.Context(), entity, input, h.actorID(c), response.RequestID(c))
				h.result(c, result, err)
			}
		case "edit":
			var input ObjectRevisionInput
			if h.bind(c, &input) {
				result, err := h.service.Edit(c.Request.Context(), entity, input, h.actorID(c), response.RequestID(c))
				h.result(c, result, err)
			}
		case "save":
			var input SaveInput
			if h.bind(c, &input) {
				result, err := h.service.Save(c.Request.Context(), entity, input, h.actorID(c), response.RequestID(c))
				h.result(c, result, err)
			}
		case "submit":
			var input VersionRevisionInput
			if h.bind(c, &input) {
				result, err := h.service.Submit(c.Request.Context(), entity, input, h.actorID(c), response.RequestID(c))
				h.result(c, result, err)
			}
		case "approve":
			var input ReviewInput
			if h.bind(c, &input) {
				result, err := h.service.Approve(c.Request.Context(), entity, input, h.actorID(c), response.RequestID(c))
				h.result(c, result, err)
			}
		case "reject":
			var input ReviewInput
			if h.bind(c, &input) {
				result, err := h.service.Reject(c.Request.Context(), entity, input, h.actorID(c), response.RequestID(c))
				h.result(c, result, err)
			}
		case "versions":
			var input HistoryInput
			if h.bind(c, &input) {
				result, err := h.service.Versions(c.Request.Context(), entity, input)
				h.result(c, result, err)
			}
		case "audit-history":
			var input HistoryInput
			if h.bind(c, &input) {
				result, err := h.service.AuditHistory(c.Request.Context(), entity, input)
				h.result(c, result, err)
			}
		default:
			h.writeError(c, domainError(ErrorInternal, "internal server error", nil, errors.New("unregistered BOB action")))
		}
	}
}

func (h *Handler) bind(c *gin.Context, target any) bool {
	if err := decodeJSON(c, target); err != nil {
		h.writeError(c, domainError(ErrorValidation, "invalid request", nil, err))
		return false
	}
	return true
}

func decodeJSON(c *gin.Context, target any) error {
	contentType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || contentType != "application/json" {
		return errors.New("content type must be application/json")
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20))
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err = decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func (h *Handler) actorID(c *gin.Context) string {
	principal, _ := c.Get(principalContextKey)
	return principal.(authorization.Principal).ActorID
}

func (h *Handler) result(c *gin.Context, data any, err error) {
	if err != nil {
		h.writeError(c, err)
		return
	}
	response.OK(c, data)
}

func (h *Handler) writeAuthorizationError(c *gin.Context, err error) {
	code := response.CodeInternal
	message := "internal server error"
	switch {
	case authorization.IsKind(err, authorization.ErrorUnauthenticated):
		code, message = response.CodeUnauthenticated, "session expired"
	case authorization.IsKind(err, authorization.ErrorForbidden):
		code, message = response.CodeForbidden, "permission denied"
	default:
		h.logger.Error("bob authorization failure", "requestId", response.RequestID(c), "path", c.Request.URL.Path, "error", err)
	}
	response.BusinessError(c, code, message, nil)
}

func (h *Handler) writeError(c *gin.Context, err error) {
	var domainErr *DomainError
	if !errors.As(err, &domainErr) {
		domainErr = &DomainError{Kind: ErrorInternal, Message: "internal server error", Cause: err}
	}
	code := response.CodeInternal
	switch domainErr.Kind {
	case ErrorValidation:
		code = response.CodeValidation
	case ErrorConflict:
		code = response.CodeConflict
	}
	if domainErr.Kind == ErrorInternal {
		h.logger.Error("bob handler failure", "requestId", response.RequestID(c), "path", c.Request.URL.Path, "error", domainErr.Cause)
	}
	response.BusinessError(c, code, domainErr.Message, domainErr.Data)
}
