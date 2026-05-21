# Lumen Gateway

## 当前目标

Lumen Gateway 是一个纯 Go L7 API 网关，基于 CloudWeGo Hertz/Netpoll，目标是提供低尾延迟代理、APISIX 兼容 Admin API、静态 YAML 与 etcd 动态配置、类型安全插件和单二进制部署。

## 项目类型

- 类型：Service + CLI 入口 + 少量 public extension API。
- 主模块：`github.com/joey/lumen-gateway`，Go 版本为 `1.25.0`。
- 运行入口：`cmd/lumen-gateway/main.go` 调用根包 `lumen.Run()`。
- 对外扩展包：根包 `lumen` 暴露运行选项，`plugin/` 暴露插件注册 API，`balancer/` 暴露自定义负载均衡接口。

## 优先级

1. 本文是项目级最高优先约束。
2. 用户当轮明确指令高于本文默认规则。
3. Skill 与本文冲突时，以本文为准。
4. 不确定时先读现有代码和测试，再按当前模式保守扩展。

## 当前结构

- `cmd/lumen-gateway/`：二进制入口，只做进程入口与错误退出。
- `lumen.go`：根包启动、CLI、Admin 子命令、source/watch/reload 编排，以及 public options。
- `internal/bootstrap/`：bootstrap 配置加载，选择 `file` 或 `etcd_apisix` source。
- `internal/config/`：原生 Lumen YAML 运行时配置模型、默认值和校验。
- `internal/provider/`：配置源抽象，负责 file/etcd 加载与 watch。
- `internal/gateway/`：网关运行时、Hertz server、原子快照、reload、请求执行链。
- `internal/router/`：exact/prefix/regex/wildcard host 路由匹配。
- `internal/proxy/`：HTTP 反向代理、连接池、httptrace 计时。
- `internal/plugin/` 与 `internal/plugin/builtin/`：插件注册、执行链、上下文和内置插件。
- `internal/balancer/` 与 `internal/balancer/roundrobin/`：内部负载均衡接口与加权轮询实现。
- `internal/controlplane/`：APISIX 兼容资源 store、bundle preview/apply/export、history、rollback。
- `internal/adminapi/`：APISIX 兼容 Admin API 与控制面端点。
- `internal/apisix/` 与 `internal/translate/`：APISIX 数据模型和 APISIX -> Lumen 配置转换。
- `internal/observability/`：Prometheus 指标记录与渲染。
- `configs/`：本地、Docker、benchmark、etcd bootstrap 和 gateway 配置。
- `deploy/`：Prometheus 与 Grafana provisioning。
- `docs/`：架构、Admin API、使用指南、benchmark 文档和结果。

## 架构规则

- 保持网关读路径无锁：运行时配置通过编译后的 snapshot 和 `atomic.Pointer` 切换。
- 请求链顺序保持为 global -> server -> route -> service -> upstream -> proxy，响应阶段由插件机制处理。
- 新控制面能力放在 `internal/controlplane/`，HTTP contract 放在 `internal/adminapi/`，不要让 Admin handler 直接耦合具体 etcd client。
- 新配置源实现 `provider.Source`，由 bootstrap 选择，不要把 file/etcd 分支散落到 gateway 执行路径。
- 新代理协议或代理行为先从 `proxy.Proxy` 边界进入；不要把 transport 细节写进 `Gateway.ServeHTTP`。
- 新负载均衡策略实现 internal balancer contract，并通过 compiler/balancer factory 接入；对外扩展能力同步考虑 `balancer/` public 包。
- 新插件优先使用 `plugin.RegisterTypedContext[T]` 风格，保持配置结构可验证、handler 可测试。
- Public API 变更包括根包、`plugin/`、`balancer/`，需要格外保守，避免破坏扩展方编译。

## Agent 执行规则

- 改代码前先读相关包的现有测试和 README/docs 中对应契约。
- 修改 `internal/gateway`、`internal/plugin`、`internal/proxy`、`internal/controlplane`、`internal/adminapi` 时，必须检查是否已有 table-driven 测试可扩展。
- 修改配置 schema 时，同步检查 `configs/*.yaml`、Admin/API 文档、usage 示例测试。
- 修改 APISIX 兼容行为时，同步检查 `internal/apisix`、`internal/translate`、`internal/controlplane`、`internal/adminapi` 的契约。
- 不要引入新框架替换 Hertz、urfave/cli、yaml.v3、etcd client、Prometheus client，除非用户明确要求迁移。
- 不要新增 Makefile 或 golangci 配置并假装它们已经是既有质量门；这些目前是待决策项。

## 开发流程

- 格式化：`gofmt -w <files>`。
- 全量测试：`go test ./...`。
- 竞态风险改动：`go test -race ./...`。
- 配置校验：`go run ./cmd/lumen-gateway --config configs/bootstrap.yaml --test`。
- 本地 file 模式运行：`go run ./cmd/lumen-gateway --config configs/bootstrap.yaml`。
- Docker 全栈运行：`docker compose up -d --build`。
- Benchmark 脚本在 `docs/benchmark/` 下；性能改动应记录命令、场景和结果文件。

