# IM — Instant Messaging Backend

一个用 Go 编写的即时通讯后端，涵盖用户系统、好友关系、WebSocket 实时消息、群组聊天。从零搭建到生产就绪的完整演进过程。

**技术栈**: Go · Gin · PostgreSQL · Redis · Kafka · WebSocket · Docker · Prometheus

## 功能

- **用户系统** — 注册、登录、JWT 双 token 认证 (access + refresh)
- **好友系统** — 添加、删除、列表，好友关系双向确认
- **实时消息** — WebSocket 长连接，在线状态追踪，心跳保活，消息送达回执
- **离线消息** — 离线用户上线后自动投递未读消息
- **消息撤回** — 2 分钟撤回窗口，已撤回消息在对话历史中标记
- **群组聊天** — 创建群组、加入/退出、群消息、游标分页历史记录
- **可观测性** — 结构化日志 (slog)、Prometheus 指标 (RED 方法)、Grafana 面板
- **多实例扩展** — Redis Pub/Sub 跨实例路由、Kafka 异步消息管道、Gateway/API/Worker 分离部署
- **容器化部署** — Docker 多阶段构建 (~15MB 镜像)、docker-compose 一键启动

## 快速开始

最简方式——Docker Compose 一行命令启动全部依赖：

```bash
# 克隆项目
git clone https://github.com/flyingwhit/IM.git
cd IM

# 启动 PostgreSQL + Redis + IM Server
docker compose up -d

# 运行数据库迁移
docker compose exec server ./migrate up   # 或使用 Makefile: make migrate-up
```

服务启动后：
- REST API: `http://localhost:8082`
- Prometheus 指标: `http://localhost:8082/metrics`
- 健康检查: `http://localhost:8082/health`

可选启用 Kafka（消息持久化管道）：
```bash
docker compose --profile kafka up -d
```

## 本地开发

### 前置条件

| 依赖 | 版本 | 用途 |
|------|------|------|
| Go | ≥ 1.26 | 编译和运行 |
| PostgreSQL | 16 | 持久化存储 |
| Redis | 7 | 缓存、Session、Pub/Sub |
| Kafka | 7.9 (可选) | 异步消息管道 |
| golang-migrate | latest | 数据库迁移 |
| vegeta | latest (可选) | 负载测试 |
| jq | latest (可选) | 脚本中 JSON 解析 |

### 1. 安装数据库

```bash
# macOS
brew install postgresql@16 redis

# 启动服务
brew services start postgresql@16
brew services start redis
```

PostgreSQL 需要创建数据库和用户：
```sql
CREATE USER im WITH PASSWORD 'im_secret';
CREATE DATABASE im_db OWNER im;
```

### 2. 配置环境变量

```bash
cp .env.example .env
```

`.env` 中的默认值可以直接用于本地开发。生产环境请修改 JWT secrets：
```bash
# 生成安全的随机密钥（不要用默认值部署到公网）
openssl rand -hex 32   # JWT_ACCESS_SECRET
openssl rand -hex 32   # JWT_REFRESH_SECRET
```

