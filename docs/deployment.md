# maas-box 部署文档 (极简)

## 1. 部署模式

- 本地开发（推荐）: Go 本机 + AI Docker host 变体 + ZLM 容器
- 本地对比方案: Go 本机 + AI/ZLM 容器 (bridge + ports)
- 线上同机: Go 本机 + AI/ZLM 同机容器 (network_mode: host)

## 2. 关键文件

- deploy/env/local.env: 本地运行时变量
- deploy/env/prod.env: 线上运行时变量
- docker-compose.ai.local.host.yml / docker-compose.zlm.local.yml: 本地默认 compose (AI host + ZLM)
- docker-compose.ai.local.yml: 本地 Docker 对比 compose (bridge + ports)
- docker-compose.ai.yml / docker-compose.zlm.yml: 线上 compose
- configs/local/config.toml: 本地 Go 配置
- configs/prod/config.toml: 线上 Go 配置
- configs/local/zlm.ini: 本地 ZLM 配置
- configs/prod/zlm.ini: 线上 ZLM 配置
- configs/: 共享静态资源目录（llm、zlm-www、events、tests、cover、device_snapshots、yolo-label.json）

## 3. 环境变量

- `deploy/env/local.env` 与 `deploy/env/prod.env` 仅作为 compose 预留覆盖文件，可为空。
- 当前基线中：
  - Windows 本地默认用 `docker-compose.ai.local.host.yml` 启动 AI，回调与 AI 输入地址按 `127.0.0.1` 对齐
  - Docker AI 回调地址/Token 固定写在 `docker-compose.ai.local.host.yml`、`docker-compose.ai.local.yml`、`docker-compose.ai.yml`
  - Go 的 AI/ZLM 访问参数固定写在 `configs/local/config.toml`、`configs/prod/config.toml`
- `configs/local/config.toml` 当前默认：
  - `Server.AI.ServiceURL = "http://127.0.0.1:50052"`
  - `Server.AI.CallbackURL = "http://127.0.0.1:15123/ai"`
  - `Server.ZLM.AIInputHost = "127.0.0.1"`
- 线上请保持 `configs/prod/config.toml` 的 `Server.ZLM.PlayHost` 与 `configs/prod/zlm.ini` 的 `externIP` 一致。

## 4. 启动与停止

### 4.0 Windows 本地 AI host 前置条件

- Docker Desktop `4.34+`
- 已在 Docker Desktop `Settings -> Resources -> Network` 中启用 `Enable host networking`
- 当前必须运行 Linux containers
- 宿主机 `50051`、`50052` 端口不能被其他进程占用
- `docker-compose.ai.local.host.yml` 中的 AI 回调地址固定为 `http://127.0.0.1:15123/ai`
- 该模式用于本地默认联调，避免继续依赖 `bridge + ports` 端口转发链路

### 4.1 本地推荐启动（Go 本机 + AI Docker host + ZLM 容器）

Windows PowerShell:

```powershell
docker compose --env-file deploy/env/local.env -f docker-compose.ai.local.host.yml up -d --build
docker compose --env-file deploy/env/local.env -f docker-compose.zlm.local.yml up -d
go run main.go -config ./configs/local/config.toml
```

说明：

- AI host 模式下，Go -> AI 与 AI -> Go 都走 `127.0.0.1`
- 在线任务默认会把下发给 AI 的 RTSP 地址改写到 `127.0.0.1`
- 如需回到 `bridge + ports` 方式，可改用 `docker-compose.ai.local.yml`

### 4.2 本地推荐停止

Windows PowerShell:

```powershell
docker compose --env-file deploy/env/local.env -f docker-compose.ai.local.host.yml down
docker compose --env-file deploy/env/local.env -f docker-compose.zlm.local.yml down
```

### 4.3 本地 Docker 对比方案启动（bridge + ports）

```bash
docker compose --env-file deploy/env/local.env -f docker-compose.ai.local.yml up -d --build
docker compose --env-file deploy/env/local.env -f docker-compose.zlm.local.yml up -d
```

### 4.4 本地 Docker 对比方案停止

```bash
docker compose --env-file deploy/env/local.env -f docker-compose.ai.local.yml down
docker compose --env-file deploy/env/local.env -f docker-compose.zlm.local.yml down
```

### 4.5 生产启动

```bash
docker compose --env-file deploy/env/prod.env -f docker-compose.ai.yml up -d --build
docker compose --env-file deploy/env/prod.env -f docker-compose.zlm.yml up -d
```

### 4.6 生产停止

```bash
docker compose --env-file deploy/env/prod.env -f docker-compose.ai.yml down
docker compose --env-file deploy/env/prod.env -f docker-compose.zlm.yml down
```

## 5. 打包 AI arm64 tar 镜像

```bash
docker buildx create --name maas-builder --use --bootstrap
docker buildx build --platform linux/arm64 -f Dockerfile.ai -t maas-box/ai:arm64 --output type=docker,dest=maas-box-ai-arm64.tar .
```

