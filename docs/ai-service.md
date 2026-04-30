# AI 服务说明（当前实现）

更新时间：2026-03-27  
本文描述当前代码真实行为，优先级高于历史记录。

## 1. 模块定位

`ai-analysis` 提供两类能力：

- 在线任务能力（摄像头启停、状态、实时检测）
- 算法测试能力（图片测试、视频测试）

职责边界：

- Go 服务负责组装业务 Prompt（在线任务、图片测试、视频测试）
- AI 服务只负责：接收 `llm_prompt`、调用模型、返回原始模型结果（`llm_result`）和用量（`llm_usage`）

## 1.1 本地推荐运行方式

- Windows 本地开发默认通过 `docker-compose.ai.local.host.yml` 启动 AI 服务
- 本地默认配置配套为：
  - Go -> AI：`http://127.0.0.1:50052`
  - AI -> Go：`http://127.0.0.1:15123/ai`
  - AI 拉取 ZLM RTSP 输入主机：`127.0.0.1`
- `docker-compose.ai.local.yml` 保留为 `bridge + ports` 对比方案，不作为本地默认路径

## 2. 启动参数

主入口：`ai-analysis/main.py`

Windows 本地推荐启动：

```bash
docker compose --env-file deploy/env/local.env -f docker-compose.ai.local.host.yml up -d --build
```

如需对比 `bridge + ports` 方式，可改用：

```bash
docker compose --env-file deploy/env/local.env -f docker-compose.ai.local.yml up -d --build
```

常用参数：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--port` | `50051` | gRPC 端口 |
| `--http-port` | `50052` | HTTP 端口 |
| `--model` | `yolo.onnx` | 小模型权重路径 |
| `--callback-url` | `http://127.0.0.1:15123/ai` | 回调地址 |
| `--callback-secret` | 空 | 回调密钥 |
| `--keepalive-interval` | `60` | keepalive 间隔（秒） |
| `--log-level` | `INFO` | 日志级别 |
| `--algorithm-test-root` | 自动推断 | 算法测试媒体根目录 |
| `--log-dir` | 自动推断 | AI 日志目录 |

`--algorithm-test-root` 默认会尝试：

1. `ai-analysis/configs/test`
2. `../configs/test`

本地 Docker compose 默认会显式传入：

- `--algorithm-test-root /app/configs/test`
- `--log-dir /app/configs/logs/ai`
- `--callback-url http://127.0.0.1:15123/ai`

## 3. 并发与同步模型

### HTTP

- 使用 `ThreadingHTTPServer`
- 支持并发接入
- 单个请求内部仍是同步阻塞处理
- 无流式响应、无任务队列、无异步回调

### gRPC

- 使用 `ThreadPoolExecutor(max_workers=20)`
- 最大并发 worker 为 `20`
- 单个 RPC 内部为同步阻塞处理

## 4. HTTP 接口

### 4.1 `POST /api/start_camera`

请求字段：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `camera_id` | string | 是 | 摄像头 ID |
| `rtsp_url` | string | 是 | RTSP 地址 |
| `callback_url` | string | 否 | 回调地址 |
| `callback_secret` | string | 否 | 回调密钥 |
| `detect_rate_mode` | string | 否 | `fps` / `interval` |
| `detect_rate_value` | int | 否 | 抽帧参数 |
| `retry_limit` | int | 否 | 重试上限 |
| `llm_api_url` | string | 否 | LLM API URL |
| `llm_api_key` | string | 否 | LLM API Key |
| `llm_model` | string | 否 | LLM 模型 |
| `llm_prompt` | string | 否 | 已组装的最终 Prompt |
| `algorithm_configs` | array | 是 | 算法配置列表 |

成功返回：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `success` | bool | 是否成功 |
| `message` | string | 结果信息 |
| `camera_id` | string | 摄像头 ID |
| `source_width` | int | 源宽 |
| `source_height` | int | 源高 |
| `source_fps` | number | 源帧率 |

### 4.2 `POST /api/stop_camera`

请求字段：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `camera_id` | string | 是 | 摄像头 ID |

返回字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `success` | bool | 是否成功 |
| `message` | string | 结果信息 |
| `camera_id` | string | 摄像头 ID |

### 4.3 `POST /api/analyze_image`

用途：算法测试图片分析。

请求字段（当前有效）：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `image_rel_path` | string | 是 | 相对 `configs/test` 的图片路径 |
| `algorithm_configs` | array | 是 | 算法配置 |
| `llm_api_url` | string | 否 | LLM API URL |
| `llm_api_key` | string | 否 | LLM API Key |
| `llm_model` | string | 否 | LLM 模型 |
| `llm_prompt` | string | 否 | 已组装的最终 Prompt |

返回字段（当前有效）：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `success` | bool | 是否成功 |
| `message` | string | 结果信息 |
| `detections` | array | 小模型检测结果 |
| `algorithm_results` | array | 按算法聚合结果 |
| `llm_result` | string | 原始 LLM 返回 |
| `llm_usage` | object/null | LLM 用量信息 |

已删除字段（不再接收/返回）：

- request：`camera_id`
- response：`camera_id`、`snapshot`、`snapshot_width`、`snapshot_height`、`detect_mode`

### 4.4 `POST /api/analyze_video_test`

