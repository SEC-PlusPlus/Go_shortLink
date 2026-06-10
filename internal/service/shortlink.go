package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"shortlink/config"
	"shortlink/internal/bloom"
	"shortlink/internal/dao"
	"shortlink/internal/model"
	"shortlink/pkg/base62"

	"github.com/go-redis/redis/v8"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
)

// ShortLinkService handles all business logic for short link operations.
// It orchestrates between Redis cache, MySQL database, base62 encoding, and bloom filter.
type ShortLinkService struct {
	dao    *dao.ShortLinkDAO
	rdb    *redis.Client
	bloom  *bloom.Filter
	cfg    *config.ShortLinkConfig
	logger *zap.Logger
	sg     singleflight.Group
}

// NewShortLinkService creates a new ShortLinkService with all dependencies.
func NewShortLinkService(
	dao *dao.ShortLinkDAO,
	rdb *redis.Client,
	bloomFilter *bloom.Filter,
	cfg *config.ShortLinkConfig,
	logger *zap.Logger,
) *ShortLinkService {
	return &ShortLinkService{
		dao:    dao,
		rdb:    rdb,
		bloom:  bloomFilter,
		cfg:    cfg,
		logger: logger,
	}
}

// ShortenRequest is the input for creating a short link.
type ShortenRequest struct {
	OriginalURL string `json:"original_url" validate:"required,url"`
	CustomCode  string `json:"custom_code" validate:"omitempty,min=4,max=10,alphanum"`
	ExpireDays  int    `json:"expire_days" validate:"omitempty,min=0,max=3650"`
}

// ShortenResponse is the output of a successful short link creation.
type ShortenResponse struct {
	ShortURL string     `json:"short_url"`
	ShortCode string    `json:"short_code"`
	ExpireAt  *time.Time `json:"expire_at,omitempty"`
}

// RedirectResult holds the data needed to perform an HTTP redirect.
type RedirectResult struct {
	OriginalURL string
	IsPermanent bool // true → 301, false → 302
}

// Shorten creates a short link from the given request.
//
// Flow:
// 1. If custom_code is provided, check for conflicts in Redis and MySQL.
// 2. If no custom_code, call Redis INCR on the ID counter and encode it with base62.
// 3. Calculate expire_at from expire_days (0 = permanent, nil for permanent).
// 4. Insert the record into MySQL via DAO.
// 5. Cache the mapping in Redis with TTL.
// 6. Add the short code to the bloom filter.
func (s *ShortLinkService) Shorten(ctx context.Context, req *ShortenRequest) (*ShortenResponse, error) {
	var code string

	if req.CustomCode != "" {
		// Validate base62 charset
		if !base62.IsValid(req.CustomCode) {
			return nil, fmt.Errorf("custom_code contains invalid characters")
		}

		// Check Redis cache first
		exists, err := s.rdb.Exists(ctx, s.cacheKey(req.CustomCode)).Result()
		if err != nil {
			s.logger.Error("redis check failed", zap.Error(err))
		}
		if exists > 0 {
			return nil, fmt.Errorf("short code already in use: %s", req.CustomCode)
		}

		// Check MySQL
		dbExists, err := s.dao.ExistsByShortCode(ctx, req.CustomCode)
		if err != nil {
			s.logger.Error("db check failed", zap.Error(err))
			return nil, fmt.Errorf("internal error checking code availability")
		}
		if dbExists {
			return nil, fmt.Errorf("short code already in use: %s", req.CustomCode)
		}

		code = req.CustomCode
	} else {
		// Generate from auto-increment counter via Redis INCR
		id, err := s.rdb.Incr(ctx, s.cfg.IDCounterKey).Result()
		if err != nil {
			s.logger.Error("redis incr failed", zap.Error(err))
			return nil, fmt.Errorf("failed to generate short code")
		}
		code = base62.Encode(uint64(id))
	}

	// Calculate expiration
	var expireAt *time.Time
	expireDays := req.ExpireDays
	if expireDays == 0 {
		expireDays = s.cfg.DefaultExpireDays
	}
	if expireDays > 0 {
		t := time.Now().Add(time.Duration(expireDays) * 24 * time.Hour)
		expireAt = &t
	}
	// expireDays == -1 means permanent (if we wanted to support that explicitly)

	// Create database record
	link := &model.ShortLink{
		ShortCode:   code,
		OriginalURL: req.OriginalURL,
		ExpireAt:    expireAt,
	}

	if err := s.dao.Create(ctx, link); err != nil {
		return nil, fmt.Errorf("failed to create short link: %w", err)
	}

	// Cache in Redis
	cacheTTL := s.cacheTTL(expireAt)
	if err := s.rdb.Set(ctx, s.cacheKey(code), req.OriginalURL, cacheTTL).Err(); err != nil {
		s.logger.Warn("failed to cache in redis", zap.String("code", code), zap.Error(err))
		// Non-fatal: continue (redirect will fall back to DB)
	}

	// Update bloom filter
	s.bloom.Add(code)

	s.logger.Info("short link created",
		zap.String("code", code),
		zap.String("original_url", req.OriginalURL),
	)

	return &ShortenResponse{
		ShortURL:  code, // handler will prepend domain
		ShortCode: code,
		ExpireAt:  expireAt,
	}, nil
}

