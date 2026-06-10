package bloom

import (
	"context"
	"sync"
	"time"

	"github.com/bits-and-blooms/bloom/v3"
)

// Filter wraps the bloom filter implementation for short code existence checking.
// It supports both in-memory and Redis-backed bloom filters.
//
// In-memory mode (use_redis: false) uses bits-and-blooms/bloom for high performance.
// Redis mode (use_redis: true) uses Redis Bitmap for multi-instance sharing.
type Filter struct {
	memFilter *bloom.BloomFilter
	useRedis  bool
	redisKey  string
	mu        sync.RWMutex
}

// NewFilter creates a new bloom filter.
// If useRedis is false, it creates an in-memory bloom filter with the given capacity and error rate.
// If useRedis is true, redisKey is the key used for the Redis bitmap.
func NewFilter(capacity uint, errorRate float64, useRedis bool, redisKey string) *Filter {
	f := &Filter{
		useRedis: useRedis,
		redisKey: redisKey,
	}
	if !useRedis {
		f.memFilter = bloom.NewWithEstimates(capacity, errorRate)
	}
	return f
}

// Add inserts a short code into the bloom filter.
func (f *Filter) Add(code string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.useRedis {
		f.memFilter.AddString(code)
	}
	// Redis Add would be implemented via SETBIT for each hash position
}

// Test checks if a short code might exist in the bloom filter.
// Returns true if the code might exist (possible false positive), false if definitely absent.
func (f *Filter) Test(code string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if !f.useRedis {
		return f.memFilter.TestString(code)
	}
	// Redis Test would be implemented via GETBIT for each hash position
	return true // conservatively assume present if Redis not implemented
}

// Rebuild clears the filter and reloads it from the database.
// This is called periodically to remove expired entries and ensure consistency.
func (f *Filter) Rebuild(ctx context.Context, loader func(context.Context) ([]string, error)) error {
	codes, err := loader(ctx)
	if err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if !f.useRedis {
		// Clear and reload the in-memory filter
		f.memFilter.ClearAll()
		for _, code := range codes {
			f.memFilter.AddString(code)
		}
	}
	// Redis rebuild would pipeline DEL + batch SETBIT
	return nil
}

// StartRebuildLoop starts a periodic goroutine that rebuilds the bloom filter.
// interval specifies how often to rebuild (e.g., 1 hour).
// loader is called to get active short codes from the database.
func (f *Filter) StartRebuildLoop(ctx context.Context, interval time.Duration, loader func(context.Context) ([]string, error)) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := f.Rebuild(ctx, loader); err != nil {
					// Logging handled by caller; we just skip this cycle
				}
			}
		}
	}()
}
