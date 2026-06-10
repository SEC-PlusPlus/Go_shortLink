package model

import (
	"time"

	"gorm.io/gorm"
)

// ShortLink represents the short_links table in MySQL.
//
// Fields:
//   ID          - auto-increment primary key
//   ShortCode   - the generated or custom short code (unique)
//   OriginalURL - the original long URL
//   ExpireAt    - expiration time; nil means permanent
//   CreatedAt   - record creation timestamp
//   UpdatedAt   - last update timestamp
//   DeletedAt   - soft delete timestamp (GORM soft delete)
type ShortLink struct {
	ID          uint64         `gorm:"primaryKey;autoIncrement" json:"id"`
	ShortCode   string         `gorm:"uniqueIndex;size:10;not null" json:"short_code"`
	OriginalURL string         `gorm:"type:text;not null" json:"original_url"`
	ExpireAt    *time.Time     `gorm:"index" json:"expire_at"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName explicitly sets the table name.
func (ShortLink) TableName() string {
	return "short_links"
}

// IsExpired checks whether the short link has expired.
func (s *ShortLink) IsExpired() bool {
	if s.ExpireAt == nil {
		return false
	}
	return time.Now().After(*s.ExpireAt)
}
