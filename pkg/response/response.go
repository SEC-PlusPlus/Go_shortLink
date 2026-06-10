// Package response 提供统一的 API 响应格式。
// 所有接口返回的 JSON 结构为 {"code": 业务码, "message": 描述, "data": 数据}。
//
// 业务码规则：
//   0     — 成功
//   1001  — 参数错误（400）
//   1002  — 资源冲突（409）
//   1003  — 服务内部错误（500）
//   1004  — 资源不存在（404）
//   1005  — 资源已过期（410）
package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Response 是统一 API 响应结构体。
// Data 字段在失败时省略（omitempty），不输出 null。
type Response struct {
	Code    int         `json:"code"`              // 业务状态码，0=成功
	Message string      `json:"message"`           // 可读的描述信息
	Data    interface{} `json:"data,omitempty"`    // 响应数据，仅成功时填充
}

// Success 返回成功响应（HTTP 200，业务码 0）。
// 调用示例：response.Success(c, gin.H{"short_url": "..."})
func Success(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, Response{
		Code:    0,
		Message: "ok",
		Data:    data,
	})
}

// Error 是通用错误响应构造函数，其余 5 个快捷函数都委托给它。
// 参数 httpStatus 是 HTTP 状态码，code 是业务错误码，message 是错误描述。
func Error(c *gin.Context, httpStatus int, code int, message string) {
	c.JSON(httpStatus, Response{
		Code:    code,
		Message: message,
	})
}

// BadRequest 返回 400 参数错误（业务码 1001）。
// 用于请求体 JSON 格式错误、参数校验不通过等场景。
func BadRequest(c *gin.Context, message string) {
	Error(c, http.StatusBadRequest, 1001, message)
}

// Conflict 返回 409 资源冲突（业务码 1002）。
// 用于自定义短码已被占用等场景。
func Conflict(c *gin.Context, message string) {
	Error(c, http.StatusConflict, 1002, message)
}

// InternalError 返回 500 服务内部错误（业务码 1003）。
// 用于数据库错误、Redis 错误等不暴露内部细节的通用错误。
func InternalError(c *gin.Context, message string) {
	Error(c, http.StatusInternalServerError, 1003, message)
}

// NotFound 返回 404 资源不存在（业务码 1004）。
// 用于短码不存在、格式不合法等场景。
func NotFound(c *gin.Context, message string) {
	Error(c, http.StatusNotFound, 1004, message)
}

// Gone 返回 410 资源已过期（业务码 1005）。
// 用于短链已过期的场景。
func Gone(c *gin.Context, message string) {
	Error(c, http.StatusGone, 1005, message)
}
