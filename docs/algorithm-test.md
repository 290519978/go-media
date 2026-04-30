# 算法测试说明（当前实现）

更新时间：2026-04-02  
本文描述算法管理测试与 `camera2` 草稿测试的真实链路与字段契约。

## 1. 目标与范围

算法测试支持：

- 多图片
- 多视频
- 图片+视频混合上传

本模块描述两条测试链路：

- 算法管理 -> 测试：正式算法测试，保留测试记录
- `camera2` 大屏 -> 新增算法 -> 开始测试：草稿算法测试，不保留测试记录

两条链路都不覆盖在线任务实时分析链路。

## 2. 核心链路

关键代码：

- `internal/server/algorithms.go`
- `internal/server/algorithm_test_jobs.go`
- `internal/ai/client.go`
- `ai-analysis/http_api.py`

执行流程：

1. 前端 `POST /api/v1/algorithms/:id/test` 上传 `multipart/form-data`
2. Go 保存媒体到 `configs/test/YYYYMMDD/<batch>/...`
3. Go 创建 job 与 item（数据库）
4. Go 后台异步执行每个 item，调用 AI HTTP
5. 前端轮询 job 接口增量获取结果

`camera2` 草稿测试执行流程：

1. 前端 `POST /api/v1/algorithms/draft-test` 上传 `multipart/form-data`
2. Go 直接按表单字段构造临时算法配置与临时提示词
3. Go 仅在内存中维护草稿 job 状态，不写 `Algorithm` / `AlgorithmPromptVersion` / `AlgorithmTestRecord`
4. Go 保存测试媒体到 `configs/test/YYYYMMDD/<batch>/...`
5. 前端轮询 `GET /api/v1/algorithms/draft-test-jobs/:job_id`

## 3. 并发与同步模型

### 前端

- 创建任务后立即返回 `job_id`
- 每约 1.5 秒轮询一次任务状态
- 上传前会先按后端下发的限制做本地校验：
  - 图片最多 `AlgorithmTestImageMaxCount`
  - 视频最多 `AlgorithmTestVideoMaxCount`
  - 视频大小最多 `AlgorithmTestVideoMaxBytes`
- 当前上传批次的图片结果会优先使用浏览器基于 `File` 生成的本地 `blob:` 预览地址，不依赖 `/api/v1/algorithms/test-media/*` 才能显示异常图片
- 历史测试记录、页面刷新后的旧结果以及视频预览仍按 `media_url` / `test-media` 访问后端媒体文件

### Go 后端

- 正式算法测试 job 异步执行
- 正式算法测试 item 改为 job 内并发执行：
  - 总并发最多 `5`
  - 视频并发最多 `1`
  - 图片与视频共享总槽位，混合上传时总请求数不会超过 `5`
- 正式算法测试图片分项在首轮失败后，若命中可恢复错误，会在 job 内统一补跑 `1` 轮：
  - Go -> AI 图片接口 `connect / read / timeout`
  - AI 已返回，但图片结果里的大模型连接失败，如 `Connection error.`
  - 补跑期间 item 会继续保持 `running`，轮询结果显示“自动重试中”
  - 仅按最终结果落一次 `AlgorithmTestRecord`，避免首轮失败与补跑成功产生双记录
- 草稿算法测试仍按 item 串行执行
- 图片/视频测试遇到 AI HTTP 瞬时传输错误时，会按 `1s / 2s` 退避最多重试到第 `3` 次

### AI 服务

- HTTP 支持并发接入（ThreadingHTTPServer）
- 单请求内部同步处理并同步返回

## 4. 媒体保护与重试

### 4.1 活跃测试媒体保护

- `pending/running` 的算法测试分项会保护对应 `media_path`
- 例行保留期清理与软水位压缩都会跳过这些媒体
- 如果保护集加载失败，该轮 `configs/test` 清理会直接跳过

### 4.2 算法测试 AI 重试

- 仅算法管理测试链路启用该重试逻辑
- 单次 AI 请求内仅对传输级瞬时错误重试：`connect`、`read`
- 正式算法测试图片在首轮结束后，还会对可恢复失败统一补跑 `1` 次：
  - 传输级 `connect`、`read`、`timeout`
  - LLM `call_status=error` 且错误文本为连接失败（如 `Connection error.`）
- `empty_content`、配置/提示词/限额错误、AI `4xx` / 业务校验失败等明确非瞬时失败不纳入统一补跑
- 每次重试都使用独立单次超时，不复用上一次 attempt 的超时上下文

## 5. 外部 API（Go 对前端）

