package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Logger is a Gin middleware that logs every request using Zap.
// It records the HTTP method, path, status code, latency, and trace_id.
//
// Parameters:
//   logger - the Zap logger instance
//
// Returns a gin.HandlerFunc that logs request details at Info level on completion.
func Logger(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		// Process the request
		c.Next()

		// Calculate latency
		latency := time.Since(start)

		// Build log fields
		fields := []zap.Field{
			zap.Int("status", c.Writer.Status()),
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.String("query", query),
			zap.String("ip", c.ClientIP()),
			zap.Duration("latency", latency),
			zap.Int("body_size", c.Writer.Size()),
		}

		// Include trace_id if available
		if traceID := GetTraceID(c); traceID != "" {
			fields = append(fields, zap.String("trace_id", traceID))
		}

		// Log errors at Warn level
		status := c.Writer.Status()
		if status >= 500 {
			logger.Error("request completed", fields...)
		} else if status >= 400 {
			logger.Warn("request completed", fields...)
		} else {
			logger.Info("request completed", fields...)
		}
	}
}
