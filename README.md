# IM — Instant Messaging Backend

Go 编写的即时通讯后端，涵盖用户系统、WebSocket 实时消息、群组聊天。
从零搭建到生产就绪，作为后端工程学习项目。

**技术栈**: Go · Gin · PostgreSQL · Redis · Kafka · WebSocket · Docker · Prometheus

## 功能

- 用户注册/登录，JWT 双 token 认证 (access + refresh)
- 好友系统，双向确认
- WebSocket 实时私聊 + 群聊，在线状态追踪，心跳保活
- 消息持久化、离线消息投递、2 分钟撤回窗口
- 多实例扩展（Redis Pub/Sub 跨实例路由、Kafka 异步管道）
- 结构化日志 (slog)、Prometheus 指标、Grafana 面板
- Docker 多阶段构建 (~15MB 镜像)、docker-compose 一键启动

## 快速开始

```bash
git clone https://github.com/flyingwhit/IM.git && cd IM

# 启动 PostgreSQL + Redis + IM Server
docker compose up -d

# 验证
curl http://localhost:8082/health
```

可选启用 Kafka / Prometheus + Grafana：
```bash
docker compose --profile kafka up -d
docker compose -f docker-compose.yml -f configs/docker-compose.observability.yml up -d
```

## 本地开发

**前置条件**：Go ≥ 1.26、PostgreSQL 16、Redis 7、golang-migrate

```bash
# 1. 创建数据库
createdb im_db

# 2. 配置环境
cp .env.example .env   # 默认值可直接用于本地开发

# 3. 数据库迁移
make migrate-up

# 4. 运行
make run               # 编译 + 启动，Ctrl+C 优雅关闭
make test              # 运行所有测试
```

服务地址：API `http://localhost:8082` · Metrics `http://localhost:8082/metrics`

## 负载测试

```bash
./scripts/loadtest.sh                      # 默认: 50 req/s, 30s
./scripts/loadtest.sh -rate 200 -duration 60s
```

如需跳过注册（用已有 token）：`./scripts/loadtest.sh -skip-register`

## API 概览

REST API 涵盖认证、用户、好友、消息、群组。WebSocket 连接 `ws://host/ws?token=<JWT>`。

完整 API 文档见 **[docs/api.md](docs/api.md)**。

## 项目结构

```
cmd/server/          # 入口，组合根
internal/
├── config/          # 环境变量加载
├── gateway/         # WebSocket Hub、Client、升级处理器
├── handler/         # HTTP 处理器 (Gin)
├── kafka/           # Kafka producer/consumer
├── middleware/       # JWT 认证、CORS、Prometheus 指标
├── model/           # 共享类型、领域错误
├── repository/      # PostgreSQL + Redis 数据访问
├── router/          # 路由注册
├── service/         # 业务逻辑层
└── ws/              # WebSocket 消息协议
configs/             # Docker Compose、Prometheus、Grafana 配置
docs/                # 架构、数据库、API 设计文档
migrations/          # PostgreSQL 迁移 SQL
scripts/             # 运维脚本（负载测试等）
tools/im-client/     # 浏览器端测试客户端（vanilla JS + WebSocket）
```

更多设计决策见 **[docs/architecture.md](docs/architecture.md)**。

## 常用命令

`make run` / `make build` / `make test` / `make fmt` / `make migrate-up`

## 文档

| 文档 | 内容 |
|------|------|
| [architecture.md](docs/architecture.md) | 分层架构、WebSocket 模型、多实例扩展 |
| [database.md](docs/database.md) | Schema 设计、索引策略 |
| [api.md](docs/api.md) | 认证流程、错误约定 |
| [decisions.md](docs/decisions.md) | 重要技术选择与取舍 |
| [learning_notes.md](docs/learning_notes.md) | Go 概念讲解、实践总结 |
| [observability.md](docs/observability.md) | 日志、指标、Grafana 配置 |
| [troubleshooting.md](docs/troubleshooting.md) | 常见问题与调试 |

## CI

GitHub Actions：Lint (golangci-lint) → Test (`-race`) → Build。配置见 `.github/workflows/ci.yml`。

## License

MIT
