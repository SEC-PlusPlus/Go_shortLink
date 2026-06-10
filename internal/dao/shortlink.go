package dao

import (
	"context"
	"errors"
	"fmt"
	"time"

	"shortlink/internal/model"

	"gorm.io/gorm"
)

// ShortLinkDAO handles all database operations for the short_links table.
// It wraps GORM and provides typed methods for CRUD operations.
type ShortLinkDAO struct {
	db *gorm.DB
}

// NewShortLinkDAO creates a new ShortLinkDAO.
func NewShortLinkDAO(db *gorm.DB) *ShortLinkDAO {
	return &ShortLinkDAO{db: db}
}

// Create inserts a new short link record.
// It returns the created record or an error if the short_code already exists (duplicate key).
func (d *ShortLinkDAO) Create(ctx context.Context, link *model.ShortLink) error {
	result := d.db.WithContext(ctx).Create(link)
	if result.Error != nil {
		// Check for duplicate entry (MySQL error 1062)
		if errors.Is(result.Error, gorm.ErrDuplicatedKey) {
			return fmt.Errorf("short code already exists: %s", link.ShortCode)
		}
		return fmt.Errorf("failed to create short link: %w", result.Error)
	}
	return nil
}

// GetByShortCode retrieves an unexpired, non-deleted short link by its short code.
// Returns gorm.ErrRecordNotFound if no matching record exists.
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

// ExistsByShortCode checks if a short code exists (including expired/soft-deleted).
// Used during custom code conflict checking.
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

// GetAllActiveShortCodes retrieves all short codes that are not expired and not deleted.
// This is used by the bloom filter to rebuild its state from the database.
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

// DeleteExpired soft-deletes all records whose expire_at is in the past.
// Returns the number of affected rows.
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
