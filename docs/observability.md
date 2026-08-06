# 可观测性 (Observability)

## 概述

Phase 5 为 IM 后端接入三大可观测性支柱：

| 支柱 | 工具 | 回答什么问题 |
|------|------|-------------|
| **结构化日志** | `log/slog` (Go 1.21 stdlib) | 某个请求发生了什么？ |
| **指标 (Metrics)** | Prometheus + promauto | 系统整体趋势如何？错误率、延迟、连接数 |
| **健康检查** | `/health` + `/health/ready` | 进程是否存活？能否处理请求？ |

当前未覆盖的支柱：

- **链路追踪 (Tracing)**: OpenTelemetry — 单实例阶段收益不大，留待 Phase 7+.

## 设计决策

### 为什么选 `log/slog` 而非 `zap`/`zerolog`？

Go 1.21 标准库引入了 `log/slog`，提供了结构化日志所需的核心能力：

- 键值对结构化输出 (`slog.Info("msg", "key", value)`)
- JSON 和 Text 两种格式
- 零内存分配的 `LogAttrs` 方法
- 可插拔的 Handler（未来可切换到 OpenTelemetry bridge）

选择 `slog` 的原因：

1. **零外部依赖**：遵循项目 "Prefer standard library" 原则
2. **足够用**：IM 后端每天几十万条日志，slog 的性能完全满足
3. **生态趋势**：越来越多的库直接支持 `slog.Handler`（如 GORM、pgx）

取舍：`slog` 缺少 `zap` 的 caller 跳过次数配置和采样功能。对当前规模可接受。

### 为什么用 Prometheus 而非 InfluxDB / Datadog？

- **Pull 模型**：服务暴露 `/metrics`，Prometheus 定时抓取。不需要服务主动推送，服务逻辑更简单。
- **单一二进制**：Prometheus server 是一个二进制文件，本地开发零依赖。
- **事实标准**：Prometheus metrics 格式是云原生生态的标准，Kubernetes、Grafana、Alertmanager 都原生支持。
- **pro-mauto 模式**：`promauto` 在包级别自动注册，零样板代码。一个 counter 就是一行 `var x = promauto.NewCounter(...)`。

### 为什么不用 OpenTelemetry SDK 直接发 metrics？

OTel SDK 比 `promauto` 多一层抽象，且最终还是要接 Prometheus exporter。Phase 5 的 metrics 体量小（~10 个指标），直接用 `promauto` 更简单。未来如果要换后端（如 Datadog），迁移只是在 collector 层配置，不需要改代码。

## 指标设计：RED 方法

HTTP 指标遵循 **RED 方法**（Rate, Errors, Duration）—— 这是 Google SRE 推荐的微服务监控最小集。

| 指标 | 类型 | PromQL 示例 |
|------|------|------------|
| `http_requests_total{method,path,status}` | Counter | `rate(http_requests_total{status=~"5.."}[1m])` 错误率 |
| `http_request_duration_seconds{method,path}` | Histogram | `histogram_quantile(0.99, rate(..._bucket[1m]))` P99 延迟 |

关键细节：

- **Path 用路由模板**：`c.FullPath()` 返回 `/api/v1/users/:id` 而非 `/api/v1/users/123`。如果每条具体 URL 都成一个 label，cardinality 会无限增长（用户 ID、消息 ID 都是无界值），Prometheus 内存会爆炸。这是生产环境常见的错误。

- **Histogram buckets**：`[.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10]`（秒）。从 5ms（快速 DB 读）到 10s（慢上传），覆盖大部分请求场景。buckets 越多精度越高，但每个 bucket 是一个 time series —— 10 个 bucket × 3 path × 3 method = 90 个 series，量很小。

## WebSocket 指标

| 指标 | 类型 | 含义 |
|------|------|------|
| `ws_connections_active` | Gauge | 当前活跃连接数。突然下降 → 可能 crash 或网络分区 |
| `ws_connections_total` | Counter | 启动以来建立的连接总数。rate() 可看到连接/断开频率 |
| `ws_messages_received_total` | Counter | 从客户端收到的消息数 |
| `ws_messages_sent_total` | Counter | 发送给客户端的消息数 |

设计要点：

