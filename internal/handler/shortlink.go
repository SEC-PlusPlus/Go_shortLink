// Package handler 是 HTTP 请求处理层（Gin Handler）。
// 负责：解析请求参数 → 调用 Service 层 → 格式化 HTTP 响应。
// 不做业务逻辑，只做参数校验和响应映射。
package handler

import (
	"errors"
	"net/http"
	"strings"

	"shortlink/internal/middleware"
	"shortlink/internal/service"
	"shortlink/pkg/base62"
	"shortlink/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"go.uber.org/zap"
)

// ShortLinkHandler 是短链 API 的 Gin 处理器。
// 持有 Service 层引用、日志、校验器和短链 URL 前缀。
type ShortLinkHandler struct {
	svc       *service.ShortLinkService // 业务逻辑层
	logger    *zap.Logger               // 日志
	validator *validator.Validate       // 参数校验器
	baseURL   string                    // 短链 URL 前缀，如 http://localhost:8080
}

// NewShortLinkHandler 构造函数，依赖注入 Service、Logger 和 baseURL。
// 调用者：main()
func NewShortLinkHandler(svc *service.ShortLinkService, logger *zap.Logger, baseURL string) *ShortLinkHandler {
	return &ShortLinkHandler{
		svc:       svc,
		logger:    logger,
		validator: validator.New(),
		baseURL:   baseURL,
	}
}

// Shorten 处理 POST /api/shorten 请求。
//
// 请求体示例：
//   {"original_url": "https://example.com/long", "custom_code": "mycode", "expire_days": 30}
//
// 处理流程：
//   1. ShouldBindJSON → 将 JSON 解析到 ShortenRequest
//   2. validator.Struct → 执行 Tag 校验（required, url, alphanum, min, max）
//   3. 校验失败 → formatValidationError() 格式化错误消息 → 返回 400
//   4. service.Shorten() → 调用业务层创建短链
//   5. 业务错误 → 根据错误类型返回 409 或 500
//   6. 成功 → 拼接完整短链 URL → 返回 200
func (h *ShortLinkHandler) Shorten(c *gin.Context) {
	// 获取 trace_id 用于日志关联
	traceID := middleware.GetTraceID(c)

	// ── 1. JSON 绑定 ─────────────────────────────────────
	var req service.ShortenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid JSON: "+err.Error())
		return
	}

	// ── 2. 参数 Tag 校验 ──────────────────────────────────
	if err := h.validator.Struct(req); err != nil {
		var validationErrors validator.ValidationErrors
		if errors.As(err, &validationErrors) {
			// 构建字段级别的错误信息列表
			msgs := make([]string, 0, len(validationErrors))
			for _, ve := range validationErrors {
				msgs = append(msgs, formatValidationError(ve))
			}
			response.BadRequest(c, strings.Join(msgs, "; "))
			return
		}
		response.BadRequest(c, err.Error())
		return
	}

	// ── 3. 调用 Service 层 ────────────────────────────────
	resp, err := h.svc.Shorten(c.Request.Context(), &req)
	if err != nil {
		h.logger.Error("shorten failed",
			zap.String("trace_id", traceID),
			zap.Error(err),
		)

		msg := err.Error()
		// 根据错误消息判断类型（"already in use" → 409 冲突）
		if strings.Contains(msg, "already in use") {
			response.Conflict(c, msg)
			return
		}
		response.InternalError(c, "failed to create short link")
		return
	}

	// ── 4. 构造完整短链 URL ──────────────────────────────
	// Service 返回的 ShortURL 只含短码，此处拼接域名前缀
	resp.ShortURL = h.baseURL + "/" + resp.ShortCode

	response.Success(c, resp)
}

// Redirect 处理 GET /:code 请求。
//
// 处理流程：
//   1. 提取 URL 路径参数 code
//   2. base62.IsValid() → 校验短码格式
//   3. service.Redirect() → 调用业务层查找原始 URL
//   4. 错误分发：
//      ErrNotFound → 404
//      ErrExpired  → 410
//      其他        → 500
//   5. 成功 → c.Redirect(301 或 302, originalURL) → 执行 HTTP 重定向
func (h *ShortLinkHandler) Redirect(c *gin.Context) {
	traceID := middleware.GetTraceID(c)
	code := c.Param("code") // 从 URL 路径提取短码

	// ── 1. 校验短码格式 ──────────────────────────────────
	// 空字符串、过长、含非法字符 → 直接返回 404
	if code == "" || len(code) > 20 || !base62.IsValid(code) {
		response.NotFound(c, "invalid short code format")
		return
	}

	// ── 2. 调用 Service 层 ────────────────────────────────
	result, err := h.svc.Redirect(c.Request.Context(), code)
	if err != nil {
		// 根据领域错误类型映射 HTTP 状态码
		if errors.Is(err, service.ErrNotFound) {
			response.NotFound(c, "short link not found")
			return
		}
		if errors.Is(err, service.ErrExpired) {
			response.Gone(c, "short link has expired")
			return
		}

		// 其他未知错误 → 记录详细日志 + 返回通用错误
		h.logger.Error("redirect failed",
			zap.String("trace_id", traceID),
			zap.String("code", code),
			zap.Error(err),
		)
		response.InternalError(c, "internal error")
		return
	}

	// ── 3. 执行重定向 ────────────────────────────────────
	status := http.StatusMovedPermanently // 301 永久重定向
	if !result.IsPermanent {
		status = http.StatusFound // 302 临时重定向
	}

	h.logger.Info("redirect",
		zap.String("trace_id", traceID),
		zap.String("code", code),
		zap.String("to", result.OriginalURL),
		zap.Int("status", status),
	)

	c.Redirect(status, result.OriginalURL)
}

// formatValidationError 将 validator 的 Tag 错误转换为人类可读的中文消息。
// 这是一个本文件内私有的辅助函数。
//
// 转换规则：
//   required → "OriginalURL is required"
//   url      → "OriginalURL must be a valid URL"
//   min      → "CustomCode must be at least 4 characters"
//   max      → "CustomCode must be at most 10 characters"
//   alphanum → "CustomCode must contain only letters and numbers"
func formatValidationError(ve validator.FieldError) string {
	switch ve.Tag() {
	case "required":
		return ve.Field() + " is required"
	case "url":
		return ve.Field() + " must be a valid URL"
	case "min":
		return ve.Field() + " must be at least " + ve.Param() + " characters"
	case "max":
		return ve.Field() + " must be at most " + ve.Param() + " characters"
	case "alphanum":
		return ve.Field() + " must contain only letters and numbers"
	default:
		return ve.Field() + " failed validation: " + ve.Tag()
	}
}
