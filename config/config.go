// Package config 负责应用配置的加载和管理。
// 使用 Viper 实现 YAML 文件读取 + 环境变量覆盖，所有零值字段自动填充默认值。
package config

import (
	"fmt"
	"log"

	"github.com/spf13/viper"
)

// Config 是应用的总配置结构体，映射 config.yaml 的全部字段。
type Config struct {
	Server    ServerConfig    `mapstructure:"server"`    // HTTP 服务配置
	MySQL     MySQLConfig     `mapstructure:"mysql"`     // 数据库配置
	Redis     RedisConfig     `mapstructure:"redis"`     // 缓存配置
	ShortLink ShortLinkConfig `mapstructure:"shortlink"` // 短链业务配置
	Log       LogConfig       `mapstructure:"log"`       // 日志配置
}

// ServerConfig HTTP 服务配置。
type ServerConfig struct {
	Port int    `mapstructure:"port"` // 监听端口，默认 8080
	Mode string `mapstructure:"mode"` // Gin 运行模式：debug / release / test
}

// MySQLConfig 数据库连接配置。
type MySQLConfig struct {
	Host         string `mapstructure:"host"`           // 数据库主机地址
	Port         int    `mapstructure:"port"`           // 数据库端口，默认 3306
	User         string `mapstructure:"user"`           // 数据库用户名
	Password     string `mapstructure:"password"`       // 数据库密码
	DBName       string `mapstructure:"dbname"`         // 数据库名称
	MaxOpenConns int    `mapstructure:"max_open_conns"` // 最大打开连接数
	MaxIdleConns int    `mapstructure:"max_idle_conns"` // 最大空闲连接数
}

// DSN 将 MySQL 配置拼接为 GORM 连接字符串（DSN 格式）。
// 固定使用 utf8mb4 字符集和 Local 时区。
func (m MySQLConfig) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		m.User, m.Password, m.Host, m.Port, m.DBName,
	)
}

// RedisConfig 缓存连接配置。
type RedisConfig struct {
	Addr     string `mapstructure:"addr"`      // Redis 地址，如 127.0.0.1:6379
	Password string `mapstructure:"password"`  // Redis 密码，空表示无密码
	DB       int    `mapstructure:"db"`        // Redis 数据库编号，默认 0
	PoolSize int    `mapstructure:"pool_size"` // 连接池大小
}

// ShortLinkConfig 短链业务参数配置。
type ShortLinkConfig struct {
	DefaultExpireDays int              `mapstructure:"default_expire_days"` // 默认过期天数，未指定时使用
	RedisCacheTTL     int              `mapstructure:"redis_cache_ttl"`     // Redis 缓存 TTL（秒）
	BloomFilter       BloomFilterConfig `mapstructure:"bloom_filter"`      // 布隆过滤器配置
	IDCounterKey      string           `mapstructure:"id_counter_key"`     // Redis 发号器 key
}

// BloomFilterConfig 布隆过滤器配置。
type BloomFilterConfig struct {
	UseRedis  bool    `mapstructure:"use_redis"`  // true=Redis Bitmap 模式，false=内存模式
	RedisKey  string  `mapstructure:"redis_key"`  // Redis Bitmap 的 key 名
	ErrorRate float64 `mapstructure:"error_rate"` // 误判率，默认 0.001（0.1%）
	Capacity  uint    `mapstructure:"capacity"`   // 预期元素数量
}

// LogConfig 日志配置。
type LogConfig struct {
	Level  string `mapstructure:"level"`  // 日志级别：debug / info / warn / error
	Format string `mapstructure:"format"` // 输出格式：json / console
}

// Load 从 YAML 文件加载配置，环境变量可覆盖（前缀 SL_），零值字段自动填充默认值。
//
// 环境变量映射规则：
//   配置路径 mysql.password → 环境变量 SL_MYSQL_PASSWORD
//   即：前缀 SL_ + 全大写 + 点号替换为下划线
//
// 默认值填充（仅对零值生效）：
//   Server.Port      → 8080
//   DefaultExpireDays → 30
//   RedisCacheTTL    → 3600
//   Bloom 误判率     → 0.001
//   Bloom 容量        → 1,000,000
//   ... 等
func Load(configPath string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(configPath)
	v.SetConfigType("yaml")

	// 环境变量覆盖：以 SL_ 为前缀
	// 例如 export SL_MYSQL_PASSWORD=xxx 会覆盖 mysql.password
	v.SetEnvPrefix("SL")
	v.AutomaticEnv()

	// 读取配置文件，文件不存在时只警告不报错（依赖默认值和环境变量）
	if err := v.ReadInConfig(); err != nil {
		log.Printf("WARN: cannot read config file %s: %v, using defaults and env", configPath, err)
	}

	// 反序列化到结构体
	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("unable to decode config: %w", err)
	}

	// ── 填充默认值（仅对未配置的零值生效）──────────────────
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}
	if cfg.Server.Mode == "" {
		cfg.Server.Mode = "debug"
	}
	if cfg.ShortLink.DefaultExpireDays == 0 {
		cfg.ShortLink.DefaultExpireDays = 30
	}
	if cfg.ShortLink.RedisCacheTTL == 0 {
		cfg.ShortLink.RedisCacheTTL = 3600
	}
	if cfg.ShortLink.IDCounterKey == "" {
		cfg.ShortLink.IDCounterKey = "short:id:counter"
	}
	if cfg.ShortLink.BloomFilter.RedisKey == "" {
		cfg.ShortLink.BloomFilter.RedisKey = "bloom:shortcodes"
	}
	if cfg.ShortLink.BloomFilter.ErrorRate == 0 {
		cfg.ShortLink.BloomFilter.ErrorRate = 0.001
	}
	if cfg.ShortLink.BloomFilter.Capacity == 0 {
		cfg.ShortLink.BloomFilter.Capacity = 1000000
	}
	if cfg.Log.Level == "" {
		cfg.Log.Level = "info"
	}
	if cfg.Log.Format == "" {
		cfg.Log.Format = "json"
	}
	if cfg.MySQL.MaxOpenConns == 0 {
		cfg.MySQL.MaxOpenConns = 100
	}
	if cfg.MySQL.MaxIdleConns == 0 {
		cfg.MySQL.MaxIdleConns = 10
	}
	if cfg.Redis.PoolSize == 0 {
		cfg.Redis.PoolSize = 50
	}

	return cfg, nil
}
