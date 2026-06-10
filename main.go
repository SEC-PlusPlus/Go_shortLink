// Package main 是短链接服务的入口，负责初始化所有组件并启动 HTTP 服务。
// 启动流程：加载配置 → 初始化日志/数据库/缓存/布隆 → 注入依赖 → 注册路由 → 启动服务。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"shortlink/config"
	"shortlink/internal/bloom"
	"shortlink/internal/dao"
	"shortlink/internal/handler"
	"shortlink/internal/middleware"
	"shortlink/internal/model"
	"shortlink/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func main() {
	// ── 1. 解析命令行参数 ──────────────────────────────────
	// 注册 -config 参数，用于指定配置文件路径，默认值为 config/config.yaml
	configPath := flag.String("config", "config/config.yaml", "path to configuration file")
	flag.Parse()

	// ── 2. 加载配置 ────────────────────────────────────────
	// 从 YAML 文件加载配置，支持环境变量覆盖（前缀 SL_）
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	// ── 3. 初始化日志 ──────────────────────────────────────
	// 基于配置创建 Zap 结构化日志，支持 JSON/Console 两种格式
	logger := initLogger(cfg)
	defer logger.Sync() // 确保退出前刷新所有日志缓冲

	logger.Info("starting shortlink service",
		zap.Int("port", cfg.Server.Port),
		zap.String("mode", cfg.Server.Mode),
	)

	// ── 4. 初始化数据库 ────────────────────────────────────
	// 连接 MySQL，配置连接池参数，并自动迁移表结构
	db, err := initMySQL(cfg)
	if err != nil {
		logger.Fatal("failed to initialize MySQL", zap.Error(err))
	}
	logger.Info("MySQL connected")

	// AutoMigrate 自动创建/更新 short_links 表，无需手动执行建表 SQL
	if err := db.AutoMigrate(&model.ShortLink{}); err != nil {
		logger.Fatal("failed to auto-migrate", zap.Error(err))
	}
	logger.Info("database migration completed")

	// ── 5. 初始化 Redis ────────────────────────────────────
	// 连接 Redis，用于缓存、发号器、布隆过滤器 Bitmap
	rdb := initRedis(cfg)
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		logger.Fatal("failed to connect to Redis", zap.Error(err))
	}
	logger.Info("Redis connected")

	// ── 6. 初始化布隆过滤器 ────────────────────────────────
	// 基于配置选择内存模式或 Redis Bitmap 模式
	bloomFilter := bloom.NewFilter(
		cfg.ShortLink.BloomFilter.Capacity,
		cfg.ShortLink.BloomFilter.ErrorRate,
		cfg.ShortLink.BloomFilter.UseRedis,
		cfg.ShortLink.BloomFilter.RedisKey,
	)

	// ── 7. 初始化 DAO（数据访问层）─────────────────────────
	// 封装 GORM 操作，提供类型安全的数据库查询方法
	shortLinkDAO := dao.NewShortLinkDAO(db)

	// ── 8. 初始化 Service（业务逻辑层）─────────────────────
	// 依赖注入：将 DAO、Redis、布隆、配置、日志全部注入 Service
	shortLinkSvc := service.NewShortLinkService(
		shortLinkDAO,
		rdb,
		bloomFilter,
		&cfg.ShortLink,
		logger,
	)

	// ── 9. 启动时重建布隆过滤器 ────────────────────────────
	// 从数据库加载所有活跃短码到布隆过滤器，保证启动后即可过滤
	ctx := context.Background()
	if err := shortLinkSvc.RebuildBloomFilter(ctx); err != nil {
		logger.Warn("failed to rebuild bloom filter on startup", zap.Error(err))
	} else {
		logger.Info("bloom filter initialized from database")
	}

	// ── 10. 启动布隆过滤器定时重建 ─────────────────────────
	// 每小时从数据库重新加载，清理已过期条目，保持过滤器准确性
	shortLinkSvc.StartBloomRebuildLoop(ctx, 1*time.Hour)

	// ── 11. 初始化 Handler（HTTP 处理层）───────────────────
	// 构建短链的完整前缀 URL（如 http://localhost:8080）
	baseURL := fmt.Sprintf("http://localhost:%d", cfg.Server.Port)
	shortLinkHandler := handler.NewShortLinkHandler(shortLinkSvc, logger, baseURL)

	// ── 12. 配置 Gin 路由 ──────────────────────────────────
	gin.SetMode(cfg.Server.Mode)
	router := gin.New()

	// 注册中间件链（按顺序执行）：
	//   TraceID → 注入请求追踪 ID
	//   Logger  → 记录请求方法、路径、状态码、耗时
	//   Recovery → 捕获 panic，防止进程崩溃
	router.Use(middleware.TraceID())
	router.Use(middleware.Logger(logger))
	router.Use(middleware.Recovery(logger))

	// 注册业务路由
	router.POST("/api/shorten", shortLinkHandler.Shorten) // 短链生成
	router.GET("/:code", shortLinkHandler.Redirect)       // 短链重定向
	router.GET("/health", func(c *gin.Context) {          // 健康检查
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// ── 13. 启动 HTTP 服务（支持优雅关闭）─────────────────
	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: router,
	}

	// 在 goroutine 中启动服务，主线程监听退出信号
	go func() {
		logger.Info("server starting", zap.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("server failed", zap.Error(err))
		}
	}()

	// ── 14. 等待退出信号 ──────────────────────────────────
	// 监听 SIGINT（Ctrl+C）和 SIGTERM（kill），收到后执行优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit // 阻塞，直到收到信号

	logger.Info("shutting down server...")

	// 5 秒超时的优雅关闭：不再接受新请求，等待现有请求处理完毕
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("server forced to shutdown", zap.Error(err))
	}

	logger.Info("server exited")
}

