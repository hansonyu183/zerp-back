package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hansonyu183/zerp-back/internal/api/response"
)

func TestRequestLoggerIncludesBusinessCode(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	router := gin.New()
	router.Use(RequestID(), RequestLogger(logger))
	router.POST("/app/test", func(c *gin.Context) {
		response.BusinessError(c, response.CodeForbidden, "forbidden", nil)
	})

	request := httptest.NewRequest(http.MethodPost, "/app/test", nil)
	request.Header.Set(requestIDHeader, "request-123")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	var entry map[string]any
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatalf("decode log entry: %v", err)
	}
	if got := entry["requestId"]; got != "request-123" {
		t.Fatalf("requestId = %v, want request-123", got)
	}
	if got := entry["businessCode"]; got != float64(response.CodeForbidden) {
		t.Fatalf("businessCode = %v, want %d", got, response.CodeForbidden)
	}
}
