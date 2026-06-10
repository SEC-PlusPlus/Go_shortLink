package config

import (
	"fmt"
	"log"

	"github.com/spf13/viper"
)

// Config holds all configuration for the application.
type Config struct {
	Server    ServerConfig    `mapstructure:"server"`
	MySQL     MySQLConfig     `mapstructure:"mysql"`
	Redis     RedisConfig     `mapstructure:"redis"`
	ShortLink ShortLinkConfig `mapstructure:"shortlink"`
	Log       LogConfig       `mapstructure:"log"`
}

// ServerConfig holds HTTP server configuration.
type ServerConfig struct {
	Port int    `mapstructure:"port"`
	Mode string `mapstructure:"mode"`
}

// MySQLConfig holds database configuration.
type MySQLConfig struct {
	Host         string `mapstructure:"host"`
	Port         int    `mapstructure:"port"`
	User         string `mapstructure:"user"`
	Password     string `mapstructure:"password"`
	DBName       string `mapstructure:"dbname"`
	MaxOpenConns int    `mapstructure:"max_open_conns"`
	MaxIdleConns int    `mapstructure:"max_idle_conns"`
}

// DSN returns the MySQL connection string.
func (m MySQLConfig) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		m.User, m.Password, m.Host, m.Port, m.DBName,
	)
}

// RedisConfig holds Redis configuration.
type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
	PoolSize int    `mapstructure:"pool_size"`
}

// ShortLinkConfig holds short-link specific configuration.
type ShortLinkConfig struct {
	DefaultExpireDays int              `mapstructure:"default_expire_days"`
	RedisCacheTTL     int              `mapstructure:"redis_cache_ttl"`
	BloomFilter       BloomFilterConfig `mapstructure:"bloom_filter"`
	IDCounterKey      string           `mapstructure:"id_counter_key"`
}

// BloomFilterConfig holds bloom filter configuration.
type BloomFilterConfig struct {
	UseRedis  bool    `mapstructure:"use_redis"`
	RedisKey  string  `mapstructure:"redis_key"`
	ErrorRate float64 `mapstructure:"error_rate"`
	Capacity  uint    `mapstructure:"capacity"`
}

// LogConfig holds logging configuration.
type LogConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}

// Load reads configuration from the given path and returns a Config.
func Load(configPath string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(configPath)
	v.SetConfigType("yaml")

	// Also read from environment variables with prefix "SL_"
	v.SetEnvPrefix("SL")
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		log.Printf("WARN: cannot read config file %s: %v, using defaults and env", configPath, err)
	}

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("unable to decode config: %w", err)
	}

	// Apply sensible defaults
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
