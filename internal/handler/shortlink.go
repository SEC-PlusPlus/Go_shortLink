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

// ShortLinkHandler handles HTTP requests for the short link API.
// It parses request parameters, calls the service layer, and formats responses.
type ShortLinkHandler struct {
	svc       *service.ShortLinkService
	logger    *zap.Logger
	validator *validator.Validate
	baseURL   string // e.g., "http://localhost:8080"
}

// NewShortLinkHandler creates a new ShortLinkHandler.
func NewShortLinkHandler(svc *service.ShortLinkService, logger *zap.Logger, baseURL string) *ShortLinkHandler {
	return &ShortLinkHandler{
		svc:       svc,
		logger:    logger,
		validator: validator.New(),
		baseURL:   baseURL,
	}
}

// Shorten handles POST /api/shorten requests.
//
// It validates the request body by:
// 1. Binding JSON to ShortenRequest struct.
// 2. Running validator tags (required, url, alphanum, min/max).
// 3. Returning 1001 error code with field-level details on validation failure.
//
// On success, it constructs the full short URL and returns it.
func (h *ShortLinkHandler) Shorten(c *gin.Context) {
	traceID := middleware.GetTraceID(c)

	var req service.ShortenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid JSON: "+err.Error())
		return
	}

	// Validate struct tags
	if err := h.validator.Struct(req); err != nil {
		var validationErrors validator.ValidationErrors
		if errors.As(err, &validationErrors) {
			// Build field-level error messages
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

	resp, err := h.svc.Shorten(c.Request.Context(), &req)
	if err != nil {
		h.logger.Error("shorten failed",
			zap.String("trace_id", traceID),
			zap.Error(err),
		)

		msg := err.Error()
		if strings.Contains(msg, "already in use") {
			response.Conflict(c, msg)
			return
		}
		response.InternalError(c, "failed to create short link")
		return
	}

	// Construct the full short URL
	resp.ShortURL = h.baseURL + "/" + resp.ShortCode

	response.Success(c, resp)
}

// Redirect handles GET /:code requests.
//
// It validates the short code format, then calls service.Redirect.
// On success, returns a 301 (permanent) or 302 (temporary) redirect.
// On failure, returns a 404 (not found) or 410 (expired) JSON response.
func (h *ShortLinkHandler) Redirect(c *gin.Context) {
	traceID := middleware.GetTraceID(c)
	code := c.Param("code")

	// Validate code format
	if code == "" || len(code) > 20 || !base62.IsValid(code) {
		response.NotFound(c, "invalid short code format")
		return
	}

	result, err := h.svc.Redirect(c.Request.Context(), code)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			response.NotFound(c, "short link not found")
			return
		}
		if errors.Is(err, service.ErrExpired) {
			response.Gone(c, "short link has expired")
			return
		}

		h.logger.Error("redirect failed",
			zap.String("trace_id", traceID),
			zap.String("code", code),
			zap.Error(err),
		)
		response.InternalError(c, "internal error")
		return
	}

	// Perform redirect
	status := http.StatusMovedPermanently // 301
	if !result.IsPermanent {
		status = http.StatusFound // 302
	}

	h.logger.Info("redirect",
		zap.String("trace_id", traceID),
		zap.String("code", code),
		zap.String("to", result.OriginalURL),
		zap.Int("status", status),
	)

	c.Redirect(status, result.OriginalURL)
}

// formatValidationError produces a human-readable error message for a validation failure.
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