### 4.1 `POST /api/v1/algorithms/:id/test`

当前主用：`multipart/form-data`

请求字段：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `files` | file[] | 是 | 上传文件列表 |
| `camera_id` | string | 否 | 可选 |

上传限制：

- 图片最多 `Server.AI.AlgorithmTestImageMaxCount`
- 视频最多 `Server.AI.AlgorithmTestVideoMaxCount`
- 视频大小最多 `Server.AI.AlgorithmTestVideoMaxBytes`

响应字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `job_id` | string | 任务 ID |
| `batch_id` | string | 批次 ID |
| `algorithm_id` | string | 算法 ID |
| `status` | string | 初始状态 |
| `total_count` | int | 文件总数 |

### 4.2 `GET /api/v1/algorithms/test-limits`

响应字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `image_max_count` | int | 图片最大上传数 |
| `video_max_count` | int | 视频最大上传数 |
| `video_max_bytes` | int64 | 视频大小上限（字节） |

### 4.3 `GET /api/v1/algorithms/test-jobs/:job_id`

响应字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `job_id` | string | 任务 ID |
| `batch_id` | string | 批次 ID |
| `algorithm_id` | string | 算法 ID |
| `status` | string | `pending/running/completed/partial_failed/failed` |
| `total_count` | int | 总数 |
| `success_count` | int | 成功数 |
| `failed_count` | int | 失败数 |
| `items` | array | 分项结果 |

`items[]` 字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `job_item_id` | string | 分项 ID |
| `sort_order` | int | 上传顺序 |
| `status` | string | `pending/running/success/failed` |
| `record_id` | string | 测试记录 ID |
| `file_name` | string | 文件名 |
| `media_type` | string | `image` / `video` |
| `success` | bool | 是否成功 |
| `conclusion` | string | 结论 |
| `basis` | string | 判定依据 |
| `media_url` | string | 媒体访问地址 |
| `normalized_boxes` | array | 图片框（`x/y` 为归一化中心点，`w/h` 为归一化宽高） |
| `anomaly_times` | array | 视频异常时间 |
| `duration_seconds` | number | 视频时长 |
| `error_message` | string | 失败原因；成功分项为空 |

说明：不再返回 `snapshot_width`、`snapshot_height`。

补充说明：

- `media_url` 仍会继续返回，主要用于历史测试记录、刷新后的旧结果以及视频媒体访问
- 当前页面会话里的当前上传批次图片，前端会优先使用本地 `blob:` 预览并叠加框选结果

### 4.4 `POST /api/v1/algorithms/draft-test`

请求字段：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `name` | string | 是 | 草稿算法名称 |
| `description` | string | 否 | 草稿算法描述 |
| `prompt` | string | 是 | 临时提示词 |
| `detect_mode` | string | 是 | 当前固定只支持 `2` |
| `files` | file[] | 是 | 上传文件列表 |
| `camera_id` | string | 否 | 可选 |

说明：

- 该接口仅用于 `camera2` 新增算法弹窗的草稿测试
- 结果结构与正式 job 尽量一致
- 不写测试记录，不进入“算法管理 -> 测试记录”
- 当前批次异常图片在前端优先使用本地 `blob:` 预览地址展示，不依赖测试媒体服务接口回显

### 4.5 `GET /api/v1/algorithms/draft-test-jobs/:job_id`

返回字段与 `GET /api/v1/algorithms/test-jobs/:job_id` 一致，但 `record_id` 为空，且数据只短期保存在内存中。

### 4.6 `GET /api/v1/algorithms/:id/tests`

返回测试历史列表，包含：

- `media_type`
- `media_url`
- `file_name`
- `batch_id`
- `conclusion`
- `basis`
- `normalized_boxes`
- `anomaly_times`
- `duration_seconds`

图片历史列表会按 `response_payload` 重新解释图片测试结果：

- `LLMOnly / Hybrid` 模式下，如果最终判定依赖大模型，且 `llm_usage.call_status = error / empty_content` 或 `llm_result` 为空，则历史列表会直接按失败返回
- 不再回退成 `person x1 / person(0.90)` 这类小模型摘要

### 4.7 `DELETE /api/v1/algorithms/:id/tests`

清空算法测试记录，并删除关联测试媒体文件。

### 4.8 `GET /api/v1/algorithms/test-media/*path`

按相对路径访问测试媒体文件（根目录：`configs/test`）。

说明：

- 历史测试记录与页面刷新后的旧结果继续使用该接口访问媒体
- 当前页面会话里新上传的图片测试结果，前端优先展示本地 `blob:` 预览，仅在没有本地预览时再回退到该接口