说明：
- `Dockerfile.ai` 默认基础镜像已切到 `m.daocloud.io/docker.io/library/python:3.11-slim`
- 这样可以避开 Docker Hub 认证与 manifest 拉取的间歇性超时，和本地 compose 构建默认值保持一致
- 如需显式覆盖基础镜像，可追加 `--build-arg PYTHON_BASE_IMAGE=<镜像地址>`

如需强制使用 Docker Hub 官方基础镜像，可执行：

```bash
docker buildx build --platform linux/arm64 -f Dockerfile.ai -t maas-box/ai:arm64 --build-arg PYTHON_BASE_IMAGE=python:3.11-slim --output type=docker,dest=maas-box-ai-arm64.tar .
```

导入到边沿盒子：

```bash
docker load -i maas-box-ai-arm64.tar
```

## 6. 构建 Go+Web 单二进制

### 6.1 生成前端 dist

```bash
cd web
npm ci
npm run build
cd ..
```

### 6.2 构建 Linux 可执行文件

使用 Makefile：

```bash
make build-linux-amd64-web
make build-linux-arm64-web
```

或直接执行：

```bash
set GOOS=linux&& set GOARCH=amd64&& set CGO_ENABLED=0&& go build -tags embed_web -o build/maas-box-linux-amd64 ./main.go ./web_assets_embed.go
set GOOS=linux&& set GOARCH=arm64&& set CGO_ENABLED=0&& go build -tags embed_web -o build/maas-box-linux-arm64 ./main.go ./web_assets_embed.go
```

产物路径：

- `build/maas-box-linux-amd64`
- `build/maas-box-linux-arm64`

## 7. 运行单二进制

```bash
./maas-box-linux-arm64 -config ./configs/prod/config.toml
```

> 本地开发可改为 `./configs/local/config.toml`。

## 8. Go 进程启动 (源码运行示例)

### 8.1 本地开发 (local/config.toml)

Linux/macOS:

```bash
// 配置国内镜像
go env -w GOPROXY=https://mirrors.aliyun.com/goproxy/,direct

go run main.go -config ./configs/local/config.toml
```

Windows PowerShell:

```powershell
go run main.go -config ./configs/local/config.toml
```

### 8.2 线上同机 (prod/config.toml)

Linux/macOS:

```bash
go run main.go -config ./configs/prod/config.toml
```

Windows PowerShell:

```powershell
go run main.go -config ./configs/prod/config.toml
```

> 说明：当前生产仍沿用 `./configs/data.db`、`./configs/recordings`、`./configs/recordings-buffer`，本次仅做配置目录分层，不迁历史数据。

## 9. 验收清单

1. http://127.0.0.1:15123 (或盒子地址) 可访问
2. http://127.0.0.1:50052/api/status 与 http://127.0.0.1:50052/api/health (AI) 可访问
3. http://127.0.0.1:11029/index/api/getServerConfig?secret=<secret> (ZLM) 可访问
4. AI 回调 (/ai/events|/ai/started|/ai/stopped|/ai/keepalive) 入库正常
5. ZLM webhook (/webhook/on_publish|/webhook/on_server_keepalive) 返回正常
6. 图片/视频算法测试不再出现 `127.0.0.1:50052 connectex actively refused`
7. `docker-compose.ai.local.host.yml` 与 `docker-compose.ai.local.yml` 均可正常执行 `docker compose ... config`

## 10. 清理业务数据 (data.db)

脚本会清理以下数据：
- 设备
- 区域（保留并重建 `root`）
- LLM 用量统计
- 视频任务
- 报警记录
- 算法测试记录 / Job / Job 明细

说明：
- 当前脚本只清理 `data.db` 内的业务数据
- 不会删除 `configs/test` 等算法测试媒体文件

执行前请先停止 Go/AI/ZLM，避免数据库锁冲突。

Linux/macOS:

```bash
bash scripts/cleanup_business_data.sh ./configs/data.db
```

Windows PowerShell:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/cleanup_business_data.ps1 -DbPath .\configs\data.db
```

本地环境若使用 `configs/local/data.db`，将 `DbPath` 替换为对应路径即可。

## 11. LLM Token 限额配置

`[Server.AI]` 新增并明确了以下配置：

- `TotalTokenLimit`
  - LLM 总 token 上限
  - `<= 0` 表示不启用该限额控制
- `DisableOnTokenLimitExceeded`
  - 是否在达到或超过 `TotalTokenLimit` 后禁用 AI 识别
  - 默认值：`false`

仅当以下条件同时满足时，配额守卫才会生效：

- `DisableOnTokenLimitExceeded = true`
- `TotalTokenLimit > 0`
- 当前累计 `total_tokens >= TotalTokenLimit`

生效后会立即停止当前运行中的 AI 任务，并拦截新的 AI 识别请求。调整配置解除限制后，系统不会自动恢复之前被停掉的任务，需要手动重新启动。
