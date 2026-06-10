// Package model 定义数据库实体模型，与 MySQL 表结构一一映射。
// GORM 通过结构体 Tag 自动处理表名、字段类型、索引、软删除等。
package model

import (
	"time"

	"gorm.io/gorm"
)

// ShortLink 是 short_links 表的 GORM 映射模型。
//
// 字段说明：
//   ID          — 自增主键
//   ShortCode   — 短码，唯一索引，最大 10 字符
//   OriginalURL — 原始长 URL，text 类型不限长度
//   ExpireAt    — 过期时间，NULL 表示永久有效（索引字段）
//   CreatedAt   — 创建时间（GORM 自动管理）
//   UpdatedAt   — 更新时间（GORM 自动管理）
//   DeletedAt   — 软删除时间（NULL 表示未删除，GORM 自动过滤已删除记录）
type ShortLink struct {
	ID          uint64         `gorm:"primaryKey;autoIncrement" json:"id"`
	ShortCode   string         `gorm:"uniqueIndex;size:10;not null" json:"short_code"`
	OriginalURL string         `gorm:"type:text;not null" json:"original_url"`
	ExpireAt    *time.Time     `gorm:"index" json:"expire_at"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"` // json:"-" 表示序列化时隐藏
}

// TableName 显式指定表名为 short_links。
// GORM 默认会将结构体名蛇形复数化（short_links 正好一致），但显式声明更清晰。
func (ShortLink) TableName() string {
	return "short_links"
}

// IsExpired 判断短链是否已过期。
// ExpireAt 为 nil 表示永久有效，返回 false。
// ExpireAt 不为 nil 且早于当前时间，返回 true。
//
// 调用位置：service.lookup() — 从数据库查到记录后判断是否返回 410。
func (s *ShortLink) IsExpired() bool {
	if s.ExpireAt == nil {
		return false // 永久有效
	}
	return time.Now().After(*s.ExpireAt) // 已过期
}
