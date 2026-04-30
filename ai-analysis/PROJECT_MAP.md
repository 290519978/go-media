# AI 分析服务项目地图

## 1. 项目定位
- 本项目用于 RTSP 视频流实时分析，也支持单张 base64 图片分析。
- AI 服务主体位于 `ai-analysis/`，使用 Python 实现。
- 对外同时提供 HTTP 与 gRPC 两套控制接口。
- 服务会向业务侧回调分析结果与状态事件（`/events`、`/started`、`/stopped`、`/keepalive`）。

## 2. 目录与核心文件
- `ai-analysis/main.py`：服务入口、gRPC 服务、任务生命周期、回调发送、心跳线程。
- `ai-analysis/http_api.py`：HTTP 接口层（`/api/start_camera`、`/api/analyze_image`、`/api/stop_camera`、`/api/status`、`/api/health`）。
- `ai-analysis/frame_capture.py`：RTSP 拉流与抽帧（`ffprobe` + `ffmpeg`）。
- `ai-analysis/detect.py`：模型加载与推理（ONNX/TFLite）、运动检测、后处理。
- `ai-analysis/llm_client.py`：OpenAI 兼容协议的大模型调用封装。
- `ai-analysis/logger.py`：日志初始化与滚动文件配置。
- `ai-analysis/analysis.proto`：gRPC 协议源文件（含 `AnalyzeImage`）。
- `ai-analysis/analysis_pb2.py`、`ai-analysis/analysis_pb2_grpc.py`：gRPC 生成代码。
- `ai-analysis/service_regression_test.py`：回归测试（手动停止回调原因、keepalive 周期）。
- `ai-analysis/webhook_openapi.yaml`：回调接口协议文档。
- `main.go`：本地回调接收示例服务（`/ai/*`）。
- `Dockerfile.ai`、`docker-compose.ai.yml`：容器化部署文件。

## 3. 运行流程
1. 服务启动后加载检测模型并置为就绪状态。
2. 客户端通过 HTTP 或 gRPC 创建摄像头任务（RTSP 场景）或直接提交图片（单图场景）。
3. RTSP 任务中，`CameraTask` 启动抽帧线程与分析线程。
4. `FrameCapture` 先通过 `ffprobe` 获取流信息，再用 `ffmpeg` 输出原始帧并按目标帧率投递队列。
5. 分析线程先做运动检测，再按 `detect_mode` 执行 YOLO / LLM / 联合策略。
6. 任务检测结果通过 `/events` 回调；任务停止时通过 `/stopped` 回调，并带 `reason`。
7. 服务启动成功会回调 `/started`；运行中按配置周期发送 `/keepalive`。

## 4. 检测模式
- `detect_mode=1`：仅 YOLO。
- `detect_mode=2`：仅 LLM（整帧/整图分析）。
- `detect_mode=3`：先 YOLO，再按阈值策略决定是否触发 LLM。

## 5. 接口总览

### HTTP 控制接口
- `POST /api/start_camera`
- `POST /api/analyze_image`
- `POST /api/stop_camera`
- `GET /api/status`
- `GET /api/health`

### gRPC 控制接口
- `AnalysisService.StartCamera`
- `AnalysisService.AnalyzeImage`
- `AnalysisService.StopCamera`
- `AnalysisService.GetStatus`
- `Health.Check`

### AI 回调接口
- `POST {callback_url}/events`
- `POST {callback_url}/started`
- `POST {callback_url}/stopped`
- `POST {callback_url}/keepalive`

## 6. 配置与依赖
- 模型自动发现逻辑位于 `main.py` 的 `MODEL_SEARCH_PATHS`，默认使用 `yolo.*` 命名。
- 运行时依赖系统工具：`ffmpeg`、`ffprobe`。
- Python 关键依赖：`onnxruntime`、`grpcio`、`opencv-python-headless`、`numpy`、`requests`、`openai`。
- 回调鉴权统一使用请求头：`Authorization: <token>`（由 `callback_secret` 提供）。
- keepalive 周期可通过 `--keepalive-interval` 配置（默认 60 秒）。

## 7. 部署方式
- 本地运行：
  - 直接执行 `ai-analysis/main.py`，传入 `--port`、`--http-port`、`--callback-url`、`--model` 等参数。
- 容器运行：
  - `Dockerfile.ai` 构建 AI 镜像；
  - `docker-compose.ai.yml` 暴露 `50051`（gRPC）与 `50052`（HTTP）。

## 8. 当前实现状态
- gRPC 与 HTTP 的核心能力已对齐，包含单图分析接口。
- `Health.Check` 已正确调用 `is_ready()`。
- 手动停止（HTTP/gRPC）均会发送 `/stopped`，并使用 `reason=user_requested`。
- keepalive 已生效，且周期支持参数化配置。
- 模型命名已统一为 `yolo.*`（主流程与文档已同步）。
- 已补充回归测试：
  - 手动停止回调原因；
  - keepalive 周期逻辑。

## 9. 后续建议
1. 增加 `/api/analyze_image` 与 `gRPC AnalyzeImage` 的 mode=1/2/3 行为回归测试。
2. 增加 `image_base64` 的输入校验策略（大小上限、MIME 前缀、异常编码兜底）。
3. 为单图分析链路增加可配置超时与最大图片大小限制。