- `wsMessagesSent` 只在 `sendRaw()` 递增 —— 这是所有出站消息的唯一出口。无论是本地投递还是跨实例投递（broker），都走 `sendRaw`。**单点计数避免重复**。

- `wsConnectionsActive` 用 Gauge 而非 Counter —— 它反映的是瞬时值，不是累计量。Prometheus 中 Counter 只能递增，Gauge 可增可减。

## Kafka 指标

| 指标 | 类型 | 含义 |
|------|------|------|
| `kafka_publish_total` | Counter | Kafka 发送尝试总数 |
| `kafka_publish_errors_total` | Counter | Kafka 发送失败数 |

Kafka 采用 fire-and-forget：失败只记日志+递增 counter，**不阻塞**消息发送路径。DB 是消息的 source of truth，Kafka 只是下游消费者的加速通道。所以 `kafka_publish_errors_total` 的告警阈值应该低于 HTTP 错误 —— Kafka 降级不影响消息收发。

## 健康检查

两个端点，两种用途：

| 端点 | 类型 | 检查内容 | 失败后果 |
|------|------|---------|---------|
| `/health` | Liveness | 进程是否存活 | K8s 重启 Pod |
| `/health/ready` | Readiness | DB/Redis/Kafka 是否连通 | K8s 从 Service 摘除 |

**Readiness 为什么用 2 秒超时？**

健康检查本身不应该成为瓶颈。如果 DB ping 卡住 60 秒，在它超时前 readiness probe 已经超时返回了 503，K8s 会摘除该 Pod。但 2 秒内的问题是真正的连通性问题，应该触发摘除。Server 的 WriteTimeout 是 10 秒，2 秒探针完全有余量。

**Kafka 的 readiness 检查为什么只看错误率？**

我们不能发一条测试消息（会污染 topic），也不能简单 ping（Kafka protocol 不支持轻量 ping）。所以我们看错误率：如果超过 50% 的 attempts 失败，标记为 degraded。Kafka 不在关键路径上，所以只是 degraded 而非 unhealthy。

## 运行时指标

`init()` 中自动注册的 Go 运行时指标（通过 `collectors.NewGoCollector()` 和 `collectors.NewProcessCollector()`）：

- `go_goroutines`：持续增长 → goroutine 泄漏
- `go_memstats_heap_inuse_bytes`：持续增长 → 内存泄漏
- `go_gc_duration_seconds`：尖刺 → 内存分配压力大，可能触发了 GC pause
- `process_open_fds`：连接/文件描述符泄漏

这些是 **免费的** —— 不需要写一行 instrumentation 代码。

## 本地运行

```bash
# 1. 启动基础设施 (DB, Redis)
docker compose up -d

# 2. 启动 IM 服务器 (按 Phase 4 方式)
go run ./cmd/server

# 3. 启动 Prometheus + Grafana
docker compose -f docker-compose.yml -f configs/docker-compose.observability.yml up -d

# 4. 访问
# Grafana:    http://localhost:3000 (admin/admin)
# Prometheus: http://localhost:9090
# Metrics:    http://localhost:8080/metrics
```

Grafana 中添加 Prometheus 数据源后，可以手动导入社区 Dashboard（如 Go Processes #6671）或者自己画面板。

## 下一步（Phase 7+）

- **Trace ID 注入**：中间件生成 trace ID 注入 context + 响应头，然后关联日志/metrics/trace
- **OpenTelemetry SDK**：自动生成 trace spans（HTTP → Service → DB），发送到 Jaeger/Tempo
- **SLO 定义**：定义服务的 SLO（如 99.9% 消息 1s 内送达），用 Prometheus recording rules 计算 error budget
- **Alertmanager**：配置告警规则（HTTP 5xx 率 > 1%、WebSocket 连接数 < 最低水位、DB 连接池耗尽等）

## 参考

- [Go `log/slog` 官方文档](https://pkg.go.dev/log/slog)
- [Prometheus 命名最佳实践](https://prometheus.io/docs/practices/naming/)
- [RED Method (Tom Wilkie)](https://grafana.com/blog/2018/08/02/the-red-method-how-to-instrument-your-services/)
- [Google SRE Book: Monitoring Distributed Systems](https://sre.google/sre-book/monitoring-distributed-systems/)