// initLogger 基于配置创建 Zap 日志实例。
// 支持 debug/warn/error/info 四个级别，JSON/Console 两种输出格式。
// Error 级别自动附带调用栈信息。
func initLogger(cfg *config.Config) *zap.Logger {
	// 解析日志级别
	var level zapcore.Level
	switch cfg.Log.Level {
	case "debug":
		level = zapcore.DebugLevel
	case "warn":
		level = zapcore.WarnLevel
	case "error":
		level = zapcore.ErrorLevel
	default:
		level = zapcore.InfoLevel
	}

	// 编码器配置：时间用 ISO8601 格式
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "time"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	// 根据配置选择 JSON 或 Console 编码器
	var encoder zapcore.Encoder
	if cfg.Log.Format == "json" {
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	} else {
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	}

	core := zapcore.NewCore(encoder, zapcore.AddSync(os.Stdout), level)
	return zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))
}

// initMySQL 创建 GORM 数据库连接并配置连接池参数。
// 返回的 *gorm.DB 用于所有数据库操作。
func initMySQL(cfg *config.Config) (*gorm.DB, error) {
	// GORM 配置：SQL 日志级别设为 Warn（只记录慢查询和错误）
	gormCfg := &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	}

	// 使用 MySQL 驱动连接数据库
	db, err := gorm.Open(mysql.Open(cfg.MySQL.DSN()), gormCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MySQL: %w", err)
	}

	// 获取底层 sql.DB 实例，配置连接池
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	sqlDB.SetMaxOpenConns(cfg.MySQL.MaxOpenConns)     // 最大打开连接数
	sqlDB.SetMaxIdleConns(cfg.MySQL.MaxIdleConns)     // 最大空闲连接数
	sqlDB.SetConnMaxLifetime(time.Hour)                // 连接最大存活时间

	return db, nil
}

// initRedis 基于配置创建 Redis 客户端。
// 返回的 *redis.Client 用于缓存读写、发号器 INCR 等操作。
func initRedis(cfg *config.Config) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
		PoolSize: cfg.Redis.PoolSize,
	})
}
