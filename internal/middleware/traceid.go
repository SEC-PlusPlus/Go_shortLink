package middleware

import (
	"crypto/rand"
	"fmt"

	"github.com/gin-gonic/gin"
)

// newTraceID generates a unique trace ID using crypto/rand.
func newTraceID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

const (
	// TraceIDKey is the context key for the trace ID.
	TraceIDKey = "trace_id"
	// HeaderTraceID is the HTTP header for the trace ID.
	HeaderTraceID = "X-Trace-ID"
)

// TraceID is a Gin middleware that injects a unique trace ID into each request's context.
// If the request already carries an X-Trace-ID header, that value is reused (for tracing
// across services). Otherwise, a new UUID is generated.
//
// The trace ID is set in both the Gin context (for handlers and downstream components)
// and the response header (for client-side tracking).
func TraceID() gin.HandlerFunc {
	return func(c *gin.Context) {
		traceID := c.GetHeader(HeaderTraceID)
		if traceID == "" {
			traceID = newTraceID()
		}

		// Store in context for use throughout the request lifecycle
		c.Set(TraceIDKey, traceID)

		// Echo back in response header
		c.Header(HeaderTraceID, traceID)

		c.Next()
	}
}

// GetTraceID extracts the trace ID from the Gin context.
// Called by handlers and loggers to associate log entries with a specific request.
func GetTraceID(c *gin.Context) string {
	if id, exists := c.Get(TraceIDKey); exists {
		if s, ok := id.(string); ok {
			return s
		}
	}
	return ""
}
