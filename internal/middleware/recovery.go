package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Recovery is a Gin middleware that recovers from panics.
// When a panic occurs, it logs the stack trace at Error level, and returns a 500
// response to the client with a generic error message (no internal details leaked).
//
// Parameters:
//   logger - the Zap logger instance
func Recovery(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				stack := string(debug.Stack())

				// Build log fields
				fields := []zap.Field{
					zap.Any("panic", err),
					zap.String("stack", stack),
					zap.String("method", c.Request.Method),
					zap.String("path", c.Request.URL.Path),
				}

				if traceID := GetTraceID(c); traceID != "" {
					fields = append(fields, zap.String("trace_id", traceID))
				}

				logger.Error(fmt.Sprintf("PANIC recovered: %v", err), fields...)

				// Return generic 500 - do not leak internal details
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"code":    1003,
					"message": "internal server error",
				})
			}
		}()
		c.Next()
	}
}
