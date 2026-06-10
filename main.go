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
	// Parse command line flags
	configPath := flag.String("config", "config/config.yaml", "path to configuration file")
	flag.Parse()

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	// Initialize logger
	logger := initLogger(cfg)
	defer logger.Sync()

	logger.Info("starting shortlink service",
		zap.Int("port", cfg.Server.Port),
		zap.String("mode", cfg.Server.Mode),
	)

	// Initialize MySQL
	db, err := initMySQL(cfg)
	if err != nil {
		logger.Fatal("failed to initialize MySQL", zap.Error(err))
	}
	logger.Info("MySQL connected")

	// Auto-migrate the model
	if err := db.AutoMigrate(&model.ShortLink{}); err != nil {
		logger.Fatal("failed to auto-migrate", zap.Error(err))
	}
	logger.Info("database migration completed")

	// Initialize Redis
	rdb := initRedis(cfg)
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		logger.Fatal("failed to connect to Redis", zap.Error(err))
	}
	logger.Info("Redis connected")

	// Initialize bloom filter
	bloomFilter := bloom.NewFilter(
		cfg.ShortLink.BloomFilter.Capacity,
		cfg.ShortLink.BloomFilter.ErrorRate,
		cfg.ShortLink.BloomFilter.UseRedis,
		cfg.ShortLink.BloomFilter.RedisKey,
	)

	// Initialize DAO
	shortLinkDAO := dao.NewShortLinkDAO(db)

	// Initialize service
	shortLinkSvc := service.NewShortLinkService(
		shortLinkDAO,
		rdb,
		bloomFilter,
		&cfg.ShortLink,
		logger,
	)

	// Build bloom filter from existing data on startup
	ctx := context.Background()
	if err := shortLinkSvc.RebuildBloomFilter(ctx); err != nil {
		logger.Warn("failed to rebuild bloom filter on startup", zap.Error(err))
	} else {
		logger.Info("bloom filter initialized from database")
	}

	// Start periodic bloom filter rebuild (every 1 hour)
	shortLinkSvc.StartBloomRebuildLoop(ctx, 1*time.Hour)

	// Build base URL
	baseURL := fmt.Sprintf("http://localhost:%d", cfg.Server.Port)

	// Initialize handler
	shortLinkHandler := handler.NewShortLinkHandler(shortLinkSvc, logger, baseURL)

	// Setup Gin
	gin.SetMode(cfg.Server.Mode)
	router := gin.New()

	// Register middleware
	router.Use(middleware.TraceID())
	router.Use(middleware.Logger(logger))
	router.Use(middleware.Recovery(logger))

	// Register routes
	router.POST("/api/shorten", shortLinkHandler.Shorten)
	router.GET("/:code", shortLinkHandler.Redirect)

	// Health check endpoint
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Start server
	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: router,
	}

	// Graceful shutdown
	go func() {
		logger.Info("server starting", zap.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("server failed", zap.Error(err))
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down server...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("server forced to shutdown", zap.Error(err))
	}

	logger.Info("server exited")
}

// initLogger creates a Zap logger based on the configuration.
func initLogger(cfg *config.Config) *zap.Logger {
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

	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "time"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	var encoder zapcore.Encoder
	if cfg.Log.Format == "json" {
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	} else {
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	}

	core := zapcore.NewCore(encoder, zapcore.AddSync(os.Stdout), level)
	return zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))
}

// initMySQL creates a GORM database connection and configures the connection pool.
func initMySQL(cfg *config.Config) (*gorm.DB, error) {
	gormCfg := &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	}

	db, err := gorm.Open(mysql.Open(cfg.MySQL.DSN()), gormCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MySQL: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	sqlDB.SetMaxOpenConns(cfg.MySQL.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MySQL.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(time.Hour)

	return db, nil
}

// initRedis creates a Redis client based on the configuration.
func initRedis(cfg *config.Config) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
		PoolSize: cfg.Redis.PoolSize,
	})
}
