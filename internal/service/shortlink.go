// Package service 是短链接服务的核心业务逻辑层。
// 负责编排 Redis 缓存、MySQL 数据库、布隆过滤器、Base62 编码之间的调用流程。
//
// 核心设计：
//   1. 缓存策略：Cache-Aside（旁路缓存）— 查询先 Redis 后 MySQL，写入同时更新两层
//   2. 防击穿：singleflight — 同一短码的并发请求合并为一次数据库查询
//   3. 防穿透：布隆过滤器 — 快速否决不存在的短码，避免无效请求打到数据库
//   4. 发号器：Redis INCR — 原子自增生成唯一 ID，Base62 编码为短码
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

// ShortLinkService 是短链业务逻辑的核心结构体。
// 持有所有外部依赖的引用，通过依赖注入在 main() 中初始化。
type ShortLinkService struct {
	dao    *dao.ShortLinkDAO        // 数据库操作
	rdb    *redis.Client            // Redis 客户端（缓存 + 发号器）
	bloom  *bloom.Filter            // 布隆过滤器（防穿透）
	cfg    *config.ShortLinkConfig  // 短链相关配置
	logger *zap.Logger              // 结构化日志
	sg     singleflight.Group       // 请求合并组（防击穿）
}

// NewShortLinkService 构造函数，通过依赖注入组装 Service。
// 调用者：main() — 唯一的组装点
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

// ShortenRequest 是短链生成请求的数据传输对象（DTO）。
// validator tag 用于 Gin 的参数校验。
type ShortenRequest struct {
	OriginalURL string `json:"original_url" validate:"required,url"`                  // 原始长 URL，必填，需为合法 URL
	CustomCode  string `json:"custom_code" validate:"omitempty,min=4,max=10,alphanum"` // 自定义短码，可选，4-10位字母数字
	ExpireDays  int    `json:"expire_days" validate:"omitempty,min=0,max=3650"`       // 过期天数，0=使用默认，负值=永久
}

// ShortenResponse 是短链生成成功后的响应 DTO。
type ShortenResponse struct {
	ShortURL  string     `json:"short_url"`           // 完整短链 URL（如 http://localhost:8080/abc123）
	ShortCode string     `json:"short_code"`          // 短码
	ExpireAt  *time.Time `json:"expire_at,omitempty"` // 过期时间，null 表示永久
}

// RedirectResult 是重定向查找的结果。
// IsPermanent 决定 HTTP 状态码：true→301，false→302。
type RedirectResult struct {
	OriginalURL string // 原始长 URL
	IsPermanent bool   // true=301 永久重定向，false=302 临时重定向
}

