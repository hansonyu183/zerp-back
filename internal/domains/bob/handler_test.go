package bob

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hansonyu183/zerp-back/internal/api/authorization"
	"github.com/hansonyu183/zerp-back/internal/api/middleware"
	"github.com/hansonyu183/zerp-back/internal/api/response"
)

type serviceStub struct {
	queryCalls int
	entity     string
}

func (s *serviceStub) Query(_ context.Context, entity string, input QueryInput) (Page[QueryItem], error) {
	s.queryCalls++
	s.entity = entity
	return Page[QueryItem]{Items: []QueryItem{}, Page: input.Page, PageSize: input.PageSize}, nil
}

func (*serviceStub) Get(context.Context, string, GetInput) (ObjectView, error) {
	return ObjectView{}, nil
}

func (*serviceStub) Create(context.Context, string, CreateInput, string, string) (MutationResult, error) {
	return MutationResult{}, nil
}

func (*serviceStub) Edit(context.Context, string, ObjectRevisionInput, string, string) (MutationResult, error) {
	return MutationResult{}, nil
}

func (*serviceStub) Save(context.Context, string, SaveInput, string, string) (MutationResult, error) {
	return MutationResult{}, nil
}

func (*serviceStub) Submit(context.Context, string, VersionRevisionInput, string, string) (MutationResult, error) {
	return MutationResult{}, nil
}

func (*serviceStub) Approve(context.Context, string, ReviewInput, string, string) (MutationResult, error) {
	return MutationResult{}, nil
}

func (*serviceStub) Reject(context.Context, string, ReviewInput, string, string) (MutationResult, error) {
	return MutationResult{}, nil
}

func (*serviceStub) Versions(context.Context, string, HistoryInput) (Page[VersionHistoryItem], error) {
	return Page[VersionHistoryItem]{Items: []VersionHistoryItem{}}, nil
}

func (*serviceStub) AuditHistory(context.Context, string, HistoryInput) (Page[AuditEventView], error) {
	return Page[AuditEventView]{Items: []AuditEventView{}}, nil
}

func testBOBLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newBOBTestRouter(service applicationService, authorizer authorization.Authorizer) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.RequestID())
	NewHandler(service, authorizer, testBOBLogger()).Register(router)
	return router
}

func TestHandlerRegistersEveryEntityAction(t *testing.T) {
	router := newBOBTestRouter(&serviceStub{}, authorization.FailClosed{})
	routes := router.Routes()
	wanted := make(map[string]bool, len(Entities)*len(Actions))
	for _, entity := range Entities {
		for _, action := range Actions {
			wanted["/bob/"+entity+"/"+action] = false
		}
	}
	for _, route := range routes {
		if _, exists := wanted[route.Path]; exists && route.Method == http.MethodPost {
			wanted[route.Path] = true
		}
	}
	for path, found := range wanted {
		if !found {
			t.Errorf("route %s is not registered", path)
		}
	}
}

func TestHandlerUsesExactPermissionPathAndPrincipal(t *testing.T) {
	service := &serviceStub{}
	var permission string
	authorizer := authorization.Func(func(_ context.Context, _ *http.Request, path string) (authorization.Principal, error) {
		permission = path
		return authorization.Principal{ActorID: "01J00000000000000000000000"}, nil
	})
	router := newBOBTestRouter(service, authorizer)
	request := httptest.NewRequest(http.MethodPost, "/bob/fund-account/query", strings.NewReader(`{"page":1,"pageSize":20,"filters":{},"sort":[]}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if permission != "/bob/fund-account/query" {
		t.Fatalf("permission = %q", permission)
	}
	if service.queryCalls != 1 || service.entity != EntityFundAccount {
		t.Fatalf("query calls = %d, entity = %q", service.queryCalls, service.entity)
	}
}

func TestHandlerFailsClosedWithoutAuthorizer(t *testing.T) {
	router := newBOBTestRouter(&serviceStub{}, nil)
	request := httptest.NewRequest(http.MethodPost, "/bob/customer/query", strings.NewReader(`{"page":1,"pageSize":20,"filters":{},"sort":[]}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	var envelope response.Envelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Code != response.CodeUnauthenticated {
		t.Fatalf("code = %d, want %d", envelope.Code, response.CodeUnauthenticated)
	}
}

func TestHandlerRejectsUnknownJSONFields(t *testing.T) {
	service := &serviceStub{}
	authorizer := authorization.Func(func(_ context.Context, _ *http.Request, _ string) (authorization.Principal, error) {
		return authorization.Principal{ActorID: "01J00000000000000000000000"}, nil
	})
	router := newBOBTestRouter(service, authorizer)
	request := httptest.NewRequest(http.MethodPost, "/bob/customer/query", strings.NewReader(`{"page":1,"pageSize":20,"filters":{},"sort":[],"unknown":true}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	var envelope response.Envelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Code != response.CodeValidation {
		t.Fatalf("code = %d, want %d", envelope.Code, response.CodeValidation)
	}
	if service.queryCalls != 0 {
		t.Fatalf("service was called %d times", service.queryCalls)
	}
}
