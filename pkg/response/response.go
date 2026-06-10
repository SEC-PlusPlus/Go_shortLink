package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Response is the unified API response format.
type Response struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// Success returns a success response with data.
func Success(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, Response{
		Code:    0,
		Message: "ok",
		Data:    data,
	})
}

// Error returns an error response with the given HTTP status, business code, and message.
func Error(c *gin.Context, httpStatus int, code int, message string) {
	c.JSON(httpStatus, Response{
		Code:    code,
		Message: message,
	})
}

// BadRequest returns a 400 error for invalid parameters.
func BadRequest(c *gin.Context, message string) {
	Error(c, http.StatusBadRequest, 1001, message)
}

// Conflict returns a 409 error when a resource already exists.
func Conflict(c *gin.Context, message string) {
	Error(c, http.StatusConflict, 1002, message)
}

// InternalError returns a 500 error for internal failures.
func InternalError(c *gin.Context, message string) {
	Error(c, http.StatusInternalServerError, 1003, message)
}

// NotFound returns a 404 error.
func NotFound(c *gin.Context, message string) {
	Error(c, http.StatusNotFound, 1004, message)
}

// Gone returns a 410 error for expired resources.
func Gone(c *gin.Context, message string) {
	Error(c, http.StatusGone, 1005, message)
}