// Redirect looks up the original URL for a short code and returns redirect info.
//
// Flow:
// 1. Check bloom filter — if the code is definitely absent, return not-found immediately.
// 2. Use singleflight to merge concurrent lookups for the same code.
// 3. Check Redis cache; on hit, return the original URL.
// 4. On miss, query MySQL; if found and not expired, cache in Redis, then return.
// 5. If not found or expired, return appropriate error.
func (s *ShortLinkService) Redirect(ctx context.Context, code string) (*RedirectResult, error) {
	// Step 1: Bloom filter check (fast reject)
	if !s.bloom.Test(code) {
		return nil, ErrNotFound
	}

	// Step 2-5: Singleflight-merged lookup
	val, err, _ := s.sg.Do(code, func() (interface{}, error) {
		return s.lookup(ctx, code)
	})

	if err != nil {
		return nil, err
	}

	result := val.(*RedirectResult)
	return result, nil
}

// lookup performs the actual cache-then-DB resolution for a short code.
// Called via singleflight to prevent cache stampede.
func (s *ShortLinkService) lookup(ctx context.Context, code string) (*RedirectResult, error) {
	// Step 3: Try Redis first
	cacheKey := s.cacheKey(code)
	originalURL, err := s.rdb.Get(ctx, cacheKey).Result()
	if err == nil {
		// Cache hit
		return &RedirectResult{
			OriginalURL: originalURL,
			IsPermanent: true, // default 301
		}, nil
	}
	if !errors.Is(err, redis.Nil) {
		// Redis error — log but continue to DB
		s.logger.Warn("redis get error", zap.String("code", code), zap.Error(err))
	}

	// Step 4: Query MySQL
	link, err := s.dao.GetByShortCode(ctx, code)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		s.logger.Error("db query error", zap.String("code", code), zap.Error(err))
		return nil, fmt.Errorf("internal error")
	}

	// Check expiration
	if link.IsExpired() {
		return nil, ErrExpired
	}

	// Step 5: Write back to Redis
	cacheTTL := s.cacheTTL(link.ExpireAt)
	if err := s.rdb.Set(ctx, cacheKey, link.OriginalURL, cacheTTL).Err(); err != nil {
		s.logger.Warn("failed to cache in redis on lookup", zap.String("code", code), zap.Error(err))
	}

	return &RedirectResult{
		OriginalURL: link.OriginalURL,
		IsPermanent: true,
	}, nil
}

// cacheKey returns the Redis key for a short code cache entry.
func (s *ShortLinkService) cacheKey(code string) string {
	return "shortlink:" + code
}

// cacheTTL calculates the TTL for a cached short link.
// If expireAt is nil (permanent), uses the configured default.
// Otherwise, sets TTL to time remaining until expiration.
func (s *ShortLinkService) cacheTTL(expireAt *time.Time) time.Duration {
	if expireAt == nil {
		// Permanent link — use configured default cache TTL
		return time.Duration(s.cfg.RedisCacheTTL) * time.Second
	}
	remaining := time.Until(*expireAt)
	if remaining <= 0 {
		return 1 * time.Second // minimum TTL for already-expired
	}
	if remaining > time.Duration(s.cfg.RedisCacheTTL)*time.Second {
		// Cap at configured max
		return time.Duration(s.cfg.RedisCacheTTL) * time.Second
	}
	return remaining
}

// RebuildBloomFilter rebuilds the bloom filter from the database.
// Called during startup and periodically.
func (s *ShortLinkService) RebuildBloomFilter(ctx context.Context) error {
	return s.bloom.Rebuild(ctx, s.dao.GetAllActiveShortCodes)
}

// StartBloomRebuildLoop starts periodic bloom filter rebuilding.
func (s *ShortLinkService) StartBloomRebuildLoop(ctx context.Context, interval time.Duration) {
	s.bloom.StartRebuildLoop(ctx, interval, s.dao.GetAllActiveShortCodes)
}

// Domain error sentinels
var (
	ErrNotFound = errors.New("short link not found")
	ErrExpired  = errors.New("short link has expired")
)