## 日志约定

- 当前代码实际使用标准库 `log/slog`。
- 日志字段使用结构化 key/value，不拼接动态字符串。
- 网关热重载、配置 watch、Admin 操作失败等可恢复错误记录为 warn 后跳过当前更新；不可启动错误返回给 CLI。
- 不要默认引入 zap 或新增 `internal/platform/logging`；那是其他 skeleton 的约定，不是当前仓库事实。

## 配置约定

- Bootstrap 配置在 `internal/bootstrap.Options`，负责 listen、source、admin key、etcd 和 file path。
- 运行时网关配置在 `internal/config.Options`，包含 servers/routes/services/upstreams/plugins/logging。
- `gateway.source=file` 使用 `configs/lumen.yaml` 类原生配置。
- `gateway.source=etcd_apisix` 使用 APISIX 兼容资源，经 provider/translate 编译成 Lumen runtime options。
- 默认 Admin key 仅用于本地开发：`local-dev-admin-key`。

## 数据库/存储约定

- 当前控制面存储是 etcd，不是 SQL 数据库。
- etcd 访问必须保留在 provider/controlplane store 边界内，业务层通过接口依赖。
- Bundle preview/apply/export/history/rollback 属于 control-plane 行为；不要放进 gateway 请求执行路径。
- 当前项目没有 pgx/PostgreSQL repository 层；`golang-database` 中 SQL/pgx 示例不适合作为默认实现。

## 测试与质量门

- 当前事实质量门：`go test ./...`。
- 改动并发、watch、reload、snapshot、插件链、代理路径时优先补充针对性单元测试；必要时跑 `go test -race ./...`。
- 测试优先使用标准库断言和现有 testdata 风格，不默认引入 testify/goleak。
- 使用示例应尽量由 Go 测试支撑；`docs/USAGE.md` 中示例已有对应 usage tests。
- 当前仓库没有 Makefile 和 `.golangci.yml`；不要把 `make check`、`make arch-check`、`golangci-lint run` 写成必跑命令。

## Skills 使用约定

- 项目上下文：修改本文时使用 `golang-claude-md`。
- 架构/布局：新增目录、feature、module 时使用 `golang-project-layout` + `golang-design-patterns`，但目录边界以本文为准。
- 命名：新增 public API、接口、错误、配置字段时使用 `golang-naming`。
- 测试：新增或调整测试时使用 `golang-testing`。
- 错误处理：改动错误流、包装、边界返回时使用 `golang-error-handling`。
- 并发/热重载：改动 watch、atomic snapshot、goroutine、channel、shutdown 时使用 `golang-concurrency` + `golang-context`。
- 数据存储：改动 etcd store/provider 时使用 `golang-database` 的事务/边界意识，但不要套用 SQL/pgx 默认。
- 可观测性：改动 Prometheus、指标、trace/log 关联时使用 `golang-observability`。
- 安全：处理 Admin key、请求头、文件路径、网络代理、用户输入时使用 `golang-security` + `golang-safety`。

Skill 与本文冲突时，以本文为准。

## Skill 覆盖与禁用项

- 当前项目使用 `slog`，日志任务可参考 `golang-slog`/标准库 slog 约定；不要默认使用 `golang-log-zap` 或迁移到 zap。
- 当前项目没有 Clean Architecture 的 `domain/usecase/repository/delivery` 目录；不要新增 feature 时强行套该分层。
- 当前项目没有 SQL repository；不要默认采用 pgx/v5、migration、record struct 映射 domain。
- 当前项目没有 gotests、golangci-lint、Makefile hooks 配置；可以建议，但不能作为已存在规则执行。

## 改代码时的注意点

- `Gateway.ServeHTTP` 是热路径，避免无必要分配、锁和全局状态读取。
- Plugin 参数来自 YAML/APISIX resources，必须校验类型、默认值和错误消息。
- APISIX 兼容端点要保持资源 envelope、HTTP status、认证头和 bundle 语义稳定。
- 路由匹配优先级、插件作用域顺序、负载均衡权重和 proxy timeout 都是用户可见行为，改动必须有测试。
- Public package 注释要说明扩展方如何接入，不要引用 internal-only 类型作为对外契约。

## 待决策事项

- 是否引入 Makefile，并定义 `check`、`check-full`、`arch-check` 等统一质量门。
- 是否引入 `.golangci.yml`，以及启用哪些 linters。
- 是否保留 `internal/app` 旧 flag-based runner，或统一到根包 `lumen.Run()` 的 urfave/cli 路径。
- 是否系统化配置 slog handler、格式、级别和 request-scoped logger。
- 是否增加 gRPC/WebSocket/更多内置插件，按 README roadmap 逐项落地。
