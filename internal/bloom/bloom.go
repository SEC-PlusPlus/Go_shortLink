// Package bloom 封装布隆过滤器，用于快速判断短码是否"绝对不存在"。
//
// 核心原理：
//   布隆过滤器是一种空间高效的概率性数据结构。
//   - Test(code) == false → 短码绝对不存在，无需查询数据库（100% 准确）
//   - Test(code) == true  → 短码可能存在，需要进一步查询确认（可能误判）
//
// 两种模式：
//   - 内存模式（use_redis=false）：基于 bits-and-blooms/bloom 库，单实例高性能
//   - Redis 模式（use_redis=true）：基于 Redis Bitmap，多实例共享（当前为预留接口）
//
// 生命周期：
//   启动时从数据库全量加载 → 新增时实时 Add → 每小时定时重建（清理过期条目）
package bloom

import (
	"context"
	"sync"
	"time"

	"github.com/bits-and-blooms/bloom/v3"
)

// Filter 是布隆过滤器的封装结构体。
// 使用读写锁保证并发安全：Test 可并发执行（读锁），Add/Rebuild 互斥执行（写锁）。
type Filter struct {
	memFilter *bloom.BloomFilter // 内存布隆过滤器实例（useRedis=false 时使用）
	useRedis  bool               // 是否使用 Redis Bitmap 模式
	redisKey  string             // Redis Bitmap 的 key 名称
	mu        sync.RWMutex       // 读写锁，保证并发安全
}

// NewFilter 创建布隆过滤器实例。
//
// 参数：
//   capacity  — 预期存储的元素数量，决定 Bitmap 大小
//   errorRate — 目标误判率，越小占用内存越多（如 0.001 = 0.1%）
//   useRedis  — true 使用 Redis Bitmap 模式；false 使用内存模式
//   redisKey  — Redis 模式下的 key 名（useRedis=true 时有效）
//
// 调用者：main() — 启动时创建
func NewFilter(capacity uint, errorRate float64, useRedis bool, redisKey string) *Filter {
	f := &Filter{
		useRedis: useRedis,
		redisKey: redisKey,
	}
	if !useRedis {
		// 根据预期容量和误判率自动计算最优的哈希函数数量和 Bitmap 大小
		f.memFilter = bloom.NewWithEstimates(capacity, errorRate)
	}
	return f
}

// Add 将一个短码加入布隆过滤器。
// 写操作持有写锁（Lock），与 Test 的读锁互斥。
//
// 调用者：service.Shorten() — 新建短链后实时添加
func (f *Filter) Add(code string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.useRedis {
		f.memFilter.AddString(code)
	}
	// Redis 模式：通过 SETBIT 对多个哈希位置置 1（待实现）
}

// Test 检查短码是否"可能存在"于布隆过滤器中。
// 返回 false 表示短码绝对不存在（可直接返回 404）。
// 返回 true 表示短码可能存在（需进一步查询 MySQL 确认）。
// 读操作持有读锁（RLock），允许多个 Test 并发执行。
//
// 调用者：service.Redirect() — 第一步快速否决
func (f *Filter) Test(code string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if !f.useRedis {
		return f.memFilter.TestString(code)
	}
	// Redis 模式：通过 GETBIT 检查所有哈希位置（待实现）
	return true // 保守策略：未实现 Redis 时默认返回"可能存在"
}

// Rebuild 清空布隆过滤器并从 loader 函数重新加载所有活跃短码。
// 写操作持有写锁（Lock），重建期间所有 Add/Test 操作阻塞等待。
//
// 参数 loader 是数据源函数，通常为 dao.GetAllActiveShortCodes。
//
// 调用者：
//   - service.RebuildBloomFilter() — 启动时调用
//   - StartRebuildLoop 的定时协程 — 每小时调用
func (f *Filter) Rebuild(ctx context.Context, loader func(context.Context) ([]string, error)) error {
	// 从 loader（通常是数据库）获取所有活跃短码
	codes, err := loader(ctx)
	if err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if !f.useRedis {
		// 清空现有数据，重新加载
		f.memFilter.ClearAll()
		for _, code := range codes {
			f.memFilter.AddString(code)
		}
	}
	// Redis 模式：先 DEL key，再批量 SETBIT（待实现）
	return nil
}

// StartRebuildLoop 启动后台定时重建协程。
// 每隔 interval 时间（如 1 小时）从 loader 重新加载数据重建布隆过滤器。
// 监听 ctx.Done()，当上下文取消时自动退出（配合应用的优雅关闭）。
//
// 参数：
//   interval — 重建间隔，推荐 1 小时
//   loader   — 数据源函数，通常为 dao.GetAllActiveShortCodes
//
// 调用者：service.StartBloomRebuildLoop() → main()
func (f *Filter) StartRebuildLoop(ctx context.Context, interval time.Duration, loader func(context.Context) ([]string, error)) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				// 应用正在关闭，退出协程
				return
			case <-ticker.C:
				// 定时重建，错误静默跳过（日志由调用方处理）
				if err := f.Rebuild(ctx, loader); err != nil {
					// 重建失败不中断定时循环，下次继续尝试
				}
			}
		}
	}()
}