完整配置项见 [配置参考](#配置参考)。

### 3. 数据库迁移

```bash
# 安装 golang-migrate
go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest

# 执行迁移
make migrate-up

# 回滚
make migrate-down
```

### 4. 运行

```bash
# 开发模式（编译 + 运行，Ctrl+C 优雅关闭）
make run

# 或者分步操作
go build -o bin/im-server ./cmd/server
./bin/im-server

# 仅运行测试
make test
```

### 5. 验证

```bash
# 健康检查
curl http://localhost:8082/health

# 注册用户
curl -X POST http://localhost:8082/api/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{"username":"alice","email":"alice@example.com","password":"secret123","nickname":"Alice"}'

# 登录
curl -X POST http://localhost:8082/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"alice","password":"secret123"}'
```

## 负载测试

```bash
# 默认: 50 req/s 持续 30s
./scripts/loadtest.sh

# 自定义压力
./scripts/loadtest.sh -rate 200 -duration 60s
```

测试覆盖 4 个端点：health check → 注册 → 登录 → profile/friends/groups（带 JWT）。

## API 概览

### 认证

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/v1/auth/register` | 注册 |
| POST | `/api/v1/auth/login` | 登录，返回 access + refresh token |
| POST | `/api/v1/auth/refresh` | 刷新 access token |

### 用户

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/users/me` | 当前用户信息 |
| GET | `/api/v1/users/search?q=` | 搜索用户 |

### 好友

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/friends` | 好友列表 |
| POST | `/api/v1/friends` | 发送好友请求 |
| PUT | `/api/v1/friends/:id` | 通过好友请求 |
| DELETE | `/api/v1/friends/:id` | 删除好友 |

### 消息

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/messages?peer_id=&cursor=&limit=` | 私聊历史（游标分页）|
| POST | `/api/v1/messages/:id/recall` | 撤回消息 |

### 群组

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/v1/groups` | 创建群组 |
| GET | `/api/v1/groups` | 我的群组列表 |
| POST | `/api/v1/groups/:id/join` | 加入群组 |
| POST | `/api/v1/groups/:id/leave` | 退出群组 |
| GET | `/api/v1/groups/:id/messages?cursor=&limit=` | 群消息历史 |

### WebSocket

| 路径 | 说明 |
|------|------|
| `ws://host:port/ws?token=<JWT>` | 建立实时连接 |

WebSocket 消息协议见 `internal/ws/message.go`，支持 `message.send` / `message.new` / `message.ack` / `message.recall` / `presence.change` 等事件。

## 配置参考

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `SERVER_HOST` | `0.0.0.0` | 监听地址 |
| `SERVER_PORT` | `8080` | 监听端口 |
| `SERVER_MODE` | `all` | `all` / `gateway` / `api` / `worker` |
| `DB_HOST` | (必填) | PostgreSQL 主机 |
| `DB_PORT` | `5432` | PostgreSQL 端口 |
| `DB_USER` | (必填) | PostgreSQL 用户 |
| `DB_PASSWORD` | (必填) | PostgreSQL 密码 |
| `DB_NAME` | (必填) | PostgreSQL 数据库名 |
| `DB_SSLMODE` | `disable` | PostgreSQL SSL 模式 |
| `REDIS_HOST` | `localhost` | Redis 主机 |
| `REDIS_PORT` | `6379` | Redis 端口 |
| `REDIS_PASSWORD` | (空) | Redis 密码 |
| `REDIS_DB` | `0` | Redis 数据库编号 |
| `JWT_ACCESS_SECRET` | (必填) | Access token 签名密钥 |
| `JWT_REFRESH_SECRET` | (必填) | Refresh token 签名密钥 |
| `JWT_ACCESS_EXPIRY` | `15m` | Access token 有效期 |
| `JWT_REFRESH_EXPIRY` | `168h` | Refresh token 有效期 |
| `LOG_LEVEL` | `info` | 日志级别: `debug` / `info` / `warn` / `error` |
| `LOG_FORMAT` | `text` | 日志格式: `text` / `json` |
| `KAFKA_BROKERS` | (空) | Kafka 地址，留空则禁用 Kafka |
| `GATEWAY_INSTANCE_ID` | (自动生成) | 网关实例 ID |

## 项目结构

```
.
├── cmd/server/main.go          # 入口，组合根 (composition root)
├── internal/
│   ├── broker/                 # Redis Pub/Sub 消息代理
│   ├── config/                 # 环境变量加载与校验
│   ├── gateway/                # WebSocket: Hub, Client, 升级处理器
│   ├── handler/                # HTTP 处理器 (Gin)
│   ├── kafka/                  # Kafka producer / consumer
│   ├── logging/                # slog 初始化
│   ├── middleware/              # Gin 中间件 (JWT, CORS, Metrics)
│   ├── model/                  # 共享类型、请求/响应结构、领域错误
│   ├── repository/
│   │   ├── postgres/           # PostgreSQL 数据访问
│   │   └── redis/              # Redis 数据访问
│   ├── router/                 # 路由注册
│   ├── service/                # 业务逻辑层
│   └── ws/                     # WebSocket 消息协议类型
├── configs/                    # Docker / Prometheus / Grafana 配置
├── docs/                       # 架构、数据库、API 文档
├── migrations/                 # PostgreSQL 迁移 SQL
├── scripts/loadtest.sh         # vegeta 负载测试脚本
├── Dockerfile                  # 多阶段构建
├── docker-compose.yml          # 服务编排
├── Makefile                    # 常用命令
└── .github/workflows/ci.yml    # GitHub Actions CI
```

## 架构

```
Handler (Gin) → Service (业务逻辑) → Repository (数据访问) → PostgreSQL / Redis
                     ↕
              gateway.Hub (WebSocket)
                     ↕
           broker (Redis Pub/Sub) ←→ 其他实例
```

分层架构，四层职责分离。更多设计决策和演进过程见 `docs/architecture.md`。

## 开发命令

| 命令 | 说明 |
|------|------|
| `make run` | 编译并运行（支持 Ctrl+C 优雅关闭） |
| `make build` | 编译到 `bin/im-server` |
| `make test` | 运行所有测试 |
| `make fmt` | 格式化代码 |
| `make vet` | 静态分析 |
| `make deps` | 整理依赖 |
| `make migrate-up` | 执行数据库迁移 |
| `make migrate-down` | 回滚数据库迁移 |
| `make migrate-create NAME=xxx` | 创建新迁移文件 |

## 文档

- [架构设计](docs/architecture.md) — 分层架构、WebSocket 模型、多实例扩展
- [数据库设计](docs/database.md) — Schema、索引策略、数据建模
- [API 契约](docs/api.md) — 认证流程、错误约定、接口规范
- [工程决策](docs/decisions.md) — 重要技术选择与取舍
- [学习笔记](docs/learning_notes.md) — 概念讲解、实践总结
- [排错指南](docs/troubleshooting.md) — 常见问题与调试方法

## License

MIT
