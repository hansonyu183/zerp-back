package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

const (
	CodeOK              = 0
	CodeUnauthenticated = 1001
	CodeForbidden       = 1002
	CodeValidation      = 2001
	CodeConflict        = 3001
	CodeInternal        = 5000
)

type Envelope struct {
	Code      int    `json:"code"`
	Message   string `json:"message"`
	Data      any    `json:"data"`
	RequestID string `json:"requestId"`
}

func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Envelope{
		Code:      CodeOK,
		Message:   "ok",
		Data:      data,
		RequestID: RequestID(c),
	})
}

func BusinessError(c *gin.Context, code int, message string, data any) {
	c.JSON(http.StatusOK, Envelope{
		Code:      code,
		Message:   message,
		Data:      data,
		RequestID: RequestID(c),
	})
}

func RequestID(c *gin.Context) string {
	return c.GetString("requestId")
}
