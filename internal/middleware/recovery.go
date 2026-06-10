package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Recovery 是一个 Gin 中间件，用于捕获 Handler 链中抛出的 panic。
//
// 为什么需要：
//   Go 的 panic 如果不被 recover，会导致整个进程崩溃。
//   Gin 内置了默认的 Recovery 中间件，但本中间件额外提供了：
//     - 使用 Zap 记录结构化错误日志（含 trace_id + 完整调用栈）
//     - 返回统一格式的 JSON 错误响应（不暴露内部代码细节）
//     - 可定制错误信息
//
// 行为：
//   1. defer + recover() 捕获 panic
//   2. 调用 runtime/debug.Stack() 获取完整调用栈
//   3. 使用 Zap Error 级别记录：panic 内容、调用栈、请求方法路径、trace_id
//   4. 返回 HTTP 500 + {"code":1003,"message":"internal server error"}
//      注意：不暴露 panic 的具体内容给客户端，防止信息泄露
//
// 参数：logger — 由 main() 初始化的全局 Zap Logger
func Recovery(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				// 获取完整调用栈（用于问题排查）
				stack := string(debug.Stack())

				// 构建日志字段（包含请求上下文信息）
				fields := []zap.Field{
					zap.Any("panic", err),             // panic 的具体内容
					zap.String("stack", stack),         // 完整调用栈
					zap.String("method", c.Request.Method),
					zap.String("path", c.Request.URL.Path),
				}

				// 附加 trace_id，方便从大量日志中定位
				if traceID := GetTraceID(c); traceID != "" {
					fields = append(fields, zap.String("trace_id", traceID))
				}

				// 记录到 Error 级别日志（通常会触发告警）
				logger.Error(fmt.Sprintf("PANIC recovered: %v", err), fields...)

				// 返回通用 500 错误
				// 注意：不要将 panic 的具体内容返回给客户端，防止敏感信息泄露
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"code":    1003,
					"message": "internal server error",
				})
			}
		}()
		c.Next() // 执行后续中间件和 Handler
	}
}
