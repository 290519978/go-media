# AI Analysis 接口与回调优化执行方案

## 1. 目标与约束

基于当前项目状态，后续改造按以下确认结果执行：

1. 控制接口需要同时支持 HTTP 与 gRPC。
2. 手动停止任务也必须触发 `stopped` 回调，并与异常停止区分。
3. `keepalive` 心跳周期固定为 `60s`。
4. 回调鉴权统一使用 `Authorization token` 方案。

本方案用于后续开发任务分解与验收，不在本次文档提交中直接改代码行为。

---

## 2. 当前问题清单（待修复）

1. `keepalive` 仅有发送函数，无定时触发逻辑。
2. 手动 `stop` 时不触发 `stopped` 回调，只有失败路径会触发。
3. gRPC 请求字段与 `main.py` 实际使用字段不一致（proto 不完整）。
4. 健康检查逻辑存在调用错误风险（`is_ready` 方法调用方式需修正）。
5. 回调鉴权文档与代码实现存在歧义，需要统一到 token 模式。

---

## 3. 接口策略（保留/新增/删除）

### 3.1 保留接口

保留现有控制接口，不删除：

- `POST /api/start_camera`
- `POST /api/stop_camera`
- `GET /api/status`
- `GET /api/health`
- `gRPC: StartCamera / StopCamera / GetStatus / Health.Check`

### 3.2 回调接口（继续保留）

- `POST {callback_url}/events`
- `POST {callback_url}/started`
- `POST {callback_url}/keepalive`
- `POST {callback_url}/stopped`

### 3.3 本轮不新增业务接口

先修复一致性和可靠性，不新增 `update_camera` 等扩展接口。新增接口放在下一阶段评估。

---

## 4. 详细改造任务

## 任务 A：gRPC 与 HTTP 字段能力对齐

目标：同一业务能力在 HTTP 和 gRPC 下都能完整配置。

执行项：

1. 更新 proto（源文件）并重新生成 `analysis_pb2.py`、`analysis_pb2.pyi`、`analysis_pb2_grpc.py`。
2. `StartCameraRequest` 增加并对齐以下字段：
   - `detect_mode`
   - `yolo_threshold`
   - `llm_trigger_threshold`
   - `iou_threshold`
   - `llm_api_url`
   - `llm_api_key`
   - `llm_model`
   - `llm_prompt`
3. `StartCameraRequest` 不再包含 `threshold` 字段，仅保留双阈值：
   - `yolo_threshold`
   - `llm_trigger_threshold`
4. `main.py` 中 gRPC `StartCamera` 参数读取不做旧字段兼容。

验收标准：

1. gRPC 与 HTTP 均可设置 mode=1/2/3 相关参数并生效。
2. 无字段时使用默认值；旧客户端仍能启动任务。

---

## 任务 B：手动停止触发 stopped 回调并区分原因

目标：所有停止路径都有回调，且原因可区分。

执行项：

1. 手动停止（HTTP、gRPC）都发送 `stopped` 回调。
2. `reason` 约定：
   - 手动停止：`user_requested`
   - 帧采集失败：`capture_failed`（已存在）
   - 分析异常：`error`（已存在）
3. `message` 约定：
   - 手动停止固定文案，例如：`"task stopped by user request"`。
   - 异常停止保留原错误信息。

验收标准：

1. `POST /api/stop_camera` 或 gRPC `StopCamera` 后，回调方收到 `stopped`。
2. 三种停止原因在回调体中可明确区分。

---

## 任务 C：keepalive 定时发送（60s）

目标：服务运行期间固定心跳。

执行项：

1. 在服务启动流程中新增心跳线程（daemon）。
2. 周期固定 `60s`，调用 `send_keepalive_callback(stats)`。
3. `stats` 至少包含：
   - `active_streams`
   - `uptime_seconds`
   - `total_detections`（若当前暂未维护，先按现状输出并在文档标注）
4. 服务退出时线程应可安全停止（跟随进程退出即可）。

验收标准：

1. 服务启动后每 60 秒发送一次 `/keepalive`。
2. 回调失败不影响主流程，错误仅记录日志。

---

## 任务 D：回调鉴权统一为 Authorization token

目标：代码与文档一致，接收端实现简单明确。

执行项：

1. 发送回调时统一使用 Header：
   - `Authorization: <token>`
2. `callback_secret` 语义固定为 token。
3. 更新 `README.md` 与 `webhook_openapi.yaml`，移除 Basic Auth 表述。

验收标准：

1. 4 类回调都带统一 `Authorization` 头。
2. 文档与实现无冲突描述。

---

## 任务 E：健康检查与状态一致性修复

目标：控制面状态可靠可用。

执行项：

1. 修复 `HealthServicer.Check` 的 `is_ready` 调用方式。
2. 评估并补齐 `total_detections` 与 `retry_count` 的维护逻辑：
   - 至少保证 `status` 返回数据语义准确。

验收标准：

1. 模型未就绪返回 `NOT_SERVING`，就绪返回 `SERVING`。
2. `status` 字段与实际运行情况一致。

---

## 5. 执行顺序（建议）

1. 任务 A（协议对齐）
2. 任务 B（停止回调）
3. 任务 C（60s 心跳）
4. 任务 D（鉴权统一）
5. 任务 E（健康检查与状态一致性）
6. 文档回归（README + webhook_openapi + PROJECT_MAP）

---

## 6. 联调与测试清单

1. 启动服务后收到一次 `/started`。
2. 启动单路与多路任务，验证 `events` 正常。
3. 每 60 秒收到 `/keepalive`。
4. 手动 stop 收到 `stopped(reason=user_requested)`。
5. 构造拉流失败，收到 `stopped(reason=capture_failed)`。
6. 构造分析异常，收到 `stopped(reason=error)`。
7. gRPC 与 HTTP 两套调用参数保持等效行为。
8. 校验回调请求头都包含 `Authorization`。

---

## 7. 交付物清单（后续任务完成时应更新）

1. 代码文件：
   - `ai-analysis/main.py`
   - `ai-analysis/http_api.py`（如涉及）
   - `ai-analysis/analysis_pb2.py`
   - `ai-analysis/analysis_pb2.pyi`
   - `ai-analysis/analysis_pb2_grpc.py`
   - proto 源文件（若在仓库中）
2. 文档文件：
   - `ai-analysis/README.md`
   - `ai-analysis/webhook_openapi.yaml`
   - `ai-analysis/PROJECT_MAP.md`