## 6. 内部 AI 接口契约（Go -> AI）

### 5.1 `POST /api/analyze_image`

请求字段（当前有效）：

- `image_rel_path`
- `algorithm_configs`
- `llm_api_url`
- `llm_api_key`
- `llm_model`
- `llm_prompt`

响应字段（当前有效）：

- `success`
- `message`
- `detections`
- `algorithm_results`
- `llm_result`
- `llm_usage`

已移除：

- request：`camera_id`
- response：`camera_id`、`snapshot`、`snapshot_width`、`snapshot_height`

### 5.2 `POST /api/analyze_video_test`

请求字段（当前有效）：

- `video_rel_path`
- `fps`
- `algorithm_configs`
- `llm_api_url`
- `llm_api_key`
- `llm_model`
- `llm_prompt`

响应字段（当前有效）：

- `success`
- `message`
- `llm_result`
- `llm_usage`

已移除：

- request：`camera_id`、`mime_type`、`duration_seconds`
- response：`camera_id`、`duration_seconds`、`conclusion`、`basis`、`anomaly_times`

## 7. 解析职责（已下沉到 Go）

### 视频测试

Go 从 `llm_result` 解析：

- `alarm`
- `reason`
- `anomaly_times`

并组装页面字段：

- `conclusion`
- `basis`
- `anomaly_times`

### 图片测试

图片结果仍由 Go 统一组装：

- `conclusion`
- `basis`
- `normalized_boxes`

图片最终结果语义：

- `LLMOnly`：只要 `llm_usage.call_status = error / empty_content`，或 `llm_result` 为空，就按失败返回
- `Hybrid`：如果只是“小模型未命中，因此未触发 LLM”，仍返回 `小模型未检出目标`
- `Hybrid`：如果本次已经需要 LLM 最终判定，但 LLM 失败或未返回有效结果，则按失败返回，不再回退小模型摘要

框来源规则：

- 模式 1：若已有 LLM 框则优先展示，否则回退 YOLO
- 模式 2：仅 LLM 框
- 模式 3：仅 LLM 框；LLM 失败时不再回退 YOLO 摘要

## 8. 媒体存储与清理

测试媒体统一目录：

- `configs/test/YYYYMMDD/<batch>/...`

数据库保存相对路径，例如：

- `20260326/<batch>/sample.jpg`

删除时机：

- 用户清空测试记录时删除对应媒体
- 存储清理策略触发时清理过期/压缩数据
- 草稿测试同样写入该目录，但不生成测试历史记录，依赖统一清理策略回收

## 9. 兼容与历史说明

- 数据库已有列（如 `snapshot_width/snapshot_height`）暂不做迁移删除
- 新接口不再输出这些字段
- 历史记录解析按 `media_type` 分流；旧 payload 与新 payload 均可解析
- 历史图片记录如果 payload 内已经体现 `llm_usage.call_status=error/empty_content` 或 `llm_result` 为空，会在历史列表阶段纠偏为失败语义；不做数据库回填

## 10. 排查入口

- Go -> AI 本地端口问题、AI 容器 -> DashScope HTTPS 问题的分层排查，见 [AI 服务 LLM 故障排查](./ai-troubleshooting.md)

## 11. LLM Token 配额拦截

补充说明：
- `config.toml -> [Server.AI].AnalyzeImageFailureRetryCount` 用于控制图片分析可恢复失败的额外补跑轮数，默认 `1`，`0` 表示关闭。
- 该配置同时作用于正式算法测试图片 job 与 `camera2` 草稿测试图片 job。
- 单次 AI HTTP 请求内部的瞬时重试逻辑保持不变；这里配置的是 job/item 级统一补跑轮数。

算法测试入口现在会在真正调用 AI 服务前，先检查 Go 侧维护的 LLM token 总配额。

启用条件：

- `Server.AI.DisableOnTokenLimitExceeded = true`
- `Server.AI.TotalTokenLimit > 0`
- `mb_llm_usage_calls.total_tokens` 的累计值达到或超过配置上限

触发后行为：

- 图片测试接口直接返回 `400`
- 视频测试接口直接返回 `400`
- 已创建的批量测试任务会把对应分项标记为失败，不再调用 AI 服务
- 草稿测试同样会被拦截，避免在超限状态下继续消耗外部识别资源

统一失败提示：

- `LLM 总 token 已达到限制，AI 识别已禁用，请调整配置后手动重启任务`

解除限制后，不会自动重跑之前失败或被拦截的测试任务；如需继续验证，需要用户重新发起测试。
