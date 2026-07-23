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
	actions    []string
}

func (s *serviceStub) record(action, entity string) {
	s.entity = entity
	s.actions = append(s.actions, action)
}

func (s *serviceStub) Query(_ context.Context, entity string, input QueryInput) (Page[QueryItem], error) {
	s.queryCalls++
	s.record("query", entity)
	return Page[QueryItem]{Items: []QueryItem{}, Page: input.Page, PageSize: input.PageSize}, nil
}

func (s *serviceStub) Get(_ context.Context, entity string, _ GetInput) (ObjectView, error) {
	s.record("get", entity)
	return ObjectView{}, nil
}

func (s *serviceStub) Create(_ context.Context, entity string, _ CreateInput, _, _ string) (MutationResult, error) {
	s.record("create", entity)
	return MutationResult{}, nil
}

func (s *serviceStub) Edit(_ context.Context, entity string, _ ObjectRevisionInput, _, _ string) (MutationResult, error) {
	s.record("edit", entity)
	return MutationResult{}, nil
}

func (s *serviceStub) Save(_ context.Context, entity string, _ SaveInput, _, _ string) (MutationResult, error) {
	s.record("save", entity)
	return MutationResult{}, nil
}

func (s *serviceStub) Submit(_ context.Context, entity string, _ VersionRevisionInput, _, _ string) (MutationResult, error) {
	s.record("submit", entity)
	return MutationResult{}, nil
}

func (s *serviceStub) Approve(_ context.Context, entity string, _ ReviewInput, _, _ string) (MutationResult, error) {
	s.record("approve", entity)
	return MutationResult{}, nil
}

func (s *serviceStub) Reject(_ context.Context, entity string, _ ReviewInput, _, _ string) (MutationResult, error) {
	s.record("reject", entity)
	return MutationResult{}, nil
}

func (s *serviceStub) Versions(_ context.Context, entity string, _ HistoryInput) (Page[VersionHistoryItem], error) {
	s.record("versions", entity)
	return Page[VersionHistoryItem]{Items: []VersionHistoryItem{}}, nil
}

func (s *serviceStub) AuditHistory(_ context.Context, entity string, _ HistoryInput) (Page[AuditEventView], error) {
	s.record("audit-history", entity)
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
	expectedEntities := []string{"customer", "supplier", "employee", "product", "service", "fund-account"}
	expectedActions := []string{
		"query", "get", "create", "edit", "save",
		"submit", "approve", "reject", "versions", "audit-history",
	}
	wanted := make(map[string]bool, len(expectedEntities)*len(expectedActions))
	for _, entity := range expectedEntities {
		for _, action := range expectedActions {
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
	if len(routes) != len(wanted) {
		t.Fatalf("registered route count = %d, want %d", len(routes), len(wanted))
	}
}

func TestHandlerDispatchesEveryAction(t *testing.T) {
	const objectID = "01J00000000000000000000010"
	const versionID = "01J00000000000000000000011"
	tests := []struct {
		action string
		body   string
	}{
		{"query", `{"page":1,"pageSize":20,"filters":{},"sort":[]}`},
		{"get", `{"objectId":"` + objectID + `"}`},
		{"create", `{"data":{"code":"C1","name":"Customer"}}`},
		{"edit", `{"objectId":"` + objectID + `","objectRevision":1}`},
		{"save", `{"objectId":"` + objectID + `","versionId":"` + versionID + `","revision":1,"data":{"name":"Customer"}}`},
		{"submit", `{"objectId":"` + objectID + `","versionId":"` + versionID + `","revision":1}`},
		{"approve", `{"objectId":"` + objectID + `","versionId":"` + versionID + `","revision":1}`},
		{"reject", `{"objectId":"` + objectID + `","versionId":"` + versionID + `","revision":1,"comment":"fix"}`},
		{"versions", `{"objectId":"` + objectID + `","page":1,"pageSize":20}`},
		{"audit-history", `{"objectId":"` + objectID + `","page":1,"pageSize":20}`},
	}
	authorizer := authorization.Func(func(_ context.Context, _ *http.Request, _, _ string) (authorization.Principal, error) {
		return authorization.Principal{ActorID: "01J00000000000000000000000"}, nil
	})
	for _, test := range tests {
		t.Run(test.action, func(t *testing.T) {
			service := &serviceStub{}
			router := newBOBTestRouter(service, authorizer)
			request := httptest.NewRequest(http.MethodPost, "/bob/customer/"+test.action, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
			}
			if len(service.actions) != 1 || service.actions[0] != test.action || service.entity != EntityCustomer {
				t.Fatalf("calls = %v, entity = %q", service.actions, service.entity)
			}
		})
	}
}

func TestHandlerUsesExactPermissionPathAndPrincipal(t *testing.T) {
	service := &serviceStub{}
	var permission string
	authorizer := authorization.Func(func(_ context.Context, _ *http.Request, path, requestID string) (authorization.Principal, error) {
		permission = path
		if requestID == "" {
			t.Fatal("requestId was not forwarded")
		}
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

func TestHandlerDoesNotReadGuessedIDWithoutPermission(t *testing.T) {
	service := &serviceStub{}
	authorizer := authorization.Func(func(_ context.Context, _ *http.Request, _, _ string) (authorization.Principal, error) {
		return authorization.Principal{}, authorization.NewError(authorization.ErrorForbidden, "permission denied", nil)
	})
	router := newBOBTestRouter(service, authorizer)
	request := httptest.NewRequest(
		http.MethodPost,
		"/bob/customer/get",
		strings.NewReader(`{"objectId":"01J00000000000000000000010"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	var envelope response.Envelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Code != response.CodeForbidden {
		t.Fatalf("code = %d, want %d", envelope.Code, response.CodeForbidden)
	}
	if len(service.actions) != 0 {
		t.Fatalf("service calls = %v", service.actions)
	}
}

func TestHandlerRejectsUnknownJSONFields(t *testing.T) {
	service := &serviceStub{}
	authorizer := authorization.Func(func(_ context.Context, _ *http.Request, _, _ string) (authorization.Principal, error) {
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
