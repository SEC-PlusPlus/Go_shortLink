// Package middleware 提供 Gin 中间件集合。
// 包含 TraceID 注入、请求日志记录、Panic 恢复三个中间件。
package middleware

import (
	"crypto/rand"
	"fmt"

	"github.com/gin-gonic/gin"
)

// newTraceID 使用 crypto/rand 生成 16 字节随机数，格式化为类 UUID 的字符串。
// 格式：xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx（8-4-4-4-12）
//
// 使用 crypto/rand 而非 math/rand 以获取密码学安全的随机数，避免 ID 碰撞。
func newTraceID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

const (
	// TraceIDKey 是 Gin Context 中存储 trace_id 的 key。
	TraceIDKey = "trace_id"
	// HeaderTraceID 是 HTTP 请求/响应头中传递 trace_id 的字段名。
	// 前端或上游服务可通过此头传入 trace_id 以支持跨服务链路追踪。
	HeaderTraceID = "X-Trace-ID"
)

// TraceID 是一个 Gin 中间件，为每个请求注入唯一的 trace_id。
//
// 行为：
//   1. 检查请求头 X-Trace-ID：如果存在则复用（支持跨服务追踪）
//   2. 不存在 → 调用 newTraceID() 生成新的唯一 ID
//   3. 将 trace_id 存入 Gin Context（c.Set），供后续 Handler 和中间件读取
//   4. 将 trace_id 写入响应头 X-Trace-ID，方便客户端关联日志
//
// 注册位置：中间件链的第一位，确保 Logger 和 Recovery 能读取到 trace_id。
func TraceID() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 尝试从请求头获取上游传入的 trace_id
		traceID := c.GetHeader(HeaderTraceID)
		if traceID == "" {
			traceID = newTraceID() // 无上游传入，自行生成
		}

		// 存入 Gin Context（后续通过 GetTraceID 读取）
		c.Set(TraceIDKey, traceID)

		// 写入响应头
		c.Header(HeaderTraceID, traceID)

		c.Next()
	}
}

// GetTraceID 从 Gin Context 中提取 trace_id。
// 所有需要记录日志的组件（Logger、Recovery、Handler）通过此函数读取。
//
// 返回值：trace_id 字符串，若不存在返回空字符串。
func GetTraceID(c *gin.Context) string {
	if id, exists := c.Get(TraceIDKey); exists {
		if s, ok := id.(string); ok {
			return s
		}
	}
	return ""
}
