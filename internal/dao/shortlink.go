// Package dao（Data Access Object）封装所有 short_links 表的数据库操作。
// 持有 *gorm.DB 实例，提供类型安全的 CRUD 方法，隔离业务层与 SQL 细节。
package dao

import (
	"context"
	"errors"
	"fmt"
	"time"

	"shortlink/internal/model"

	"gorm.io/gorm"
)

// ShortLinkDAO 是短链数据访问对象，封装对 short_links 表的所有查询。
// 所有方法都接收 context.Context 以支持超时控制和链路追踪。
type ShortLinkDAO struct {
	db *gorm.DB
}

// NewShortLinkDAO 构造函数，注入 GORM 数据库连接。
// 调用者：main() → service.NewShortLinkService()
func NewShortLinkDAO(db *gorm.DB) *ShortLinkDAO {
	return &ShortLinkDAO{db: db}
}

// Create 插入一条新的短链记录。
// 如果 ShortCode 违反唯一索引（MySQL 错误 1062），返回 "already exists" 错误。
// 插入成功后，link.ID 会被回填为数据库生成的自增 ID。
//
// 调用者：service.Shorten() — 生成短码后写入数据库
func (d *ShortLinkDAO) Create(ctx context.Context, link *model.ShortLink) error {
	result := d.db.WithContext(ctx).Create(link)
	if result.Error != nil {
		// 判断是否为唯一键冲突（MySQL error 1062）
		if errors.Is(result.Error, gorm.ErrDuplicatedKey) {
			return fmt.Errorf("short code already exists: %s", link.ShortCode)
		}
		return fmt.Errorf("failed to create short link: %w", result.Error)
	}
	return nil
}

// GetByShortCode 根据短码查询一条记录。
// GORM 自动过滤软删除的记录（DeletedAt IS NOT NULL）。
// 注意：不检查是否过期，过期判断由调用方（Service 层）通过 IsExpired() 完成。
// 未找到时返回 gorm.ErrRecordNotFound。
//
// 调用者：service.lookup() — 缓存未命中时回源查询数据库
func (d *ShortLinkDAO) GetByShortCode(ctx context.Context, shortCode string) (*model.ShortLink, error) {
	var link model.ShortLink
	result := d.db.WithContext(ctx).
		Where("short_code = ?", shortCode).
		First(&link)

	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, result.Error
		}
		return nil, fmt.Errorf("failed to query short link: %w", result.Error)
	}

	return &link, nil
}

// ExistsByShortCode 检查短码是否存在于数据库中（包括已过期、软删除的记录）。
// 使用 COUNT 查询，比 GetByShortCode 更轻量。
// 与 GetByShortCode 的区别：此方法不区分过期/删除，用于创建时的冲突检测，
// 确保同一短码不会被重复使用。
//
// 调用者：service.Shorten() — 自定义码冲突检测
func (d *ShortLinkDAO) ExistsByShortCode(ctx context.Context, shortCode string) (bool, error) {
	var count int64
	result := d.db.WithContext(ctx).
		Model(&model.ShortLink{}).
		Where("short_code = ?", shortCode).
		Count(&count)

	if result.Error != nil {
		return false, fmt.Errorf("failed to check short code existence: %w", result.Error)
	}
	return count > 0, nil
}

// GetAllActiveShortCodes 查询所有"活跃"短码列表。
// "活跃"定义：未过期（expire_at IS NULL 或 expire_at > 当前时间）且未软删除。
// 此方法专为布隆过滤器重建设计，返回字符串切片而非完整模型以节省内存。
//
// 调用者：bloom.Filter.Rebuild() — 启动时和定时重建时调用
func (d *ShortLinkDAO) GetAllActiveShortCodes(ctx context.Context) ([]string, error) {
	var codes []string
	now := time.Now()
	result := d.db.WithContext(ctx).
		Model(&model.ShortLink{}).
		Where("expire_at IS NULL OR expire_at > ?", now).
		Pluck("short_code", &codes)

	if result.Error != nil {
		return nil, fmt.Errorf("failed to query active short codes: %w", result.Error)
	}
	return codes, nil
}

// DeleteExpired 软删除所有已过期的记录（expire_at <= NOW）。
// 返回被删除的记录数。
//
// 当前状态：已实现，可在定时任务中调用以定期清理过期数据。
// 扩展点：可配合 cron 定时任务每天凌晨执行。
func (d *ShortLinkDAO) DeleteExpired(ctx context.Context) (int64, error) {
	now := time.Now()
	result := d.db.WithContext(ctx).
		Where("expire_at IS NOT NULL AND expire_at <= ?", now).
		Delete(&model.ShortLink{})

	if result.Error != nil {
		return 0, fmt.Errorf("failed to delete expired links: %w", result.Error)
	}
	return result.RowsAffected, nil
}