用途：算法测试视频分析。

请求字段（当前有效）：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `video_rel_path` | string | 是 | 相对 `configs/test` 的视频路径 |
| `fps` | int | 否 | 视频分析 FPS |
| `algorithm_configs` | array | 是 | 算法配置 |
| `llm_api_url` | string | 否 | LLM API URL |
| `llm_api_key` | string | 否 | LLM API Key |
| `llm_model` | string | 否 | LLM 模型 |
| `llm_prompt` | string | 否 | 已组装的最终 Prompt |

返回字段（当前有效）：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `success` | bool | 是否成功 |
| `message` | string | 结果信息 |
| `llm_result` | string | 原始 LLM 返回 |
| `llm_usage` | object/null | LLM 用量信息 |

已删除字段（不再接收/返回）：

- request：`camera_id`、`mime_type`、`duration_seconds`
- response：`camera_id`、`duration_seconds`、`conclusion`、`basis`、`anomaly_times`

说明：视频业务字段 `conclusion/basis/anomaly_times` 由 Go 服务解析 `llm_result` 生成。

### 4.5 `GET /api/status`

返回字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `is_ready` | bool | 模型是否就绪 |
| `cameras` | array | 摄像头任务状态 |
| `stats` | object | 服务统计信息 |

### 4.6 `GET /api/health`

返回字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `status` | string | `SERVING` / `NOT_SERVING` |

## 5. gRPC 接口（概要）

协议定义：`ai-analysis/analysis.proto`

当前主要方法：

- `StartCamera`
- `StopCamera`
- `GetStatus`
- `AnalyzeImage`（在线链路的历史兼容能力）

说明：算法管理的图片/视频测试主链路走 HTTP，不走 gRPC。

## 6. 媒体路径与日志

Docker 挂载（推荐）：

- `./configs:/app/configs`

Docker 挂载到容器内时：

- 测试媒体目录使用 `/app/configs/test`
- AI 日志目录使用 `/app/configs/logs/ai`

关键目录：

- 测试媒体：`configs/test`
- AI 日志：`configs/logs/ai/analysis.log`

Go -> AI 在算法测试中只传相对路径，不传大块媒体 base64。

LLM 失败日志会补充以下诊断字段：

- `failure_type`：`connect | timeout | tls | provider_status | empty_content | unknown`
- `exception_type`
- `provider_host`
- `base_url`
- `call_id`
- `context`

分层排查命令与判定矩阵见 [AI 服务 LLM 故障排查](./ai-troubleshooting.md)。

## 7. 错误语义

HTTP 接口统一约定：

- 参数问题：`400`
- 服务未就绪：`503`
- 业务失败：`400` + `{ "success": false, "message": "..." }`
- 未捕获异常：`500` + `{ "success": false, "message": "AI 服务内部错误" }`

此外，AI HTTP 层已加统一异常兜底，异常时优先返回 JSON 错误，而不是直接断连。

## 8. LLM Token 限额守卫

Go 侧现在会统一统计 `mb_llm_usage_calls.total_tokens` 的累计值，统计口径覆盖：

- `task_runtime`
- `algorithm_test`
- `direct_analyze`

当同时满足以下条件时，会启用 LLM token 限额守卫：

- `Server.AI.DisableOnTokenLimitExceeded = true`
- `Server.AI.TotalTokenLimit > 0`
- 当前累计 `total_tokens >= TotalTokenLimit`

触发后端行为如下：

- 立即停止当前仍在运行的 AI 任务
- 关闭这些任务的自动恢复意图，避免超限后被后台自动拉起
- 拦截新的 AI 识别请求，统一返回 `400`
- 统一错误文案为：`LLM 总 token 已达到限制，AI 识别已禁用，请调整配置后手动重启任务`

当前会被拦截的入口：

- 在线任务启动
- 启动期自动恢复和断线自动恢复
- 算法测试图片/视频分析
- `camera2` 巡检任务创建，以及巡检执行阶段的图片分析

以下链路不受该守卫影响：

- AI 回调接收
- AI 状态查询
- 用量统计与页面读取

解除限制后，系统只会恢复“允许调用 AI”的状态，不会自动恢复之前被停掉的任务；需要管理员手动重新启动任务。

## 9. LLM 配额主动提醒

当 Go 侧检测到 LLM 总 token 已达到限制后，除了继续拦截 AI 识别请求，还会额外生成一条 LLM 配额提醒。

提醒机制与现有存储清理提醒保持一致：

- 后端会持久化当前提醒状态
- 登录后可通过 `/api/v1/auth/me` 回放
- 运行中通过 `/ws/alerts` 实时推送

提醒载荷字段统一为：

- `type = "llm_quota_notice"`
- `notice_id`
- `issued_at`
- `token_total_limit`
- `used_tokens`
- `title`
- `message`

当前默认提示文案为：

- 标题：`LLM 配额提醒`
- 内容：`LLM 总 token 已达到限制，AI 识别已禁用。请前往 LLM 用量统计调整限额配置，解除限制后手动重启任务。`

当前展示端包括：

- 主系统 `AppShell`
- `camera2` 大屏独立页面

同一次登录内刷新页面不会重复弹出；用户重新登录后，如果系统仍处于超限状态，会再次提示一次。解除限制后，该提醒会自动消失。
