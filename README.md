# PR Tombstone

> **Dead patches still have something to teach us.**

PR Tombstone 是一个 GitHub App，用于保存 **Closed but not merged Pull Request** 中仍有价值的技术信息——失败原因、审查意见、设计方案与决策上下文——让后来的贡献者不再重复同样的死路。

## 功能特性

- **自动采集**：Webhook 监听未合并关闭的 PR，提取 PR 正文、变更文件、提交、审查、行内评论与时间线事件，生成结构化证据
- **证据优先分析**：确定性规则引擎 + 可插拔 LLM 分析器（`rules` / `openai` / `openai-compatible` / `anthropic`），无真实证据支撑的结论会被丢弃
- **历史相似匹配**：PostgreSQL FTS + pgvector 混合检索，新 PR 打开或更新时自动比对历史 Tombstone，相似度 60–80% 提示相关历史，超过 80% 发出警告
- **决策图谱**：存储 64 维嵌入与类型化关系，可视化仓库的决策脉络
- **React 仪表盘**：仓库管理、Tombstone 检索与详情、历史回填（backfill）、仓库级设置
- **数据治理**：全局保留期清理、安装移除级联删除、补丁落库前脱敏
- **生产就绪**：内嵌事务迁移、作业去重与指数退避重试、Prometheus 指标、结构化请求日志

## 技术栈

| 层 | 技术 |
| --- | --- |
| 后端 | Go 1.26（API Server、队列 Worker、健康检查、Fixture） |
| 数据 | PostgreSQL 16 + pgvector（内嵌迁移，pgvector 缺失时语义检索自动降级） |
| 前端 | React 19、Vite 6、TypeScript、TanStack Query |
| 部署 | Docker Compose、Kubernetes 模板、Vercel（Go Function + Cron Worker） |

## 快速开始

### Docker Compose（推荐）

```powershell
Copy-Item .env.example .env   # 按需填写 GitHub App 与模型配置
docker compose up -d --build
go run ./cmd/fixture          # 可选：注入确定性演示数据
```

启动后访问：

- 仪表盘：http://localhost:5173
- 就绪探针：http://localhost:8080/readyz
- 指标：http://localhost:8080/metrics

Fixture 会创建演示仓库 `fixture-owner/fixture-repository`、Tombstone PR #18331 及配套证据、嵌入与决策关系。停止栈：`docker compose down`。

### 冒烟验证

```powershell
./scripts/smoke.ps1
```

脚本会构建并启动完整栈、注入 fixture 并校验关键页面与接口。

### 源码开发模式

```powershell
docker compose up -d postgres        # 仅启动数据库
go run ./cmd/server                  # 终端 1：API（:8080）
go run ./cmd/worker                  # 终端 2：队列 Worker
cd web; npm install; npm run dev     # 终端 3：前端（:5173，代理 /api）
```

默认 `AI_PROVIDER=rules` 与 `EMBEDDING_PROVIDER=local` 完全确定性，无需任何模型厂商凭证即可运行。

## 配置

所有配置来自环境变量，完整示例见 [.env.example](.env.example)。常用项：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `DATABASE_URL` | 本地 PostgreSQL DSN | PostgreSQL/pgvector 连接串 |
| `HTTP_ADDR` | `:8080` | API 监听地址 |
| `GITHUB_APP_ID` / `GITHUB_APP_SLUG` | 空 | GitHub App ID 与安装链接 slug |
| `GITHUB_PRIVATE_KEY` | 空 | RSA 私钥（支持转义换行） |
| `GITHUB_WEBHOOK_SECRET` | 空 | Webhook HMAC 密钥 |
| `DASHBOARD_TOKEN` | 空 | 仪表盘 Bearer 令牌（设置后数据接口需鉴权） |
| `AI_PROVIDER` | `rules` | 分析器：`rules` / `openai` / `openai-compatible` / `anthropic` |
| `EMBEDDING_PROVIDER` | `local` | 嵌入：`local` 或 OpenAI 兼容端点 |
| `JOB_POLL_INTERVAL` | `2s` | Worker 轮询间隔 |
| `RETENTION_DAYS` | `30` | 全局数据保留天数 |
| `CRON_SECRET` | 空 | Vercel Cron 鉴权密钥 |
| `DB_MAX_OPEN_CONNS` / `DB_MAX_IDLE_CONNS` | `20` / `5` | 连接池上限（Vercel 环境自动降为 `4` / `1`） |

分析器与嵌入模型相互独立配置，聊天模型不会被误发到嵌入端点。完整配置与 API 语义见 [docs/IMPLEMENTATION.md](docs/IMPLEMENTATION.md)。

## API 概览