// Shorten 创建短链接（核心方法）。
//
// 完整业务流程：
//
//   1. 确定短码来源
//      ├─ 用户提供了 custom_code
//      │    ├─ base62.IsValid() → 校验字符集是否合法
//      │    ├─ Redis EXISTS → 检查缓存中是否已存在
//      │    ├─ DAO.ExistsByShortCode() → 检查数据库中是否已存在
//      │    └─ 无冲突 → 直接使用该 custom_code
//      │
//      └─ 用户未提供 custom_code
//           ├─ Redis INCR id_counter → 原子自增获取唯一数字 ID
//           └─ base62.Encode(id) → 将数字编码为短码
//
//   2. 计算过期时间
//      ├─ ExpireDays > 0 → now + N days
//      ├─ ExpireDays = 0 → 使用配置的默认过期天数
//      └─ ExpireDays < 0 → nil（永久有效）
//
//   3. DAO.Create() → 写入 MySQL（唯一索引保证短码唯一）
//   4. Redis SET → 将映射写入缓存（TTL 与过期时间对齐）
//   5. bloom.Add() → 将短码加入布隆过滤器
//   6. 记录日志 → 返回 ShortenResponse
func (s *ShortLinkService) Shorten(ctx context.Context, req *ShortenRequest) (*ShortenResponse, error) {
	var code string

	// ── 第1步：确定短码 ──────────────────────────────────
	if req.CustomCode != "" {
		// 用户自定义短码

		// 校验字符集必须在 Base62 范围内
		if !base62.IsValid(req.CustomCode) {
			return nil, fmt.Errorf("custom_code contains invalid characters")
		}

		// 先查 Redis 缓存（快速路径）
		exists, err := s.rdb.Exists(ctx, s.cacheKey(req.CustomCode)).Result()
		if err != nil {
			// Redis 异常不阻塞业务，记录日志后继续查数据库
			s.logger.Error("redis check failed", zap.Error(err))
		}
		if exists > 0 {
			return nil, fmt.Errorf("short code already in use: %s", req.CustomCode)
		}

		// 再查 MySQL（包括已过期/已删除记录，防止重复使用）
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
		// 自动生成短码：Redis INCR 发号器 + Base62 编码
		id, err := s.rdb.Incr(ctx, s.cfg.IDCounterKey).Result()
		if err != nil {
			s.logger.Error("redis incr failed", zap.Error(err))
			return nil, fmt.Errorf("failed to generate short code")
		}
		code = base62.Encode(uint64(id))
	}

	// ── 第2步：计算过期时间 ──────────────────────────────
	var expireAt *time.Time
	expireDays := req.ExpireDays
	if expireDays == 0 {
		expireDays = s.cfg.DefaultExpireDays // 使用配置的默认值（30天）
	}
	if expireDays > 0 {
		t := time.Now().Add(time.Duration(expireDays) * 24 * time.Hour)
		expireAt = &t
	}
	// expireDays < 0 时 expireAt 保持 nil → 永久有效

	// ── 第3步：写入数据库 ────────────────────────────────
	link := &model.ShortLink{
		ShortCode:   code,
		OriginalURL: req.OriginalURL,
		ExpireAt:    expireAt,
	}

	if err := s.dao.Create(ctx, link); err != nil {
		return nil, fmt.Errorf("failed to create short link: %w", err)
	}

	// ── 第4步：写入 Redis 缓存 ───────────────────────────
	// 缓存失败不阻塞业务（重定向时回源查数据库即可）
	cacheTTL := s.cacheTTL(expireAt)
	if err := s.rdb.Set(ctx, s.cacheKey(code), req.OriginalURL, cacheTTL).Err(); err != nil {
		s.logger.Warn("failed to cache in redis", zap.String("code", code), zap.Error(err))
	}

	// ── 第5步：更新布隆过滤器 ────────────────────────────
	s.bloom.Add(code)

	// ── 第6步：记录日志并返回 ────────────────────────────
	s.logger.Info("short link created",
		zap.String("code", code),
		zap.String("original_url", req.OriginalURL),
	)

	return &ShortenResponse{
		ShortURL:  code, // 完整 URL 由 Handler 层拼接 baseURL
		ShortCode: code,
		ExpireAt:  expireAt,
	}, nil
}

// Redirect 执行重定向查找（核心方法）。
//
// 防护策略：
//
//   1. 防穿透：布隆过滤器快速否决
//      └─ bloom.Test(code) == false → 直接返回 ErrNotFound，无需查数据库
//
//   2. 防击穿：singleflight 请求合并
//      └─ 同一短码的并发请求合并为一次 lookup() 调用
//         例：1000 个并发请求 /abc123 → 只产生 1 次数据库查询
//
//   3. 缓存加速：Cache-Aside 策略
//      └─ Redis 命中 → 直接返回（无需查数据库）
//      └─ Redis 未命中 → 查 MySQL → 回写 Redis → 返回
func (s *ShortLinkService) Redirect(ctx context.Context, code string) (*RedirectResult, error) {
	// 第1步：布隆过滤器快速否决（内存操作，微秒级）
	if !s.bloom.Test(code) {
		return nil, ErrNotFound // 绝对不存在，直接返回
	}

	// 第2-5步：singleflight 包裹的查找逻辑
	// 同一 code 的并发请求只会执行一次 lookup()，其他请求共享结果
	val, err, _ := s.sg.Do(code, func() (interface{}, error) {
		return s.lookup(ctx, code)
	})

	if err != nil {
		return nil, err
	}

	result := val.(*RedirectResult)
	return result, nil
}

