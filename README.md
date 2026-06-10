# Go_shortLink

高性能短链接服务，将长 URL 转换为短码，访问短链时 301/302 重定向到原始地址。

## 特性

- **短链生成** — 自动生成（Redis 发号器 + Base62 编码）或自定义短码
- **重定向** — 301 永久 / 302 临时，可配置
- **过期失效** — 支持设置过期时间，NULL 表示永久有效
- **高并发读** — Singleflight 合并并发请求，防止缓存击穿
- **缓存策略** — Cache-Aside 模式，Redis 缓存 + MySQL 持久化
- **布隆过滤器** — 内存布隆过滤器，快速拦截无效短码，防止缓存穿透
- **软删除** — GORM 软删除支持
- **全链路追踪** — 每个请求注入 TraceID，贯穿日志
- **结构化日志** — Zap JSON 格式日志
- **优雅关闭** — 监听信号，5 秒超时优雅退出

## 技术栈

| 组件 | 选型 |
|------|------|
| Web 框架 | [Gin](https://github.com/gin-gonic/gin) |
| ORM | [GORM](https://gorm.io/) |
| 数据库 | MySQL |
| 缓存 | Redis |
| 配置管理 | [Viper](https://github.com/spf13/viper) |
| 日志 | [Zap](https://github.com/uber-go/zap) |
| 参数校验 | [validator](https://github.com/go-playground/validator) |
| 并发合并 | [singleflight](https://pkg.go.dev/golang.org/x/sync/singleflight) |
| 布隆过滤器 | [bits-and-blooms/bloom](https://github.com/bits-and-blooms/bloom) |
| Base62 | 自实现 |

## 系统架构

```
┌─────────┐      ┌──────────────────────┐      ┌─────────┐
│  Client  │─────▶│   Gin HTTP           │─────▶│  Redis  │
└─────────┘      │   TraceID → Logger   │      │  缓存    │
                 │   → Recovery         │      │  发号器  │
                 └──────────┬───────────┘      │  布隆    │
                            │                  └─────────┘
                            ▼
                 ┌──────────────────────┐
                 │   Service 层          │
                 │   Singleflight 防击穿  │
                 │   Cache-Aside 策略     │
                 └──────────┬───────────┘
                            │
                            ▼
                 ┌──────────────────────┐
                 │   MySQL (GORM)       │
                 │   short_links 表      │
                 └──────────────────────┘
```

## 项目结构

```
shortlink/
├── config/
│   ├── config.go          # 配置结构体 + Viper 加载
│   └── config.yaml        # 默认配置文件
├── internal/
│   ├── handler/
│   │   └── shortlink.go   # Gin HTTP 处理器
│   ├── service/
│   │   └── shortlink.go   # 核心业务逻辑
│   ├── dao/
│   │   └── shortlink.go   # GORM 数据访问层
│   ├── model/
│   │   └── shortlink.go   # 数据库模型
│   ├── middleware/
│   │   ├── traceid.go     # TraceID 注入
│   │   ├── logger.go      # 请求日志
│   │   └── recovery.go    # Panic 恢复
│   └── bloom/
│       └── bloom.go       # 布隆过滤器
├── pkg/
│   ├── base62/
│   │   └── base62.go      # Base62 编解码
│   └── response/
│       └── response.go    # 统一 API 响应格式
├── main.go                # 入口 + 依赖注入
├── go.mod
└── Makefile
```

## 快速开始

### 环境要求

- Go 1.21+
- MySQL 8.0+
- Redis 6.0+

### 1. 准备数据库

```sql
CREATE DATABASE IF NOT EXISTS shorturl
  DEFAULT CHARACTER SET utf8mb4
  DEFAULT COLLATE utf8mb4_unicode_ci;
```

表结构由 GORM AutoMigrate 自动创建，无需手动建表。

### 2. 配置文件

编辑 `config/config.yaml`，修改数据库和 Redis 连接信息：

```yaml
mysql:
  host: 127.0.0.1
  port: 3306
  user: root
  password: "your_password"
  dbname: shorturl

redis:
  addr: 127.0.0.1:6379
  password: ""
```

也可通过环境变量覆盖（前缀 `SL_`），如：

```bash
export SL_MYSQL_PASSWORD="your_password"
export SL_REDIS_ADDR="127.0.0.1:6379"
```

### 3. 运行

```bash
# 下载依赖
make tidy

# 运行
make run

# 或者
go run main.go -config=config/config.yaml
```

服务默认监听 `http://localhost:8080`。

### 4. 编译

```bash
make build
./bin/shortlink -config=config/config.yaml
```

## API 文档

### 生成短链

```http
POST /api/shorten
Content-Type: application/json

{
  "original_url": "https://example.com/very/long/url/path?param=value",
  "custom_code": "mycode",    // 可选，4-10位字母数字
  "expire_days": 30           // 可选，0=使用默认值，-1=永久
}
```

**成功响应 (200):**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "short_url": "http://localhost:8080/abc123",
    "short_code": "abc123",
    "expire_at": "2026-07-10T12:00:00Z"
  }
}
```

**错误响应:**

| HTTP | code | 说明 |
|------|------|------|
| 400 | 1001 | 参数校验失败 |
| 409 | 1002 | 自定义码已被占用 |
| 500 | 1003 | 服务内部错误 |

### 重定向

```http
GET /:code
```

- 成功 → `301 Moved Permanently` 跳转到原始 URL
- 不存在 → `404` + JSON 错误 `{"code":1004,"message":"short link not found"}`
- 已过期 → `410` + JSON 错误 `{"code":1005,"message":"short link has expired"}`

### 健康检查

```http
GET /health
```

返回 `{"status":"ok"}`。

## 核心设计

### 短链生成流程

```
1. 自定义码？
   ├─ 是 → Redis EXISTS + MySQL 查重 → 无冲突则使用
   └─ 否 → Redis INCR 发号器 → Base62 编码

2. 计算 expire_at
3. INSERT INTO short_links
4. SET Redis 缓存 (TTL 与过期时间对齐)
5. ADD 布隆过滤器
```

### 重定向流程

```
1. 布隆过滤器快速否决 → false → 404
2. Singleflight 合并并发
3. Redis 缓存命中 → 301 跳转
4. 缓存未命中 → MySQL 查询
   ├─ 存在且有效 → 回写 Redis → 301
   ├─ 不存在 → 404
   └─ 已过期 → 410
```

### 布隆过滤器

- 启动时从 MySQL 加载所有活跃短码
- 每小时自动重建，清理过期条目
- 新增短链实时 Add
- 支持切换为 Redis Bitmap 模式（多实例共享）

### 防穿透 & 防击穿

| 机制 | 问题 | 解决方案 |
|------|------|----------|
| 布隆过滤器 | 大量无效短码穿透到 DB | 快速否决不存在的短码 |
| Singleflight | 热点短码并发击穿缓存 | 同 key 只发一次 DB 查询 |
| Redis 缓存 | 重复查询同一短码 | Cache-Aside 策略 |

## 配置说明

| 配置项 | 默认值 | 说明 |
|--------|--------|------|
| `server.port` | 8080 | HTTP 端口 |
| `server.mode` | debug | Gin 模式 (debug/release/test) |
| `shortlink.default_expire_days` | 30 | 默认过期天数 |
| `shortlink.redis_cache_ttl` | 3600 | 缓存 TTL（秒） |
| `shortlink.bloom_filter.capacity` | 1000000 | 布隆预期容量 |
| `shortlink.bloom_filter.error_rate` | 0.001 | 布隆误判率 |
| `shortlink.id_counter_key` | short:id:counter | Redis 发号器 key |
| `log.level` | info | 日志级别 |
| `log.format` | json | 日志格式 |

## Makefile

```makefile
make run      # 运行
make build    # 编译到 bin/shortlink
make test     # 运行测试
make tidy     # 下载依赖
make clean    # 清理编译产物
```

## License

MIT
