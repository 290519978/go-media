"""
HTTP API 层 - 为 Java 等服务提供 RESTful 接口
将 gRPC 的 AnalysisService 功能通过 HTTP JSON 暴露
"""

import json
import logging
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any

slog = logging.getLogger("HTTP")


class ThreadedAPIHTTPServer(ThreadingHTTPServer):
    daemon_threads = True
    allow_reuse_address = True


class APIHandler(BaseHTTPRequestHandler):
    """HTTP 请求处理器，复用 AnalysisServiceServicer 的业务逻辑"""

    servicer = None  # 由 start_http_server 注入

    def do_POST(self):
        self._dispatch_request("POST")

    def do_GET(self):
        self._dispatch_request("GET")

    def _dispatch_request(self, method: str):
        try:
            if method == "POST":
                if self.path == "/api/start_camera":
                    self._handle_start_camera()
                elif self.path == "/api/stop_camera":
                    self._handle_stop_camera()
                elif self.path == "/api/analyze_image":
                    self._handle_analyze_image()
                elif self.path == "/api/analyze_video_test":
                    self._handle_analyze_video_test()
                else:
                    self._safe_send_json(404, {"error": f"Not Found: {self.path}"})
                return

            if method == "GET":
                if self.path == "/api/status":
                    self._handle_get_status()
                elif self.path == "/api/health":
                    self._handle_health()
                else:
                    self._safe_send_json(404, {"error": f"Not Found: {self.path}"})
                return

            self._safe_send_json(405, {"error": f"Method Not Allowed: {method}"})
        except Exception as exc:
            slog.exception(
                "HTTP API unhandled error: method=%s path=%s err=%s",
                method,
                self.path,
                exc,
            )
            self._safe_send_json(500, {"success": False, "message": "AI 服务内部错误"})

    def _read_json(self) -> Any:
        length = int(self.headers.get("Content-Length", 0))
        if length == 0:
            return {}
        body = self.rfile.read(length)
        return json.loads(body.decode("utf-8"))

    def _send_json(self, status: int, data: dict):
        body = json.dumps(data, ensure_ascii=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _safe_send_json(self, status: int, data: dict):
        try:
            self._send_json(status, data)
        except (BrokenPipeError, ConnectionAbortedError, ConnectionResetError) as exc:
            slog.warning(
                "HTTP API response aborted: method=%s path=%s status=%s err=%s",
                self.command,
                self.path,
                status,
                exc,
            )
        except Exception as exc:
            slog.exception(
                "HTTP API send response failed: method=%s path=%s status=%s err=%s",
                self.command,
                self.path,
                status,
                exc,
            )

    def _current_thread_id(self) -> int:
        return threading.get_ident()

    def _handle_start_camera(self):
        """
        POST /api/start_camera
        {
            "camera_id": "cam_01",
            "rtsp_url": "rtsp://...",
            "callback_url": "http://java-service:8080/ai",
            "callback_secret": "",
            "detect_rate_mode": "fps",
            "detect_rate_value": 5,
            "algorithm_configs": [
                {
                    "algorithm_id": "alg_001",
                    "task_code": "ALG_001",
                    "detect_mode": 3,
                    "labels": ["person", "car"],
                    "yolo_threshold": 0.5,
                    "iou_threshold": 0.8,
                    "labels_trigger_mode": "any"
                }
            ],
            "retry_limit": 10
        }
        """
        try:
            payload = self._read_json()
        except Exception as e:
            self._send_json(400, {"success": False, "message": f"Invalid JSON: {e}"})
            return

        # 支持批量启动：请求体可为数组
        if isinstance(payload, list):
            if not payload:
                self._send_json(400, {"success": False, "message": "request list is empty"})
                return
            results = []
            success_count = 0
            for idx, item in enumerate(payload):
                status, result = self._start_camera_one(item)
                if isinstance(item, dict) and "camera_id" in item and "camera_id" not in result:
                    result["camera_id"] = item.get("camera_id")
                result["index"] = idx
                result["http_status"] = status
                results.append(result)
                if result.get("success"):
                    success_count += 1

            total = len(results)
            failed = total - success_count
            self._send_json(200, {
                "success": failed == 0,
                "message": "批量启动完成",
                "summary": {"total": total, "success": success_count, "failed": failed},
                "results": results,
            })
            return

        status, result = self._start_camera_one(payload)
        self._send_json(status, result)

    def _start_camera_one(self, data: Any) -> tuple[int, dict]:
        if not isinstance(data, dict):
            return 400, {"success": False, "message": "request item must be object"}

        camera_id = str(data.get("camera_id", "")).strip()
        rtsp_url = str(data.get("rtsp_url", "")).strip()
        if not camera_id or not rtsp_url:
            return 400, {"success": False, "message": "camera_id and rtsp_url are required"}

        servicer = self.__class__.servicer
        if not servicer or not servicer.is_ready():
            return 503, {"success": False, "message": "Model not ready"}

        # 构造与 gRPC 相同的 config
        from main import (
            GLOBAL_CONFIG,
            CameraTask,
            DEFAULT_STREAM_RETRY_LIMIT,
            wait_start_stream_info,
        )

        cb_url = data.get("callback_url") or GLOBAL_CONFIG["callback_url"]
        cb_secret = data.get("callback_secret") or GLOBAL_CONFIG["callback_secret"]
        if not cb_url:
            return 400, {"success": False, "message": "callback_url is required"}

        with servicer._lock:
            if camera_id in servicer._camera_tasks:
                task = servicer._camera_tasks[camera_id]
                return 200, {
                    "success": True,
                    "message": "任务已运行",
                    "camera_id": camera_id,
                    "source_width": task.capture.width,
                    "source_height": task.capture.height,
                    "source_fps": task.capture.fps,
                }

            detect_rate_mode = str(data.get("detect_rate_mode", "fps")).strip().lower()
            if detect_rate_mode not in {"fps", "interval"}:
                slog.warning("invalid detect_rate_mode=%s, fallback to fps", detect_rate_mode)
                detect_rate_mode = "fps"
            try:
                detect_rate_value = int(data.get("detect_rate_value", 5))
            except (TypeError, ValueError):
                detect_rate_value = 5
            if detect_rate_value < 1 or detect_rate_value > 60:
                slog.warning(
                    "invalid detect_rate_value=%s, fallback to 5",
                    data.get("detect_rate_value"),
                )
                detect_rate_value = 5
            algorithm_configs = data.get("algorithm_configs", [])
            if not isinstance(algorithm_configs, list) or len(algorithm_configs) == 0:
                return 400, {"success": False, "message": "algorithm_configs is required"}

            config = {
                "detect_rate_mode": detect_rate_mode,
                "detect_rate_value": detect_rate_value,
                "algorithm_configs": algorithm_configs,
                "retry_limit": data.get("retry_limit", DEFAULT_STREAM_RETRY_LIMIT),
                "callback_url": cb_url,
                "callback_secret": cb_secret,
                "llm_api_url": data.get("llm_api_url", ""),
                "llm_api_key": data.get("llm_api_key", ""),
                "llm_model": data.get("llm_model", ""),
                "llm_prompt": data.get("llm_prompt", ""),
            }

            task = CameraTask(
                camera_id,
                rtsp_url=rtsp_url,
                config=config,
                detector=servicer.object_detector,
                motion_detector=servicer.motion_detector,
                on_detection_callback=servicer.record_detections,
            )
            task.start()
            servicer._camera_tasks[camera_id] = task

        w, h, fps, ready, failed = wait_start_stream_info(task)
        if failed:
            fail_message = (
                str(getattr(task.capture, "last_error", "")).strip()
                or str(getattr(task, "last_error", "")).strip()
                or f"start camera failed for {rtsp_url}"
            )
            slog.error(
                "camera start failed during observation: camera_id=%s message=%s",
                camera_id,
                fail_message,
            )
            try:
                task.stop()
            except Exception as stop_err:
                slog.error(
                    "stop camera after start failure failed: camera_id=%s err=%s",
                    camera_id,
                    stop_err,
                )
            with servicer._lock:
                current = servicer._camera_tasks.get(camera_id)
                if current is task:
                    servicer._camera_tasks.pop(camera_id, None)
            return 200, {
                "success": False,
                "message": fail_message,
                "camera_id": camera_id,
                "source_width": w,
                "source_height": h,
                "source_fps": fps,
            }

        success_message = "任务已启动" if ready else "任务已启动，流信息探测中"
        if not ready:
            slog.info(
                "camera start accepted but stream info pending: camera_id=%s retry_limit=%s",
                camera_id,
                task.stream_retry_limit,
            )
        return 200, {
            "success": True,
            "message": success_message,
            "camera_id": camera_id,
            "source_width": w,
            "source_height": h,
            "source_fps": fps,
        }

    def _handle_stop_camera(self):
        """
        POST /api/stop_camera
        { "camera_id": "cam_01" }
        """
        try:
            payload = self._read_json()
        except Exception as e:
            self._send_json(400, {"success": False, "message": f"Invalid JSON: {e}"})
            return

        # 支持批量停止：请求体可为数组（元素可为 {"camera_id":"..."} 或 "camera_id"）
        if isinstance(payload, list):
            if not payload:
                self._send_json(400, {"success": False, "message": "request list is empty"})
                return
            results = []
            success_count = 0
            for idx, item in enumerate(payload):
                status, result = self._stop_camera_one(item)
                if isinstance(item, dict) and "camera_id" in item and "camera_id" not in result:
                    result["camera_id"] = item.get("camera_id")
                result["index"] = idx
                result["http_status"] = status
                results.append(result)
                if result.get("success"):
                    success_count += 1

            total = len(results)
            failed = total - success_count
            self._send_json(200, {
                "success": failed == 0,
                "message": "批量停止完成",
                "summary": {"total": total, "success": success_count, "failed": failed},
                "results": results,
            })
            return

        status, result = self._stop_camera_one(payload)
        self._send_json(status, result)

    def _stop_camera_one(self, data: Any) -> tuple[int, dict]:
        if isinstance(data, str):
            camera_id = data.strip()
        elif isinstance(data, dict):
            camera_id = str(data.get("camera_id", "")).strip()
        else:
            return 400, {"success": False, "message": "request item must be object or string"}

        if not camera_id:
            return 400, {"success": False, "message": "camera_id is required"}

        servicer = self.__class__.servicer
        with servicer._lock:
            if camera_id not in servicer._camera_tasks:
                return 404, {"success": False, "message": "Camera not found", "camera_id": camera_id}

            task = servicer._camera_tasks.pop(camera_id)

        task.stop()
        if hasattr(task, "send_stopped_callback"):
            task.send_stopped_callback("user_requested", "task stopped by user request")
        return 200, {"success": True, "message": "任务已停止", "camera_id": camera_id}

    def _handle_analyze_image(self):
        """
        POST /api/analyze_image
        {
            "image_rel_path": "20260325/<batch>/image.jpg",
            "algorithm_configs": [
                {
                    "algorithm_id": "alg_001",
                    "task_code": "ALG_001",
                    "detect_mode": 3,
                    "labels": ["person", "car"],
                    "yolo_threshold": 0.5,
                    "iou_threshold": 0.8,
                    "labels_trigger_mode": "any"
                }
            ],
            "llm_api_url": "",
            "llm_api_key": "",
            "llm_model": "",
            "llm_prompt": ""
        }
        """
        try:
            data = self._read_json()
        except Exception as e:
            self._send_json(400, {"success": False, "message": f"Invalid JSON: {e}"})
            return

        if not isinstance(data, dict):
            self._send_json(400, {"success": False, "message": "request body must be object"})
            return

        started_at = time.perf_counter()
        servicer = self.__class__.servicer
        if not servicer or not servicer.is_ready():
            slog.warning("HTTP analyze_image rejected: service not ready path=%s", self.path)
            self._send_json(503, {"success": False, "message": "Model not ready"})
            return

        image_rel_path = str(data.get("image_rel_path", "")).strip()
        if not image_rel_path:
            self._send_json(400, {"success": False, "message": "image_rel_path is required"})
            return

        algorithm_configs = data.get("algorithm_configs", [])
        if not isinstance(algorithm_configs, list) or len(algorithm_configs) == 0:
            self._send_json(400, {"success": False, "message": "algorithm_configs is required"})
            return

        slog.info(
            "HTTP analyze_image request accepted: thread_id=%s image_rel_path=%s algorithms=%d",
            self._current_thread_id(),
            image_rel_path,
            len(algorithm_configs),
        )
        slog.info(
            "HTTP analyze_image business start: thread_id=%s image_rel_path=%s algorithms=%d",
            self._current_thread_id(),
            image_rel_path,
            len(algorithm_configs),
        )
        ok, message, payload = servicer._analyze_image(
            image_rel_path=image_rel_path,
            raw_algorithm_configs=algorithm_configs,
            llm_api_url=data.get("llm_api_url", ""),
            llm_api_key=data.get("llm_api_key", ""),
            llm_model=data.get("llm_model", ""),
            llm_prompt=data.get("llm_prompt", ""),
        )
        slog.info(
            "HTTP analyze_image business finished: thread_id=%s image_rel_path=%s success=%s",
            self._current_thread_id(),
            image_rel_path,
            ok,
        )

        if not ok:
            slog.warning(
                "HTTP analyze_image response ready: thread_id=%s image_rel_path=%s success=false latency_ms=%.1f message=%s",
                self._current_thread_id(),
                image_rel_path,
                (time.perf_counter() - started_at) * 1000,
                message,
            )
            self._send_json(400, {"success": False, "message": message})
            slog.info(
                "HTTP analyze_image response sent: thread_id=%s image_rel_path=%s status=400",
                self._current_thread_id(),
                image_rel_path,
            )
            return

        payload.pop("camera_id", None)
        payload.pop("detect_mode", None)
        payload.pop("snapshot", None)
        payload.pop("snapshot_width", None)
        payload.pop("snapshot_height", None)
        payload["success"] = True
        payload["message"] = message
        slog.info(
            "HTTP analyze_image response ready: thread_id=%s image_rel_path=%s success=true latency_ms=%.1f",
            self._current_thread_id(),
            image_rel_path,
            (time.perf_counter() - started_at) * 1000,
        )
        self._send_json(200, payload)
        slog.info(
            "HTTP analyze_image response sent: thread_id=%s image_rel_path=%s status=200",
            self._current_thread_id(),
            image_rel_path,
        )

    def _handle_analyze_video_test(self):
        """
        POST /api/analyze_video_test
        {
            "video_rel_path": "20260325/<batch>/video.mp4",
            "fps": 1,
            "algorithm_configs": [...],
            "llm_api_url": "",
            "llm_api_key": "",
            "llm_model": "",
            "llm_prompt": ""
        }
        """
        try:
            data = self._read_json()
        except Exception as e:
            self._send_json(400, {"success": False, "message": f"Invalid JSON: {e}"})
            return

        if not isinstance(data, dict):
            self._send_json(400, {"success": False, "message": "request body must be object"})
            return

        started_at = time.perf_counter()
        servicer = self.__class__.servicer
        if not servicer or not servicer.is_ready():
            slog.warning("HTTP analyze_video_test rejected: service not ready path=%s", self.path)
            self._send_json(503, {"success": False, "message": "Model not ready"})
            return

        video_rel_path = str(data.get("video_rel_path", "")).strip()
        if not video_rel_path:
            self._send_json(400, {"success": False, "message": "video_rel_path is required"})
            return

        algorithm_configs = data.get("algorithm_configs", [])
        if not isinstance(algorithm_configs, list) or len(algorithm_configs) == 0:
            self._send_json(400, {"success": False, "message": "algorithm_configs is required"})
            return

        fps = data.get("fps", 1)
        slog.info(
            "HTTP analyze_video_test request accepted: thread_id=%s video_rel_path=%s algorithms=%d fps=%s",
            self._current_thread_id(),
            video_rel_path,
            len(algorithm_configs),
            fps,
        )
        slog.info(
            "HTTP analyze_video_test business start: thread_id=%s video_rel_path=%s algorithms=%d fps=%s",
            self._current_thread_id(),
            video_rel_path,
            len(algorithm_configs),
            fps,
        )
        ok, message, payload = servicer._analyze_video_test(
            video_rel_path=video_rel_path,
            fps=fps,
            raw_algorithm_configs=algorithm_configs,
            llm_api_url=data.get("llm_api_url", ""),
            llm_api_key=data.get("llm_api_key", ""),
            llm_model=data.get("llm_model", ""),
            llm_prompt=data.get("llm_prompt", ""),
        )
        slog.info(
            "HTTP analyze_video_test business finished: thread_id=%s video_rel_path=%s success=%s",
            self._current_thread_id(),
            video_rel_path,
            ok,
        )
        if not ok:
            slog.warning(
                "HTTP analyze_video_test response ready: thread_id=%s video_rel_path=%s success=false latency_ms=%.1f message=%s",
                self._current_thread_id(),
                video_rel_path,
                (time.perf_counter() - started_at) * 1000,
                message,
            )
            self._send_json(400, {"success": False, "message": message})
            slog.info(
                "HTTP analyze_video_test response sent: thread_id=%s video_rel_path=%s status=400",
                self._current_thread_id(),
                video_rel_path,
            )
            return

        payload.pop("camera_id", None)
        payload.pop("duration_seconds", None)
        payload.pop("conclusion", None)
        payload.pop("basis", None)
        payload.pop("anomaly_times", None)
        payload["success"] = True
        payload["message"] = message
        slog.info(
            "HTTP analyze_video_test response ready: thread_id=%s video_rel_path=%s success=true latency_ms=%.1f",
            self._current_thread_id(),
            video_rel_path,
            (time.perf_counter() - started_at) * 1000,
        )
        self._send_json(200, payload)
        slog.info(
            "HTTP analyze_video_test response sent: thread_id=%s video_rel_path=%s status=200",
            self._current_thread_id(),
            video_rel_path,
        )

    def _handle_get_status(self):
        """GET /api/status"""
        servicer = self.__class__.servicer
        import time

        cameras = []
        with servicer._lock:
            for cid, task in servicer._camera_tasks.items():
                cameras.append({
                    "camera_id": cid,
                    "status": task.status,
                    "frames_processed": task.frames_processed,
                    "retry_count": task.retry_count,
                    "last_error": task.last_error,
                })

        self._send_json(200, {
            "is_ready": servicer.is_ready(),
            "cameras": cameras,
            "stats": {
                "active_streams": len(cameras),
                "uptime_seconds": int(time.time() - servicer._start_time),
            },
        })

    def _handle_health(self):
        """GET /api/health"""
        servicer = self.__class__.servicer
        status = "SERVING" if servicer and servicer.is_ready() else "NOT_SERVING"
        self._send_json(200, {"status": status})

    def log_message(self, format, *args):
        """覆盖默认日志，使用 slog"""
        slog.debug(f"{self.client_address[0]} - {format % args}")


def start_http_server(servicer, port: int = 50052):
    """启动 HTTP API 服务（在独立线程中运行）"""
    APIHandler.servicer = servicer
    server = ThreadedAPIHTTPServer(("0.0.0.0", port), APIHandler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    slog.info(f"HTTP API started: 0.0.0.0:{port}")
    return server