公开运维端点：`GET /livez`、`GET /readyz`、`GET /api/healthz`、`GET /api/github/install`、`GET /api/github/setup`、`POST /api/github/webhook`。

仪表盘与数据端点（设置 `DASHBOARD_TOKEN` 后需 `Authorization: Bearer`）：

- `GET /api/repositories`、`GET /api/repositories/{id}/history`、`POST /api/repositories/{id}/backfill`、`GET|PUT /api/repositories/{id}/settings`
- `GET /api/tombstones/repository/{id}?q=...`、`GET /api/tombstones/{id}`、`GET /api/tombstones/{id}/related`、`POST /api/tombstones/{id}/reanalyze`、`PUT /api/tombstones/{id}/state`
- `GET /api/graph/repository/{id}`、`GET /api/jobs`、`GET /metrics`

## 部署

### Docker Compose / 容器

根目录 `Dockerfile` 以多阶段构建产出 `server`、`worker`、`healthcheck` 三个二进制（distroless 镜像）；`web/Dockerfile` 产出带安全响应头与 `/api` 反代的 nginx 前端镜像。`docker-compose.yml` 一键编排 postgres + server + worker + web。

### Kubernetes

`deploy/kubernetes.yaml` 为可选的生产模板：替换占位符，并搭配带 pgvector 的托管 PostgreSQL 使用。

### Vercel（Serverless）

仓库内置 Vercel 支持：`api/index.go` 以单个 Go Function 承载全部 API，Vercel Cron 按计划调用 `/api/cron/worker` 驱动有界批处理 Worker，`web/` 构建为静态站点。重写规则、函数限制与 Cron 计划见 [vercel.json](vercel.json)，完整部署指南（托管数据库、环境变量、GitHub App 回调、Deployment Protection 注意事项）见 [docs/VERCEL.md](docs/VERCEL.md)。

## 本地验证与 CI

```powershell
gofmt -w cmd internal api
go vet ./...
go test -race ./...
Push-Location web; npm ci; npm run build; Pop-Location
docker compose config
```

GitHub Actions（`.github/workflows/ci.yml`）在每次 push / PR 执行同样的检查：后端 gofmt + vet + race 测试、前端 `npm ci` + 构建、两个容器镜像构建。

## 项目结构

```text
.
├── api/                 # Vercel Go Function 入口（API + Cron Worker 路由）
├── cmd/
│   ├── server/          # API 服务（本地/容器主入口）
│   ├── worker/          # 队列 Worker（长轮询）
│   ├── healthcheck/     # 容器健康检查二进制
│   └── fixture/         # 确定性演示数据注入
├── internal/
│   ├── analyzer/        # 分析器抽象与实现（rules / LLM）
│   ├── confidence/      # 平台置信度评分
│   ├── config/          # 环境变量配置
│   ├── embedding/       # 嵌入抽象（local / OpenAI 兼容）
│   ├── evidence/        # 证据排序
│   ├── github/          # GitHub App 认证与 REST 客户端
│   ├── httpapi/         # HTTP API 与中间件
│   ├── ingest/          # 采集与分析流水线
│   ├── jobs/            # 作业队列 Worker（含 RunBatch 批处理）
│   ├── model/           # 领域模型
│   ├── observability/   # Prometheus 指标
│   ├── repository/      # PostgreSQL 存储与内嵌迁移
│   ├── similarity/      # 历史相似度评分
│   ├── version/         # 版本信息
│   └── webhook/         # Webhook HMAC 校验
├── web/                 # React + Vite 仪表盘
├── docs/                # 实施、部署与策略文档
├── deploy/              # Kubernetes 模板
├── fixtures/            # Webhook fixture 数据
├── scripts/             # 冒烟脚本（smoke.ps1）
├── Dockerfile           # 后端多阶段镜像
├── docker-compose.yml   # 本地/单机完整编排
└── vercel.json          # Vercel 构建、重写与 Cron 配置
```

## 文档索引

| 文档 | 内容 |
| --- | --- |
| [docs/IMPLEMENTATION.md](docs/IMPLEMENTATION.md) | 运行、配置、API、分析与运维细节 |
| [docs/VERCEL.md](docs/VERCEL.md) | Vercel Serverless 部署指南 |
| [docs/GITHUB_APP.md](docs/GITHUB_APP.md) | GitHub App 创建与权限配置 |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | 架构总览 |
| [docs/PRIVACY.md](docs/PRIVACY.md) / [docs/RETENTION.md](docs/RETENTION.md) / [docs/DELETION.md](docs/DELETION.md) | 隐私、保留与删除策略 |
| [docs/SECURITY.md](docs/SECURITY.md) | 安全模型与威胁分析 |
