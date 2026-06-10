package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Logger 是一个 Gin 中间件，使用 Zap 记录每个请求的完整信息。
//
// 记录时机：请求处理完成后（c.Next() 返回之后）
//
// 记录字段：
//   status    — HTTP 状态码（200, 404, 500...）
//   method    — HTTP 方法（GET, POST...）
//   path      — 请求路径（/api/shorten）
//   query     — URL 查询参数
//   ip        — 客户端 IP
//   latency   — 请求处理耗时
//   body_size — 响应体大小（字节）
//   trace_id  — 链路追踪 ID（从 Context 读取）
//
// 日志级别策略：
//   status >= 500 → Error（服务端错误，需要告警）
//   status >= 400 → Warn （客户端错误，可关注）
//   其他          → Info （正常请求）
//
// 参数：logger — 由 main() 初始化的全局 Zap Logger
func Logger(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()     // 记录请求开始时间
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		// 执行后续中间件和 Handler（阻塞直到请求处理完毕）
		c.Next()

		// 计算请求总耗时
		latency := time.Since(start)

		// 构建日志字段（所有请求共有的基础字段）
		fields := []zap.Field{
			zap.Int("status", c.Writer.Status()),
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.String("query", query),
			zap.String("ip", c.ClientIP()),
			zap.Duration("latency", latency),
			zap.Int("body_size", c.Writer.Size()),
		}

		// 附加 trace_id（从 TraceID 中间件注入的 Context 中读取）
		if traceID := GetTraceID(c); traceID != "" {
			fields = append(fields, zap.String("trace_id", traceID))
		}

		// 根据状态码选择日志级别
		status := c.Writer.Status()
		switch {
		case status >= 500:
			logger.Error("request completed", fields...)
		case status >= 400:
			logger.Warn("request completed", fields...)
		default:
			logger.Info("request completed", fields...)
		}
	}
}