// lookup 执行实际的缓存→数据库查询（被 singleflight 包裹）。
// 方法为私有（小写开头），只通过 singleflight.Do() 间接调用。
//
// 查询顺序：Redis 缓存 → MySQL 数据库 → 回写 Redis
func (s *ShortLinkService) lookup(ctx context.Context, code string) (*RedirectResult, error) {
	cacheKey := s.cacheKey(code)

	// ── 第3步：尝试 Redis 缓存 ───────────────────────────
	originalURL, err := s.rdb.Get(ctx, cacheKey).Result()
	if err == nil {
		// 缓存命中，直接返回（最常见路径）
		return &RedirectResult{
			OriginalURL: originalURL,
			IsPermanent: true, // 默认 301 永久重定向
		}, nil
	}
	// redis.Nil 表示 key 不存在，属于正常情况，继续查数据库
	// 其他错误（连接失败等）记录日志后也继续查数据库，降级处理
	if !errors.Is(err, redis.Nil) {
		s.logger.Warn("redis get error", zap.String("code", code), zap.Error(err))
	}

	// ── 第4步：查询 MySQL 数据库 ─────────────────────────
	link, err := s.dao.GetByShortCode(ctx, code)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound // 数据库中也无记录
		}
		s.logger.Error("db query error", zap.String("code", code), zap.Error(err))
		return nil, fmt.Errorf("internal error")
	}

	// ── 第5步：过期检查 ──────────────────────────────────
	if link.IsExpired() {
		return nil, ErrExpired // 已过期
	}

	// ── 第6步：回写 Redis 缓存 ───────────────────────────
	// 缓存回写失败不阻塞业务（下次请求重新查库即可）
	cacheTTL := s.cacheTTL(link.ExpireAt)
	if err := s.rdb.Set(ctx, cacheKey, link.OriginalURL, cacheTTL).Err(); err != nil {
		s.logger.Warn("failed to cache in redis on lookup", zap.String("code", code), zap.Error(err))
	}

	return &RedirectResult{
		OriginalURL: link.OriginalURL,
		IsPermanent: true,
	}, nil
}

// cacheKey 生成 Redis 缓存 key。
// 格式：shortlink:{code}
// 示例：shortlink:abc123
func (s *ShortLinkService) cacheKey(code string) string {
	return "shortlink:" + code
}

// cacheTTL 计算缓存 TTL（过期时间）。
//
// 策略：
//   expireAt == nil（永久）            → 使用配置的默认 TTL（如 3600 秒）
//   剩余时间 > 配置 TTL                 → 封顶为配置 TTL（避免缓存过久）
//   0 < 剩余时间 ≤ 配置 TTL            → 使用剩余时间（与数据过期同步）
//   剩余时间 ≤ 0（已过期但未清理）       → 返回 1 秒（最小 TTL）
//
// 设计意图：缓存永远不比数据活得更久，避免返回已过期的重定向。
func (s *ShortLinkService) cacheTTL(expireAt *time.Time) time.Duration {
	if expireAt == nil {
		// 永久链接：使用默认 TTL，到期后自动刷新
		return time.Duration(s.cfg.RedisCacheTTL) * time.Second
	}
	remaining := time.Until(*expireAt)
	if remaining <= 0 {
		return 1 * time.Second // 最小 TTL，避免负值
	}
	if remaining > time.Duration(s.cfg.RedisCacheTTL)*time.Second {
		// 剩余时间超过配置上限，封顶处理
		return time.Duration(s.cfg.RedisCacheTTL) * time.Second
	}
	// TTL 与数据过期时间一致
	return remaining
}

// RebuildBloomFilter 代理方法，从数据库重建布隆过滤器。
// 调用者：main() — 启动时调用一次
func (s *ShortLinkService) RebuildBloomFilter(ctx context.Context) error {
	return s.bloom.Rebuild(ctx, s.dao.GetAllActiveShortCodes)
}

// StartBloomRebuildLoop 代理方法，启动布隆过滤器的定时重建协程。
// 调用者：main() — 启动后台 goroutine
func (s *ShortLinkService) StartBloomRebuildLoop(ctx context.Context, interval time.Duration) {
	s.bloom.StartRebuildLoop(ctx, interval, s.dao.GetAllActiveShortCodes)
}

// ── 领域错误哨兵值 ──────────────────────────────────────
// Service 层返回这些错误，Handler 层通过 errors.Is() 判断并映射到 HTTP 状态码。
var (
	ErrNotFound = errors.New("short link not found") // → 404
	ErrExpired  = errors.New("short link has expired") // → 410
)
