# ai-analysis 服务说明

`ai-analysis` 是 `maas-box` 使用的轻量 AI 分析服务，负责视频/图像检测推理。

核心技术：
- Python 3
- ONNX Runtime / TFLite 模型加载
- OpenCV 帧处理
- HTTP + gRPC 接口

## 1. 功能能力
- 支持基于 RTSP 的实时视频分析。
- 提供单图分析接口，用于算法测试。
- 支持三种检测模式：
  - 仅小模型
  - 仅大模型
  - 小模型 + 大模型混合
- 支持回调通知后端：
  - events
  - started
  - stopped
  - keepalive

## 2. 安装
```bash
cd ai-analysis
python -m pip install -r requirements.txt
```

同时请确保宿主机/容器中已安装 `ffmpeg`。

## 3. 启动

本地默认推荐通过 Docker host compose 启动：

```bash
docker compose --env-file deploy/env/local.env -f docker-compose.ai.local.host.yml up -d --build
```

如需对比 `bridge + ports` 方式，可改用：

```bash
docker compose --env-file deploy/env/local.env -f docker-compose.ai.local.yml up -d --build
```

容器内实际执行的仍是 `main.py`，等价启动参数示例：

```bash
python main.py \
  --port 50051 \
  --http-port 50052 \
  --callback-url http://127.0.0.1:15123/ai \
  --callback-secret maas-box-callback-token \
  --algorithm-test-root /app/configs/test \
  --log-dir /app/configs/logs/ai \
  --log-level INFO
```

## 4. 命令行参数
- `--port`：gRPC 端口，默认 `50051`
- `--http-port`：HTTP 端口，默认 `50052`，传 `0` 表示关闭 HTTP
- `--model`：模型文件路径提示，默认 `yolo.onnx`
- `--callback-url`：后端回调基础地址
- `--callback-secret`：回调请求 `Authorization` 头使用的 Token
- `--keepalive-interval`：keepalive 回调间隔（秒），默认 `60`
- `--log-level`：`DEBUG|INFO|ERROR`

本地 Docker compose 默认会额外固定：

- `--algorithm-test-root /app/configs/test`
- `--log-dir /app/configs/logs/ai`

模型自动发现优先级：
1. `../configs/yolo.tflite`
2. `../configs/yolo.onnx`
3. `./configs/yolo.tflite`
4. `./configs/yolo.onnx`
5. `./yolo.tflite`
6. `./yolo.onnx`
7. 最后回退到 `--model`

## 5. HTTP API

## `POST /api/start_camera`
启动单路或批量摄像头分析。

最小请求示例：
```json
{
  "camera_id": "dev_001",
  "rtsp_url": "rtsp://user:pass@192.168.1.10/stream",
  "callback_url": "http://maas-box-backend:15123/ai",
  "callback_secret": "maas-box-callback-token",
  "detect_mode": 3
}
```

## `POST /api/stop_camera`
停止单路或批量摄像头分析。

## `POST /api/analyze_image`
对一张 base64 图片执行分析（算法测试）。

## `GET /api/status`
返回服务就绪状态和运行中摄像头状态。

## `GET /api/health`
健康检查接口。

## 6. 检测模式
- `1`：仅小模型（YOLO）
- `2`：仅大模型（LLM）
- `3`：混合模式（小模型触发 + 大模型判断，含阈值与 IoU 去重）

## 7. 回调契约

服务会向后端以下路由回调：
- `{callback_url}/events`
- `{callback_url}/started`
- `{callback_url}/stopped`
- `{callback_url}/keepalive`

当设置 `callback_secret` 时，所有回调请求都会携带：
- `Authorization: <callback_secret>`

## 8. 边缘设备建议（RK3588 / 4GB）
- 并发摄像头数量保持保守。
- `detect_rate_mode/detect_rate_value` 使用中等值，避免过载。
- 优先采用报警驱动流程，避免连续重负载运行。
