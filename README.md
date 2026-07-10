# switch-manager

公司内网交换机统一管理服务。

V1 使用 Go 开发，通过统一 REST API 管理华为与 H3C 交换机，并提供任务调度、设备级串行锁、RBAC、配置备份和审计能力。

## 当前阶段

正在实施 `TASK-001：项目骨架`。当前只包含：

- 严格配置加载与校验
- 结构化日志
- HTTP 服务
- `/health/live`
- `/health/ready`
- context 驱动的优雅关闭
- Go test、race test、vet CI

本阶段不包含数据库、业务 API、SSH 或任何真实厂商命令。

## 本地运行

```bash
go run ./cmd/server -config configs/config.example.yaml
```

默认地址：`127.0.0.1:8080`。

```bash
curl http://127.0.0.1:8080/health/live
curl http://127.0.0.1:8080/health/ready
```

## 检查

```bash
make check
```

## 配置覆盖

支持以下环境变量：

```text
SWITCH_MANAGER_SERVER_LISTEN
SWITCH_MANAGER_DATABASE_DSN
SWITCH_MANAGER_DATABASE_REQUIRED
SWITCH_MANAGER_LOG_LEVEL
SWITCH_MANAGER_LOG_FORMAT
```

TASK-001 使用一个严格的标量 YAML 子集，遇到未知配置项会拒绝启动。后续若替换为完整 YAML 库，必须作为独立变更并保留现有验证行为。
