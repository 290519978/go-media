import os
import signal

# 解决 macOS 上 OpenMP 库冲突问题，必须在导入 cv2 等库之前设置
os.environ["KMP_DUPLICATE_LIB_OK"] = "TRUE"

import atexit
import argparse
import base64
import json
from concurrent import futures
import logging
import mimetypes
import queue
import re
import sys
import threading
import time
from typing import Any, Callable
import requests
import numpy as np

import grpc

# 添加当前目录到 path 以支持直接运行
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import logger
from detect import MotionDetector, ObjectDetector
from frame_capture import FrameCapture
from http_api import start_http_server
from llm_client import LLMCallResult, call_llm, call_llm_with_video
import cv2

# 模型文件搜索候选路径（按优先级排序）
MODEL_SEARCH_PATHS = [
    ("../configs/yolo.tflite", "tflite"),
    ("../configs/yolo.onnx", "onnx"),
    ("./configs/yolo.tflite", "tflite"),
    ("./configs/yolo.onnx", "onnx"),
    ("./yolo.tflite", "tflite"),
    ("./yolo.onnx", "onnx"),
]

# 导入生成的 proto 代码
# 这些模块必须存在才能启动 gRPC 服务
import analysis_pb2
import analysis_pb2_grpc


slog = logging.getLogger("AI")

# 全局配置
GLOBAL_CONFIG = {
    "callback_url": "",
    "callback_secret": "",
    "algorithm_test_root": "",
}

DEFAULT_STREAM_RETRY_LIMIT = 20
DEFAULT_ANALYSIS_ERROR_RETRY_LIMIT = 10
START_STREAM_INFO_TIMEOUT_SEC = 5.0
LABELS_TRIGGER_MODE_ANY = "any"
LABELS_TRIGGER_MODE_ALL = "all"
DETECT_MODE_SMALL_ONLY = 1
DETECT_MODE_LLM_ONLY = 2
DETECT_MODE_HYBRID = 3
LLM_JSON_FENCE_PATTERN = re.compile(r"^```(?:json)?\s*([\s\S]*?)\s*```$", re.IGNORECASE)


def _handle_thread_exception(args: threading.ExceptHookArgs):
    slog.exception(
        "线程未捕获异常: thread_name=%s err=%s",
        getattr(args.thread, "name", "unknown"),
        args.exc_value,
        exc_info=(args.exc_type, args.exc_value, args.exc_traceback),
    )


def _handle_system_exception(exc_type, exc_value, exc_traceback):
    if issubclass(exc_type, KeyboardInterrupt):
        sys.__excepthook__(exc_type, exc_value, exc_traceback)
        return
    slog.exception(
        "主线程未捕获异常: err=%s",
        exc_value,
        exc_info=(exc_type, exc_value, exc_traceback),
    )


def _log_process_exit():
    slog.info("AI 进程退出")


def _safe_positive_int(value: Any, default: int) -> int:
    try:
        normalized = int(value)
    except (TypeError, ValueError):
        normalized = default
    if normalized <= 0:
        return default
    return normalized


def _default_algorithm_test_root() -> str:
    script_dir = os.path.dirname(os.path.abspath(__file__))
    candidates = [
        os.path.join(script_dir, "configs", "test"),
        os.path.join(script_dir, "..", "configs", "test"),
    ]
    for path in candidates:
        if os.path.exists(path):
            return os.path.normpath(path)
    return os.path.normpath(candidates[-1])


def _get_algorithm_test_root() -> str:
    configured = str(GLOBAL_CONFIG.get("algorithm_test_root", "")).strip()
    if configured:
        return os.path.normpath(configured)
    return _default_algorithm_test_root()


def _resolve_algorithm_test_media_path(rel_path: str) -> tuple[str, str]:
    normalized = str(rel_path or "").strip().replace("\\", "/").strip("/")
    if not normalized:
        return "", "测试媒体路径不能为空"
    if os.path.isabs(normalized):
        return "", "测试媒体路径必须为相对路径"

    base_dir = os.path.abspath(_get_algorithm_test_root())
    full_path = os.path.abspath(os.path.join(base_dir, normalized))
    if full_path != base_dir and not full_path.startswith(base_dir + os.sep):
        return "", "测试媒体路径越界"
    if not os.path.isfile(full_path):
        return "", f"测试媒体文件不存在: {normalized}"
    return full_path, ""


def _normalize_llm_json_text(raw: Any) -> str:
    text = str(raw or "").strip()
    if not text:
        return ""
    matched = LLM_JSON_FENCE_PATTERN.match(text)
    if matched:
        return str(matched.group(1) or "").strip()
    return text


def _parse_llm_json_payload(
    raw: Any,
    *,
    camera_id: str,
    call_id: str,
    scene: str,
) -> dict[str, Any]:
    normalized = _normalize_llm_json_text(raw)
    if not normalized:
        return {}
    try:
        payload = json.loads(normalized)
    except Exception as exc:
        slog.warning(
            "LLM JSON parse failed: scene=%s camera_id=%s call_id=%s err=%s",
            scene,
            camera_id,
            call_id,
            exc,
        )
        return {}
    if not isinstance(payload, dict):
        slog.warning(
            "LLM JSON parse failed: scene=%s camera_id=%s call_id=%s root_type=%s",
            scene,
            camera_id,
            call_id,
            type(payload).__name__,
        )
        return {}
    return payload


def wait_start_stream_info(
    task,
    timeout_sec: float = START_STREAM_INFO_TIMEOUT_SEC,
    poll_interval: float = 0.5,
):
    capture = getattr(task, "capture", None)
    w, h, fps = 0, 0, 0.0
    started_at = time.time()
    while time.time() - started_at < timeout_sec:
        if capture is not None:
            w, h, fps = capture.get_stream_info()
        if w > 0 and h > 0:
            return w, h, fps, True, False
        failed = bool(getattr(capture, "is_failed", False)) or getattr(task, "status", "") == "error"
        if failed:
            return w, h, fps, False, True
        time.sleep(poll_interval)

    if capture is not None:
        w, h, fps = capture.get_stream_info()
    ready = w > 0 and h > 0
    failed = bool(getattr(capture, "is_failed", False)) or getattr(task, "status", "") == "error"
    if ready:
        failed = False
    return w, h, fps, ready, failed

# 保存父进程 PID，用于检测父进程是否退出
_PARENT_PID = os.getppid()


def _watch_parent_process():
    """
    监控父进程是否存活。当 Go 父进程退出后，Python 子进程应该自动退出，
    避免成为孤儿进程持续占用端口和资源。
    """
    while True:
        time.sleep(3)
        # 检查父进程是否还存在
        # 如果父进程退出，当前进程的 ppid 会变成 1 (init/launchd) 或其他进程
        current_ppid = os.getppid()
        if current_ppid != _PARENT_PID:
            slog.warning(
                f"父进程已退出 (原 PID: {_PARENT_PID}, 当前 PPID: {current_ppid})，Python 进程退出"
            )
            os._exit(0)


class CameraTask:
    def __init__(
        self,
        camera_id: str,
        rtsp_url: str,
        config: dict[str, Any],
        detector: ObjectDetector,
        motion_detector: MotionDetector,
        on_detection_callback: Callable[[int], None] | None = None,
    ) -> None:
        self.camera_id = camera_id
        self.rtsp_url = rtsp_url
        self.config = config
        self.detector = detector
        self.motion_detector = motion_detector
        self._on_detection_callback = on_detection_callback

        self.algorithm_configs = self._normalize_algorithm_configs(
            config.get("algorithm_configs", [])
        )
        # 回调兼容字段：算法模式混合时统一回填为 3。
        self.detect_mode = self._derive_legacy_detect_mode()

        # 大模型参数
        self.llm_api_url = config.get("llm_api_url", "")
        self.llm_api_key = config.get("llm_api_key", "")
        self.llm_model = config.get("llm_model", "")
        self.llm_prompt = config.get("llm_prompt", "")

        # IoU 去重：按算法记录上一次触发大模型时的检测框
        self._last_llm_detections_by_algorithm: dict[str, list[dict]] = {}

        self.status = "initializing"
        self.frames_processed = 0
        self.retry_count = 0
        self.total_detections = 0
        self.last_error = ""
        self._stop_event = threading.Event()
        self._thread: threading.Thread | None = None
        self.stream_retry_limit = _safe_positive_int(
            config.get("retry_limit", DEFAULT_STREAM_RETRY_LIMIT),
            DEFAULT_STREAM_RETRY_LIMIT,
        )
        self.analysis_error_retry_limit = DEFAULT_ANALYSIS_ERROR_RETRY_LIMIT

        self.frame_queue = queue.Queue(maxsize=1)
        self.capture = FrameCapture(
            rtsp_url,
            self.frame_queue,
            config.get("detect_rate_mode", "fps"),
            config.get("detect_rate_value", 5),
            self.stream_retry_limit,
        )

    def start(self):
        self.status = "running"
        min_threshold = 0.5
        max_threshold = 0.5
        small_algorithms = [
            cfg
            for cfg in self.algorithm_configs
            if cfg["detect_mode"] in (DETECT_MODE_SMALL_ONLY, DETECT_MODE_HYBRID)
        ]
        if small_algorithms:
            thresholds = [cfg["yolo_threshold"] for cfg in small_algorithms]
            min_threshold = min(thresholds)
            max_threshold = max(thresholds)
        mode1_count = len(
            [cfg for cfg in self.algorithm_configs if cfg["detect_mode"] == DETECT_MODE_SMALL_ONLY]
        )
        mode2_count = len(
            [cfg for cfg in self.algorithm_configs if cfg["detect_mode"] == DETECT_MODE_LLM_ONLY]
        )
        mode3_count = len(
            [cfg for cfg in self.algorithm_configs if cfg["detect_mode"] == DETECT_MODE_HYBRID]
        )
        slog.info(
            f"摄像头 {self.camera_id} 算法配置: "
            f"count={len(self.algorithm_configs)}, "
            f"mode_count=(m1:{mode1_count},m2:{mode2_count},m3:{mode3_count}), "
            f"yolo_threshold_range=[{min_threshold:.2f},{max_threshold:.2f}]"
        )
        # 任务重启时清理该摄像头的运动检测背景，避免复用旧分辨率缓存
        if hasattr(self.motion_detector, "clear_background"):
            self.motion_detector.clear_background(self.camera_id)
        self.capture.start()
        self._stop_event.clear()
        self._thread = threading.Thread(target=self._analysis_loop, daemon=True)
        self._thread.start()
        slog.info(f"CameraTask started for {self.camera_id}")

    def stop(self):
        self.status = "stopping"
        self._stop_event.set()
        self.capture.stop()
        if self._thread:
            self._thread.join(timeout=2)
        if hasattr(self.motion_detector, "clear_background"):
            self.motion_detector.clear_background(self.camera_id)
        slog.info(f"CameraTask stopped for {self.camera_id}")

    def _analysis_loop(self):
        error_streak = 0
        retry_limit = self.analysis_error_retry_limit

        while not self._stop_event.is_set():
            # 检查 FrameCapture 是否已达到重试上限
            if self.capture.is_failed:
                self.status = "error"
                self.retry_count = self.capture.error_count
                self.last_error = self.capture.last_error
                self._send_stopped_callback("capture_failed", self.last_error)
                slog.error(
                    f"CameraTask {self.camera_id} 因帧捕获失败而停止: {self.last_error}"
                )
                break

            try:
                try:
                    frame = self.frame_queue.get(timeout=2.0)
                except queue.Empty:
                    slog.debug("CameraTask frame queue empty, skipping")
                    continue

                error_streak = 0
                self.frames_processed += 1

                roi_points = self.config.get("roi_points")
                motion_boxes, has_motion = self.motion_detector.detect(
                    frame, self.camera_id, roi_points
                )

                if not has_motion:
                    continue

                self._process_per_algorithm_modes(frame)

            except Exception as e:
                slog.error(f"CameraTask analysis loop error: {e}")
                error_streak += 1
                self.retry_count = error_streak
                self.last_error = str(e)
                if error_streak >= retry_limit:
                    self.status = "error"
                    self._send_stopped_callback("error", self.last_error)
                    self.capture.stop()
                    break
                # 防止 cpu 在异常里空转
                time.sleep(1)

    @staticmethod
    def _normalize_algorithm_configs(raw_configs: Any) -> list[dict[str, Any]]:
        if not isinstance(raw_configs, list):
            return []
        normalized: list[dict[str, Any]] = []
        for item in raw_configs:
            if not isinstance(item, dict):
                continue
            algorithm_id = str(item.get("algorithm_id", "")).strip()
            task_code = str(item.get("task_code", "")).strip()
            if not algorithm_id:
                continue
            labels = CameraTask._normalize_labels(item.get("labels"))
            labels_lower = {label.lower() for label in labels}
            normalized.append(
                {
                    "algorithm_id": algorithm_id,
                    "task_code": task_code,
                    "detect_mode": CameraTask._normalize_detect_mode(item.get("detect_mode")),
                    "labels": labels,
                    "labels_lower": labels_lower,
                    "yolo_threshold": CameraTask._safe_conf(
                        item.get("yolo_threshold"), 0.5
                    ),
                    "iou_threshold": CameraTask._safe_iou(
                        item.get("iou_threshold"), 0.8
                    ),
                    "labels_trigger_mode": CameraTask._normalize_labels_trigger_mode(
                        item.get("labels_trigger_mode")
                    ),
                }
            )
        return normalized

    def _derive_legacy_detect_mode(self) -> int:
        configs = getattr(self, "algorithm_configs", [])
        if not isinstance(configs, list):
            configs = []
        modes = {
            int(cfg.get("detect_mode", DETECT_MODE_HYBRID))
            for cfg in configs
            if isinstance(cfg, dict)
        }
        modes.discard(0)
        if not modes:
            return self._normalize_detect_mode(getattr(self, "detect_mode", DETECT_MODE_HYBRID))
        if len(modes) == 1:
            return list(modes)[0]
        return DETECT_MODE_HYBRID

    @staticmethod
    def _normalize_detect_mode(raw: Any) -> int:
        try:
            value = int(raw)
        except (TypeError, ValueError):
            return DETECT_MODE_HYBRID
        if value in (
            DETECT_MODE_SMALL_ONLY,
            DETECT_MODE_LLM_ONLY,
            DETECT_MODE_HYBRID,
        ):
            return value
        return DETECT_MODE_HYBRID

    @staticmethod
    def _normalize_labels(raw: Any) -> list[str]:
        if not isinstance(raw, list):
            return []
        out: list[str] = []
        seen: set[str] = set()
        for item in raw:
            label = str(item).strip()
            if not label:
                continue
            key = label.lower()
            if key in seen:
                continue
            seen.add(key)
            out.append(label)
        return out

    @staticmethod
    def _normalize_labels_trigger_mode(raw: Any) -> str:
        value = str(raw or "").strip().lower()
        if value == LABELS_TRIGGER_MODE_ALL:
            return LABELS_TRIGGER_MODE_ALL
        return LABELS_TRIGGER_MODE_ANY

    @staticmethod
    def _safe_conf(value: Any, default: float) -> float:
        try:
            parsed = float(value)
        except (TypeError, ValueError):
            return default
        if parsed <= 0:
            return default
        if parsed < 0.01:
            return 0.01
        if parsed > 0.99:
            return 0.99
        return parsed

    @staticmethod
    def _safe_iou(value: Any, default: float) -> float:
        try:
            parsed = float(value)
        except (TypeError, ValueError):
            return default
        if parsed <= 0:
            return default
        if parsed < 0.1:
            return 0.1
        if parsed > 0.99:
            return 0.99
        return parsed

    @staticmethod
    def _normalize_alarm_value(value: Any) -> str:
        if isinstance(value, bool):
            return "1" if value else "0"
        if isinstance(value, (int, float)):
            return "1" if int(value) != 0 else "0"
        text = str(value or "").strip().lower()
        if text in {"1", "true", "yes", "on"}:
            return "1"
        return "0"

    def _run_yolo_candidates(self, frame, target_configs: list[dict[str, Any]] | None = None):
        configs = target_configs if target_configs is not None else self.algorithm_configs
        if not configs:
            return []
        min_threshold = min(
            cfg["yolo_threshold"] for cfg in configs
        )
        label_set: set[str] = set()
        for cfg in configs:
            for label in cfg["labels"]:
                label_set.add(label)
        safe_labels = list(label_set) if label_set else None
        try:
            detections, _ = self.detector.detect(
                frame,
                threshold=min_threshold,
                label_filter=safe_labels,
                regions=None,
            )
            return detections or []
        except Exception as e:
            slog.error(f"YOLO detect error: {e}")
            return []

    def _evaluate_algorithm_hits(
        self,
        detections: list[dict[str, Any]],
        target_configs: list[dict[str, Any]] | None = None,
    ) -> list[dict[str, Any]]:
        configs = target_configs if target_configs is not None else self.algorithm_configs
        evaluations: list[dict[str, Any]] = []
        slog.info(
            "camera_id=%s 小模型候选结果: total=%d, detections=%s",
            self.camera_id,
            len(detections),
            self._format_detections_for_log(detections),
        )
        for cfg in configs:
            threshold = cfg["yolo_threshold"]
            labels_lower: set[str] = cfg["labels_lower"]
            matched: list[dict[str, Any]] = []
            matched_labels: set[str] = set()
            threshold_passed: list[dict[str, Any]] = []
            for det in detections:
                confidence = float(det.get("confidence", 0.0))
                if confidence < threshold:
                    continue
                threshold_passed.append(det)
                label = str(det.get("label", "")).strip()
                if not label:
                    continue
                label_lower = label.lower()
                if labels_lower and label_lower not in labels_lower:
                    continue
                matched.append(det)
                matched_labels.add(label_lower)
            missing_labels: list[str] = []
            if cfg["labels_trigger_mode"] == LABELS_TRIGGER_MODE_ALL:
                missing_labels = sorted(
                    [label for label in labels_lower if label not in matched_labels]
                )
                small_alarm = (
                    len(labels_lower) > 0
                    and len(missing_labels) == 0
                )
                compare_basis = (
                    f"mode=all required={sorted(labels_lower)} "
                    f"matched={sorted(matched_labels)} missing={missing_labels}"
                )
            else:
                small_alarm = len(matched) > 0
                compare_basis = (
                    f"mode=any matched_count={len(matched)} "
                    f"matched_labels={sorted(matched_labels)}"
                )
            slog.info(
                "camera_id=%s 算法判定: algorithm_id=%s task_code=%s "
                "labels=%s yolo_threshold=%.2f threshold_passed=%s matched=%s result=%s basis=%s",
                self.camera_id,
                cfg["algorithm_id"],
                cfg["task_code"],
                cfg["labels"],
                threshold,
                self._format_detections_for_log(threshold_passed),
                self._format_detections_for_log(matched),
                "trigger" if small_alarm else "no_trigger",
                compare_basis,
            )
            evaluations.append(
                {
                    "config": cfg,
                    "small_alarm": small_alarm,
                    "matched": matched,
                }
            )
        return evaluations

    @staticmethod
    def _format_detection_for_log(det: dict[str, Any]) -> str:
        label = str(det.get("label", "")).strip() or "unknown"
        confidence = float(det.get("confidence", 0.0))
        box = det.get("box", {}) if isinstance(det.get("box"), dict) else {}
        x_min = int(box.get("x_min", 0))
        y_min = int(box.get("y_min", 0))
        x_max = int(box.get("x_max", 0))
        y_max = int(box.get("y_max", 0))
        return (
            f"{label}@{confidence:.2f}"
            f"[{x_min},{y_min},{x_max},{y_max}]"
        )

    @classmethod
    def _format_detections_for_log(
        cls,
        detections: list[dict[str, Any]],
        limit: int = 8,
    ) -> str:
        if not detections:
            return "[]"
        chunks = [cls._format_detection_for_log(det) for det in detections[:limit]]
        remain = len(detections) - len(chunks)
        if remain > 0:
            chunks.append(f"...+{remain}")
        return "[" + ", ".join(chunks) + "]"

    @staticmethod
    def _clone_detection(det: dict[str, Any]) -> dict[str, Any]:
        box = det.get("box", {}) if isinstance(det.get("box"), dict) else {}
        x_min = int(box.get("x_min", 0))
        y_min = int(box.get("y_min", 0))
        x_max = int(box.get("x_max", 0))
        y_max = int(box.get("y_max", 0))
        area = int(det.get("area") or max(0, x_max - x_min) * max(0, y_max - y_min))
        return {
            "label": str(det.get("label", "")),
            "confidence": float(det.get("confidence", 0.0)),
            "box": {
                "x_min": x_min,
                "y_min": y_min,
                "x_max": x_max,
                "y_max": y_max,
            },
            "area": area,
        }

    def _build_small_alarm_result(
        self,
        cfg: dict[str, Any],
        matched: list[dict[str, Any]],
    ) -> dict[str, Any]:
        return {
            "algorithm_id": cfg["algorithm_id"],
            "task_code": cfg["task_code"],
            "alarm": 1,
            "source": "small",
            "reason": "small model trigger",
            "boxes": [self._clone_detection(det) for det in matched],
        }

    @staticmethod
    def _llm_object_to_detection(obj: dict[str, Any], width: int, height: int) -> dict[str, Any] | None:
        bbox = obj.get("bbox2d")
        if not isinstance(bbox, list) or len(bbox) != 4:
            return None
        try:
            x0 = float(bbox[0])
            y0 = float(bbox[1])
            x1 = float(bbox[2])
            y1 = float(bbox[3])
        except (TypeError, ValueError):
            return None
        if x1 <= x0 or y1 <= y0:
            return None
        w = max(1, int(width))
        h = max(1, int(height))
        x_min = int(round(x0 / 1000.0 * w))
        y_min = int(round(y0 / 1000.0 * h))
        x_max = int(round(x1 / 1000.0 * w))
        y_max = int(round(y1 / 1000.0 * h))
        if x_max <= x_min or y_max <= y_min:
            return None
        area = max(0, x_max - x_min) * max(0, y_max - y_min)
        return {
            "label": str(obj.get("label", "")),
            "confidence": float(obj.get("confidence", 0.0)),
            "box": {
                "x_min": x_min,
                "y_min": y_min,
                "x_max": x_max,
                "y_max": y_max,
            },
            "area": area,
        }

    def _build_llm_algorithm_results(
        self,
        llm_result: str,
        only_algorithm_ids: set[str] | None,
        width: int,
        height: int,
        llm_call_id: str = "",
    ) -> list[dict[str, Any]]:
        payload = _parse_llm_json_payload(
            llm_result,
            camera_id=self.camera_id,
            call_id=str(llm_call_id or ""),
            scene="stream_build_llm_algorithm_results",
        )
        if not payload:
            return []
        task_results = payload.get("task_results")
        if not isinstance(task_results, list):
            task_results = []
        objects = payload.get("objects")
        if not isinstance(objects, list):
            objects = []

        config_by_task_code: dict[str, dict[str, Any]] = {}
        config_by_algorithm_id: dict[str, dict[str, Any]] = {}
        for cfg in self.algorithm_configs:
            task_code = str(cfg.get("task_code", "")).strip().upper()
            if task_code:
                config_by_task_code[task_code] = cfg
            config_by_algorithm_id[cfg["algorithm_id"]] = cfg

        objects_by_task_code: dict[str, list[dict[str, Any]]] = {}
        object_by_id: dict[str, dict[str, Any]] = {}
        for obj in objects:
            if not isinstance(obj, dict):
                continue
            task_code = str(obj.get("task_code", "")).strip().upper()
            if task_code:
                objects_by_task_code.setdefault(task_code, []).append(obj)
            object_id = str(obj.get("object_id", "")).strip()
            if object_id:
                object_by_id[object_id] = obj

        results: list[dict[str, Any]] = []
        for item in task_results:
            if not isinstance(item, dict):
                continue
            task_code = str(item.get("task_code", "")).strip().upper()
            cfg = config_by_task_code.get(task_code)
            if cfg is None:
                slog.info(
                    "camera_id=%s LLM判定跳过: task_code=%s 未匹配到算法配置",
                    self.camera_id,
                    task_code,
                )
                continue
            algorithm_id = cfg["algorithm_id"]
            if only_algorithm_ids is not None and algorithm_id not in only_algorithm_ids:
                slog.info(
                    "camera_id=%s LLM判定跳过: algorithm_id=%s 不在门控集合内",
                    self.camera_id,
                    algorithm_id,
                )
                continue
            alarm = self._normalize_alarm_value(item.get("alarm")) == "1"
            if not alarm:
                slog.info(
                    "camera_id=%s LLM判定结果: algorithm_id=%s task_code=%s alarm=0 reason=%s",
                    self.camera_id,
                    algorithm_id,
                    cfg["task_code"],
                    str(item.get("reason", "")),
                )
                continue
            selected_objects: list[dict[str, Any]] = []
            object_ids = item.get("object_ids")
            if isinstance(object_ids, list) and len(object_ids) > 0:
                for object_id in object_ids:
                    obj = object_by_id.get(str(object_id).strip())
                    if obj is None:
                        continue
                    if str(obj.get("task_code", "")).strip().upper() != task_code:
                        continue
                    selected_objects.append(obj)
            else:
                selected_objects = list(objects_by_task_code.get(task_code, []))

            boxes: list[dict[str, Any]] = []
            for obj in selected_objects:
                det = self._llm_object_to_detection(obj, width, height)
                if det is not None:
                    boxes.append(det)
            results.append(
                {
                    "algorithm_id": algorithm_id,
                    "task_code": cfg["task_code"],
                    "alarm": 1,
                    "source": "llm",
                    "reason": str(item.get("reason", "")),
                    "boxes": boxes,
                }
            )
            slog.info(
                "camera_id=%s LLM判定结果: algorithm_id=%s task_code=%s alarm=1 boxes=%s reason=%s",
                self.camera_id,
                algorithm_id,
                cfg["task_code"],
                self._format_detections_for_log(boxes),
                str(item.get("reason", "")),
            )
        return results

    def _encode_frame_b64(self, frame) -> str:
        """将帧编码为 base64 JPEG"""
        success, buffer = cv2.imencode(".jpg", frame)
        if not success:
            return ""
        return base64.b64encode(buffer).decode("utf-8")

    def _call_llm_with_frame(self, frame) -> LLMCallResult | None:
        """调用大模型分析整帧画面。"""
        image_b64 = self._encode_frame_b64(frame)
        if not image_b64:
            slog.error("Failed to encode frame for LLM")
            return None
        return call_llm(
            api_url=self.llm_api_url,
            api_key=self.llm_api_key,
            model=self.llm_model,
            prompt=self.llm_prompt,
            image_b64=image_b64,
            log_context=f"camera_id={self.camera_id}, detect_mode={self.detect_mode}",
        )

    def _process_per_algorithm_modes(self, frame):
        mode1_configs = [
            cfg for cfg in self.algorithm_configs if cfg["detect_mode"] == DETECT_MODE_SMALL_ONLY
        ]
        mode2_configs = [
            cfg for cfg in self.algorithm_configs if cfg["detect_mode"] == DETECT_MODE_LLM_ONLY
        ]
        mode3_configs = [
            cfg for cfg in self.algorithm_configs if cfg["detect_mode"] == DETECT_MODE_HYBRID
        ]
        has_mode2 = len(mode2_configs) > 0
        small_configs = mode1_configs if has_mode2 else [*mode1_configs, *mode3_configs]

        detections: list[dict[str, Any]] = []
        evaluation_index: dict[str, dict[str, Any]] = {}
        if small_configs:
            detections = self._run_yolo_candidates(frame, small_configs)
            evaluations = self._evaluate_algorithm_hits(detections, small_configs)
            for item in evaluations:
                cfg = item.get("config", {})
                algorithm_id = str(cfg.get("algorithm_id", "")).strip()
                if algorithm_id:
                    evaluation_index[algorithm_id] = item
        else:
            slog.info("camera_id=%s 当前帧无小模型算法，跳过YOLO", self.camera_id)

        algorithm_results: list[dict[str, Any]] = []
        for cfg in mode1_configs:
            algorithm_id = cfg["algorithm_id"]
            item = evaluation_index.get(algorithm_id, {"small_alarm": False, "matched": []})
            if not item["small_alarm"]:
                slog.info(
                    "camera_id=%s detect_mode=1 小模型未触发: algorithm_id=%s task_code=%s",
                    self.camera_id,
                    algorithm_id,
                    cfg["task_code"],
                )
                continue
            matched = item["matched"]
            algorithm_results.append(self._build_small_alarm_result(cfg, matched))
            slog.info(
                "camera_id=%s detect_mode=1 小模型触发: algorithm_id=%s task_code=%s matched=%s",
                self.camera_id,
                algorithm_id,
                cfg["task_code"],
                self._format_detections_for_log(matched),
            )

        llm_pending: set[str] = {cfg["algorithm_id"] for cfg in mode2_configs}
        if mode2_configs:
            slog.info(
                "camera_id=%s detect_mode=2 算法待LLM判定: algorithm_ids=%s",
                self.camera_id,
                sorted(llm_pending),
            )
        llm_gate_algorithms: list[tuple[dict[str, Any], list[dict[str, Any]]]] = []
        if has_mode2:
            if mode3_configs:
                mode3_ids = sorted([cfg["algorithm_id"] for cfg in mode3_configs])
                llm_pending.update(mode3_ids)
                slog.info(
                    "camera_id=%s detect_mode=2 存在，detect_mode=3 跳过小模型过滤: algorithm_ids=%s",
                    self.camera_id,
                    mode3_ids,
                )
        else:
            for cfg in mode3_configs:
                algorithm_id = cfg["algorithm_id"]
                item = evaluation_index.get(
                    algorithm_id, {"small_alarm": False, "matched": []}
                )
                if not item["small_alarm"]:
                    slog.info(
                        "camera_id=%s detect_mode=3 小模型未触发: algorithm_id=%s task_code=%s",
                        self.camera_id,
                        algorithm_id,
                        cfg["task_code"],
                    )
                    continue
                matched = item["matched"]
                llm_gate_algorithms.append((cfg, matched))
                slog.info(
                    "camera_id=%s detect_mode=3 小模型触发，进入LLM门控: algorithm_id=%s task_code=%s matched=%s",
                    self.camera_id,
                    algorithm_id,
                    cfg["task_code"],
                    self._format_detections_for_log(matched),
                )
                should_call = self._should_call_llm_for_algorithm(
                    cfg["algorithm_id"],
                    matched,
                    cfg["iou_threshold"],
                )
                if should_call:
                    llm_pending.add(cfg["algorithm_id"])
                slog.info(
                    "camera_id=%s detect_mode=3 LLM门控判定: algorithm_id=%s iou_threshold=%.2f decision=%s",
                    self.camera_id,
                    cfg["algorithm_id"],
                    cfg["iou_threshold"],
                    "call_llm" if should_call else "skip_llm",
                )

        llm_call = None
        if llm_pending:
            llm_call = self._call_llm_with_frame(frame)
            if llm_call is not None and llm_call.content:
                llm_results = self._build_llm_algorithm_results(
                    llm_call.content,
                    llm_pending,
                    frame.shape[1],
                    frame.shape[0],
                    llm_call.call_id,
                )
                algorithm_results.extend(llm_results)
                slog.info(
                    "camera_id=%s 按算法LLM返回结果: pending=%s llm_results=%d result_ids=%s",
                    self.camera_id,
                    sorted(llm_pending),
                    len(llm_results),
                    [item["algorithm_id"] for item in llm_results],
                )
            else:
                slog.warning(
                    "camera_id=%s 需要LLM但未获得有效返回: pending=%s",
                    self.camera_id,
                    sorted(llm_pending),
                )
            if not has_mode2:
                for cfg, matched in llm_gate_algorithms:
                    if cfg["algorithm_id"] in llm_pending:
                        self._last_llm_detections_by_algorithm[cfg["algorithm_id"]] = list(
                            matched
                        )

        callback_detect_mode = self._derive_legacy_detect_mode()
        slog.info(
            "camera_id=%s 按算法判定汇总: mode_count=(m1:%d,m2:%d,m3:%d) detections=%d algorithm_results=%d ids=%s llm_called=%s callback_detect_mode=%d",
            self.camera_id,
            len(mode1_configs),
            len(mode2_configs),
            len(mode3_configs),
            len(detections),
            len(algorithm_results),
            [item["algorithm_id"] for item in algorithm_results],
            llm_call is not None,
            callback_detect_mode,
        )
        if algorithm_results or llm_call is not None:
            self._send_detection_callback(
                detections,
                frame,
                llm_result=llm_call.content if llm_call is not None else "",
                llm_usage=llm_call.to_payload() if llm_call is not None else None,
                algorithm_results=algorithm_results,
                detect_mode=callback_detect_mode,
            )

    def _process_mode1(self, frame):
        """模式1：仅小模型，按算法策略触发。"""
        detections = self._run_yolo_candidates(frame)
        evaluations = self._evaluate_algorithm_hits(detections)
        algorithm_results: list[dict[str, Any]] = []
        for item in evaluations:
            if not item["small_alarm"]:
                continue
            cfg = item["config"]
            algorithm_results.append(
                self._build_small_alarm_result(cfg, item["matched"])
            )
        slog.info(
            "camera_id=%s detect_mode=1 判定汇总: algorithm_results=%d ids=%s",
            self.camera_id,
            len(algorithm_results),
            [item["algorithm_id"] for item in algorithm_results],
        )
        if algorithm_results:
            self._send_detection_callback(
                detections,
                frame,
                algorithm_results=algorithm_results,
            )

    def _process_mode2(self, frame):
        """模式2：仅大模型。"""
        llm_call = self._call_llm_with_frame(frame)
        if llm_call is not None:
            algorithm_results = self._build_llm_algorithm_results(
                llm_call.content or "",
                None,
                frame.shape[1],
                frame.shape[0],
                llm_call.call_id,
            )
            self._send_detection_callback(
                [],
                frame,
                llm_result=llm_call.content,
                llm_usage=llm_call.to_payload(),
                algorithm_results=algorithm_results,
            )

    def _process_mode3(self, frame):
        """模式3：小模型命中后进入LLM门控。"""
        detections = self._run_yolo_candidates(frame)
        evaluations = self._evaluate_algorithm_hits(detections)
        algorithm_results: list[dict[str, Any]] = []

        llm_gate_algorithms: list[tuple[dict[str, Any], list[dict[str, Any]]]] = []
        for item in evaluations:
            if not item["small_alarm"]:
                cfg = item["config"]
                slog.info(
                    "camera_id=%s detect_mode=3 小模型未触发: algorithm_id=%s task_code=%s",
                    self.camera_id,
                    cfg["algorithm_id"],
                    cfg["task_code"],
                )
                continue
            cfg = item["config"]
            matched = item["matched"]
            slog.info(
                "camera_id=%s detect_mode=3 小模型触发，进入LLM门控: algorithm_id=%s task_code=%s matched=%s",
                self.camera_id,
                cfg["algorithm_id"],
                cfg["task_code"],
                self._format_detections_for_log(matched),
            )
            llm_gate_algorithms.append((cfg, matched))

        llm_call = None
        llm_gate_pending: set[str] = set()
        for cfg, matched in llm_gate_algorithms:
            should_call = self._should_call_llm_for_algorithm(
                cfg["algorithm_id"],
                matched,
                cfg["iou_threshold"],
            )
            if should_call:
                llm_gate_pending.add(cfg["algorithm_id"])
            slog.info(
                "camera_id=%s detect_mode=3 LLM门控判定: algorithm_id=%s iou_threshold=%.2f decision=%s",
                self.camera_id,
                cfg["algorithm_id"],
                cfg["iou_threshold"],
                "call_llm" if should_call else "skip_llm",
            )

        if llm_gate_pending:
            llm_call = self._call_llm_with_frame(frame)
            if llm_call is not None and llm_call.content:
                llm_results = self._build_llm_algorithm_results(
                    llm_call.content,
                    llm_gate_pending,
                    frame.shape[1],
                    frame.shape[0],
                    llm_call.call_id,
                )
                algorithm_results.extend(llm_results)
                slog.info(
                    "camera_id=%s detect_mode=3 LLM返回结果: pending=%s llm_results=%d result_ids=%s",
                    self.camera_id,
                    sorted(llm_gate_pending),
                    len(llm_results),
                    [item["algorithm_id"] for item in llm_results],
                )
            else:
                slog.warning(
                    "camera_id=%s detect_mode=3 需要LLM但未获得有效返回: pending=%s",
                    self.camera_id,
                    sorted(llm_gate_pending),
                )
            for cfg, matched in llm_gate_algorithms:
                if cfg["algorithm_id"] in llm_gate_pending:
                    self._last_llm_detections_by_algorithm[cfg["algorithm_id"]] = list(
                        matched
                    )

        slog.info(
            "camera_id=%s detect_mode=3 判定汇总: detections=%d algorithm_results=%d ids=%s llm_called=%s",
            self.camera_id,
            len(detections),
            len(algorithm_results),
            [item["algorithm_id"] for item in algorithm_results],
            llm_call is not None,
        )
        if algorithm_results or llm_call is not None:
            self._send_detection_callback(
                detections,
                frame,
                llm_result=llm_call.content if llm_call is not None else "",
                llm_usage=llm_call.to_payload() if llm_call is not None else None,
                algorithm_results=algorithm_results,
            )

    @staticmethod
    def _compute_iou(box1: dict, box2: dict) -> float:
        """计算两个检测框的 IoU"""
        x1 = max(box1["x_min"], box2["x_min"])
        y1 = max(box1["y_min"], box2["y_min"])
        x2 = min(box1["x_max"], box2["x_max"])
        y2 = min(box1["y_max"], box2["y_max"])

        inter = max(0, x2 - x1) * max(0, y2 - y1)
        area1 = (box1["x_max"] - box1["x_min"]) * (box1["y_max"] - box1["y_min"])
        area2 = (box2["x_max"] - box2["x_min"]) * (box2["y_max"] - box2["y_min"])

        return inter / (area1 + area2 - inter + 1e-6)

    def _should_call_llm_for_algorithm(
        self,
        algorithm_id: str,
        current_dets: list[dict[str, Any]],
        iou_threshold: float,
    ) -> bool:
        """按算法维度判断是否应该调用大模型（IoU 去重）。"""
        previous = self._last_llm_detections_by_algorithm.get(algorithm_id) or []
        current_for_log = self._format_detections_for_log(current_dets)
        previous_for_log = self._format_detections_for_log(previous)
        if not previous:
            slog.info(
                "camera_id=%s 算法IoU门控: algorithm_id=%s decision=call_llm reason=first_hit iou_threshold=%.2f current=%s previous=%s",
                self.camera_id,
                algorithm_id,
                iou_threshold,
                current_for_log,
                previous_for_log,
            )
            return True  # 首次检测，直接调用

        if len(current_dets) != len(previous):
            slog.info(
                "camera_id=%s 算法IoU门控: algorithm_id=%s decision=call_llm reason=count_changed current_count=%d previous_count=%d iou_threshold=%.2f current=%s previous=%s",
                self.camera_id,
                algorithm_id,
                len(current_dets),
                len(previous),
                iou_threshold,
                current_for_log,
                previous_for_log,
            )
            return True  # 目标数量变了

        # 检查每个当前目标是否都与上次有高 IoU 匹配
        compare_notes: list[str] = []
        for curr in current_dets:
            matched = False
            label = str(curr.get("label", ""))
            confidence = float(curr.get("confidence", 0.0))
            best_iou = 0.0
            for prev in previous:
                if prev["label"] == curr["label"]:
                    iou = self._compute_iou(prev["box"], curr["box"])
                    if iou > best_iou:
                        best_iou = iou
                    if iou >= iou_threshold:
                        matched = True
                        break
            compare_notes.append(
                f"{label}@{confidence:.2f}:best_iou={best_iou:.3f},matched={matched}"
            )
            if not matched:
                slog.info(
                    "camera_id=%s 算法IoU门控: algorithm_id=%s decision=call_llm reason=unmatched_target iou_threshold=%.2f details=%s current=%s previous=%s",
                    self.camera_id,
                    algorithm_id,
                    iou_threshold,
                    compare_notes,
                    current_for_log,
                    previous_for_log,
                )
                return True  # 有目标没匹配上，需要调大模型

        slog.info(
            "camera_id=%s 算法IoU门控: algorithm_id=%s decision=skip_llm reason=all_targets_matched iou_threshold=%.2f details=%s current=%s previous=%s",
            self.camera_id,
            algorithm_id,
            iou_threshold,
            compare_notes,
            current_for_log,
            previous_for_log,
        )
        return False  # 所有目标都匹配，跳过

    def _send_detection_callback(
        self,
        detections,
        frame,
        llm_result: str = "",
        llm_usage: dict[str, Any] | None = None,
        algorithm_results: list[dict[str, Any]] | None = None,
        detect_mode: int | None = None,
    ):
        if detect_mode is None:
            detect_mode = self._derive_legacy_detect_mode()
        timestamp = int(time.time() * 1000)
        success, buffer = cv2.imencode(".jpg", frame)
        snapshot_b64 = ""
        if success:
            snapshot_b64 = base64.b64encode(buffer).decode("utf-8")

        payload = {
            "camera_id": self.camera_id,
            "timestamp": timestamp,
            "detect_mode": detect_mode,
            "detections": detections,
            "snapshot": snapshot_b64,
            "snapshot_width": frame.shape[1],
            "snapshot_height": frame.shape[0],
        }

        if algorithm_results is not None:
            payload["algorithm_results"] = algorithm_results
        if llm_result:
            payload["llm_result"] = llm_result
        if llm_usage is not None:
            payload["llm_usage"] = llm_usage

        algorithm_summary: list[str] = []
        for item in algorithm_results or []:
            boxes = item.get("boxes")
            box_count = len(boxes) if isinstance(boxes, list) else 0
            algorithm_summary.append(
                f"{item.get('algorithm_id', '')}:"
                f"{item.get('source', '')}:"
                f"alarm={self._normalize_alarm_value(item.get('alarm'))}:"
                f"boxes={box_count}"
            )
        slog.info(
            "camera_id=%s 回调上报: detect_mode=%d detections=%d detection_labels=%s "
            "algorithm_results=%s llm_result=%s llm_usage=%s",
            self.camera_id,
            detect_mode,
            len(detections),
            self._format_detections_for_log(detections),
            algorithm_summary,
            "yes" if bool(str(llm_result or "").strip()) else "no",
            "yes" if llm_usage is not None else "no",
        )
        send_callback(self.config, "/events", payload)
        det_count = len(detections)
        self.total_detections += det_count
        if self._on_detection_callback and det_count > 0:
            self._on_detection_callback(det_count)

    def _send_stopped_callback(self, reason, message):
        payload = {
            "camera_id": self.camera_id,
            "timestamp": int(time.time() * 1000),
            "reason": reason,
            "message": message,
        }
        send_callback(self.config, "/stopped", payload)

    def send_stopped_callback(self, reason: str, message: str) -> None:
        """对外暴露的 stopped 回调触发接口（用于手动停止场景）。"""
        self._send_stopped_callback(reason, message)


class HealthServicer(analysis_pb2_grpc.HealthServicer):
    def __init__(self, servicer):
        self._servicer = servicer

    def Check(self, request, context):
        if not self._servicer.is_ready():
            return analysis_pb2.HealthCheckResponse(
                status=analysis_pb2.HealthCheckResponse.NOT_SERVING
            )
        return analysis_pb2.HealthCheckResponse(
            status=analysis_pb2.HealthCheckResponse.SERVING
        )


class AnalysisServiceServicer(analysis_pb2_grpc.AnalysisServiceServicer):
    def __init__(self, model_path):
        self._camera_tasks: dict[str, CameraTask] = {}
        self._lock = threading.Lock()
        self._is_ready = False
        self._start_time = time.time()
        self._total_detections = 0

        self.object_detector = ObjectDetector(model_path)
        self.motion_detector = MotionDetector()

    def is_ready(self) -> bool:
        return self._is_ready

    def initialize(self):
        slog.info("AnalysisService initializing...")
        success = self.object_detector.load_model()
        self._is_ready = success

        if not success:
            slog.error("AnalysisService initialization failed")
            return
        slog.info("AnalysisService initialized")
        threading.Thread(target=send_started_callback).start()

    def record_detections(self, count: int) -> None:
        """累计全局检测数（用于 status/keepalive）。"""
        if count <= 0:
            return
        with self._lock:
            self._total_detections += count

    def snapshot_stats(self) -> dict[str, int]:
        with self._lock:
            active_streams = len(self._camera_tasks)
            total_detections = self._total_detections
        return {
            "active_streams": active_streams,
            "total_detections": total_detections,
            "uptime_seconds": int(time.time() - self._start_time),
        }

    @staticmethod
    def _decode_image_base64(image_base64: str):
        if not image_base64:
            return None, "image_base64 is required"
        if "base64," in image_base64:
            image_base64 = image_base64.split("base64,", 1)[1]
        try:
            data = base64.b64decode(image_base64)
        except Exception as e:
            return None, f"invalid base64: {e}"
        arr = np.frombuffer(data, np.uint8)
        frame = cv2.imdecode(arr, cv2.IMREAD_COLOR)
        if frame is None:
            return None, "failed to decode image"
        return frame, ""

    @staticmethod
    def _load_algorithm_test_image(image_rel_path: str):
        full_path, err = _resolve_algorithm_test_media_path(image_rel_path)
        if err:
            return None, err
        try:
            data = np.fromfile(full_path, dtype=np.uint8)
        except Exception as exc:
            return None, f"读取测试图片失败: {exc}"
        frame = cv2.imdecode(data, cv2.IMREAD_COLOR)
        if frame is None:
            return None, "测试图片解码失败"
        return frame, ""

    @staticmethod
    def _encode_frame_b64(frame) -> str:
        success, buffer = cv2.imencode(".jpg", frame)
        if not success:
            return ""
        return base64.b64encode(buffer).decode("utf-8")

    def _run_yolo_once(self, frame, yolo_threshold: float, labels: list[str] | None):
        try:
            safe_labels = [str(l) for l in labels] if labels else None
            detections, _ = self.object_detector.detect(
                frame,
                threshold=yolo_threshold,
                label_filter=safe_labels,
                regions=None,
            )
            return detections or []
        except Exception as e:
            slog.error(f"YOLO detect error: {e}")
            return []

    @staticmethod
    def _build_snapshot(frame, detections: list[dict]) -> tuple[str, int, int]:
        _ = detections
        snapshot_b64 = AnalysisServiceServicer._encode_frame_b64(frame)
        return snapshot_b64, frame.shape[1], frame.shape[0]

    def _analyze_image(
        self,
        camera_id: str = "",
        raw_algorithm_configs: list[dict[str, Any]] | None = None,
        llm_api_url: str = "",
        llm_api_key: str = "",
        llm_model: str = "",
        llm_prompt: str = "",
        image_rel_path: str = "",
        image_base64: str = "",
    ) -> tuple[bool, str, dict]:
        if str(image_rel_path or "").strip():
            frame, err = self._load_algorithm_test_image(image_rel_path)
        else:
            frame, err = self._decode_image_base64(image_base64)
        if frame is None:
            return False, err, {}
        algorithm_configs = CameraTask._normalize_algorithm_configs(raw_algorithm_configs or [])
        if not algorithm_configs:
            return False, "algorithm_configs is required", {}

        detections: list[dict] = []
        llm_result = ""
        llm_usage = None
        algorithm_results: list[dict[str, Any]] = []

        mode1_configs = [
            cfg for cfg in algorithm_configs if cfg["detect_mode"] == DETECT_MODE_SMALL_ONLY
        ]
        mode2_configs = [
            cfg for cfg in algorithm_configs if cfg["detect_mode"] == DETECT_MODE_LLM_ONLY
        ]
        mode3_configs = [
            cfg for cfg in algorithm_configs if cfg["detect_mode"] == DETECT_MODE_HYBRID
        ]
        has_mode2 = len(mode2_configs) > 0
        small_configs = mode1_configs if has_mode2 else [*mode1_configs, *mode3_configs]

        evaluation_index: dict[str, dict[str, Any]] = {}
        if small_configs:
            yolo_threshold = min(cfg["yolo_threshold"] for cfg in small_configs)
            label_set: set[str] = set()
            for cfg in small_configs:
                for label in cfg["labels"]:
                    label_set.add(label)
            detections = self._run_yolo_once(
                frame,
                yolo_threshold,
                list(label_set) if label_set else None,
            )
            for cfg in small_configs:
                threshold = cfg["yolo_threshold"]
                labels_lower: set[str] = cfg["labels_lower"]
                matched: list[dict[str, Any]] = []
                matched_labels: set[str] = set()
                for det in detections:
                    confidence = float(det.get("confidence", 0.0))
                    if confidence < threshold:
                        continue
                    label = str(det.get("label", "")).strip()
                    if not label:
                        continue
                    label_lower = label.lower()
                    if labels_lower and label_lower not in labels_lower:
                        continue
                    matched.append(det)
                    matched_labels.add(label_lower)
                if cfg["labels_trigger_mode"] == LABELS_TRIGGER_MODE_ALL:
                    small_alarm = len(labels_lower) > 0 and all(
                        label in matched_labels for label in labels_lower
                    )
                else:
                    small_alarm = len(matched) > 0
                evaluation_index[cfg["algorithm_id"]] = {
                    "config": cfg,
                    "small_alarm": small_alarm,
                    "matched": matched,
                }

        for cfg in mode1_configs:
            item = evaluation_index.get(cfg["algorithm_id"], {"small_alarm": False, "matched": []})
            if not item["small_alarm"]:
                continue
            algorithm_results.append(
                {
                    "algorithm_id": cfg["algorithm_id"],
                    "task_code": cfg["task_code"],
                    "alarm": 1,
                    "source": "small",
                    "reason": "small model trigger",
                    "boxes": [CameraTask._clone_detection(det) for det in item["matched"]],
                }
            )

        llm_pending: set[str] = {cfg["algorithm_id"] for cfg in mode2_configs}
        if has_mode2:
            if mode3_configs:
                mode3_ids = sorted([cfg["algorithm_id"] for cfg in mode3_configs])
                llm_pending.update(mode3_ids)
                slog.info(
                    "analyze_image camera_id=%s detect_mode=2 存在，detect_mode=3 跳过小模型过滤: algorithm_ids=%s",
                    camera_id,
                    mode3_ids,
                )
        else:
            for cfg in mode3_configs:
                item = evaluation_index.get(
                    cfg["algorithm_id"], {"small_alarm": False, "matched": []}
                )
                if not item["small_alarm"]:
                    continue
                llm_pending.add(cfg["algorithm_id"])

        if llm_pending:
            image_b64 = self._encode_frame_b64(frame)
            if image_b64:
                llm_call = call_llm(
                    api_url=llm_api_url,
                    api_key=llm_api_key,
                    model=llm_model,
                    prompt=llm_prompt,
                    image_b64=image_b64,
                    log_context=f"analyze_image camera_id={camera_id}, detect_mode=per_algorithm",
                )
                llm_result = llm_call.content
                llm_usage = llm_call.to_payload()
                llm_results = []
                if llm_result:
                    llm_payload = _parse_llm_json_payload(
                        llm_result,
                        camera_id=camera_id,
                        call_id=llm_call.call_id,
                        scene="analyze_image",
                    )
                    task_results = llm_payload.get("task_results")
                    if not isinstance(task_results, list):
                        task_results = []
                    objects = llm_payload.get("objects")
                    if not isinstance(objects, list):
                        objects = []
                    config_by_task_code = {
                        str(cfg.get("task_code", "")).strip().upper(): cfg
                        for cfg in algorithm_configs
                        if str(cfg.get("task_code", "")).strip()
                    }
                    objects_by_task_code: dict[str, list[dict[str, Any]]] = {}
                    object_by_id: dict[str, dict[str, Any]] = {}
                    for obj in objects:
                        if not isinstance(obj, dict):
                            continue
                        task_code = str(obj.get("task_code", "")).strip().upper()
                        if task_code:
                            objects_by_task_code.setdefault(task_code, []).append(obj)
                        object_id = str(obj.get("object_id", "")).strip()
                        if object_id:
                            object_by_id[object_id] = obj
                    for item in task_results:
                        if not isinstance(item, dict):
                            continue
                        if CameraTask._normalize_alarm_value(item.get("alarm")) != "1":
                            continue
                        task_code = str(item.get("task_code", "")).strip().upper()
                        cfg = config_by_task_code.get(task_code)
                        if cfg is None:
                            continue
                        if cfg["algorithm_id"] not in llm_pending:
                            continue
                        selected_objects: list[dict[str, Any]] = []
                        object_ids = item.get("object_ids")
                        if isinstance(object_ids, list) and object_ids:
                            for object_id in object_ids:
                                hit = object_by_id.get(str(object_id).strip())
                                if hit is None:
                                    continue
                                if str(hit.get("task_code", "")).strip().upper() != task_code:
                                    continue
                                selected_objects.append(hit)
                        else:
                            selected_objects = list(objects_by_task_code.get(task_code, []))
                        boxes = []
                        for obj in selected_objects:
                            det = CameraTask._llm_object_to_detection(
                                obj, frame.shape[1], frame.shape[0]
                            )
                            if det is not None:
                                boxes.append(det)
                        llm_results.append(
                            {
                                "algorithm_id": cfg["algorithm_id"],
                                "task_code": cfg["task_code"],
                                "alarm": 1,
                                "source": "llm",
                                "reason": str(item.get("reason", "")),
                                "boxes": boxes,
                            }
                        )
                algorithm_results.extend(llm_results)

        snapshot_b64, w, h = self._build_snapshot(frame, detections)
        detect_mode = DETECT_MODE_HYBRID
        mode_set = {cfg["detect_mode"] for cfg in algorithm_configs}
        if len(mode_set) == 1:
            detect_mode = list(mode_set)[0]
        payload = {
            "camera_id": camera_id,
            "detect_mode": detect_mode,
            "detections": detections,
            "algorithm_results": algorithm_results,
            "llm_result": llm_result,
            "snapshot": snapshot_b64,
            "snapshot_width": w,
            "snapshot_height": h,
        }
        if llm_usage is not None:
            payload["llm_usage"] = llm_usage
        return True, "ok", payload

    def _analyze_video_test(
        self,
        video_rel_path: str,
        fps: Any,
        raw_algorithm_configs: list[dict[str, Any]],
        llm_api_url: str,
        llm_api_key: str,
        llm_model: str,
        llm_prompt: str,
    ) -> tuple[bool, str, dict]:
        algorithm_configs = CameraTask._normalize_algorithm_configs(raw_algorithm_configs)
        if not algorithm_configs:
            return False, "algorithm_configs is required", {}

        primary_cfg = algorithm_configs[0]
        mode = int(primary_cfg.get("detect_mode", DETECT_MODE_HYBRID))
        if mode == DETECT_MODE_SMALL_ONLY:
            return False, "video test only supports llm or hybrid algorithms", {}

        normalized_video_path, path_err = _resolve_algorithm_test_media_path(video_rel_path)
        if path_err:
            return False, path_err, {}

        guessed_mime_type, _ = mimetypes.guess_type(normalized_video_path)
        normalized_mime_type = str(guessed_mime_type or "").strip() or "video/mp4"
        normalized_fps = _safe_positive_int(fps, 1)
        video_size = 0
        try:
            video_size = os.path.getsize(normalized_video_path)
        except OSError:
            video_size = 0
        slog.info(
            "video test shared file ready: bytes=%d mime=%s fps=%d path=%s mode=%s",
            video_size,
            normalized_mime_type,
            normalized_fps,
            normalized_video_path,
            "dashscope_sdk",
        )
        llm_call = call_llm_with_video(
            api_url=llm_api_url,
            api_key=llm_api_key,
            model=llm_model,
            prompt=(llm_prompt or "").strip(),
            video_path=normalized_video_path,
            mime_type=normalized_mime_type,
            fps=normalized_fps,
            timeout=120.0,
            log_context="analyze_video_test",
        )
        if not llm_call.success:
            return False, llm_call.error_message or "视频分析失败", {}

        llm_result = llm_call.content
        llm_usage = llm_call.to_payload()
        payload = {
            "llm_result": llm_result,
        }
        if llm_usage is not None:
            payload["llm_usage"] = llm_usage
        return True, "ok", payload

    def StartCamera(self, request, context):
        if not self._is_ready:
            context.set_details("model loadding")
            context.set_code(grpc.StatusCode.UNAVAILABLE)
            return analysis_pb2.StartCameraResponse(
                success=False, message="model loadding"
            )
        camera_id = request.camera_id
        with self._lock:
            if camera_id in self._camera_tasks:
                slog.info(
                    f"Camera {camera_id} already exists, status: {self._camera_tasks[camera_id].status}"
                )
                return analysis_pb2.StartCameraResponse(
                    success=True, message="任务已运行"
                )
            cb_url = request.callback_url or GLOBAL_CONFIG["callback_url"]
            cb_secret = request.callback_secret or GLOBAL_CONFIG["callback_secret"]
            if not cb_url:
                context.set_details("callback url is required")
                context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
                return analysis_pb2.StartCameraResponse(
                    success=False, message="callback url is required"
                )
            if len(request.algorithm_configs) == 0:
                context.set_details("algorithm_configs is required")
                context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
                return analysis_pb2.StartCameraResponse(
                    success=False, message="algorithm_configs is required"
                )
            algorithm_configs = []
            for item in request.algorithm_configs:
                algorithm_configs.append(
                    {
                        "algorithm_id": item.algorithm_id,
                        "task_code": item.task_code,
                        "detect_mode": item.detect_mode,
                        "labels": list(item.labels),
                        "yolo_threshold": item.yolo_threshold,
                        "iou_threshold": item.iou_threshold,
                        "labels_trigger_mode": item.labels_trigger_mode,
                    }
                )
            config = {
                "detect_rate_mode": request.detect_rate_mode
                if str(request.detect_rate_mode).strip()
                else "fps",
                "detect_rate_value": request.detect_rate_value
                if request.detect_rate_value > 0
                else 5,
                "algorithm_configs": algorithm_configs,
                "retry_limit": request.retry_limit if request.retry_limit > 0 else DEFAULT_STREAM_RETRY_LIMIT,
                "callback_url": cb_url,
                "callback_secret": cb_secret,
                "llm_api_url": request.llm_api_url,
                "llm_api_key": request.llm_api_key,
                "llm_model": request.llm_model,
                "llm_prompt": request.llm_prompt,
            }

            task = CameraTask(
                camera_id,
                rtsp_url=request.rtsp_url,
                config=config,
                detector=self.object_detector,
                motion_detector=self.motion_detector,
                on_detection_callback=self.record_detections,
            )
            task.start()
            self._camera_tasks[camera_id] = task

        w, h, fps, ready, failed = wait_start_stream_info(task)
        if failed:
            fail_message = (
                str(getattr(task.capture, "last_error", "")).strip()
                or str(getattr(task, "last_error", "")).strip()
                or f"start camera failed for {request.rtsp_url}"
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
            with self._lock:
                current = self._camera_tasks.get(camera_id)
                if current is task:
                    self._camera_tasks.pop(camera_id, None)
            return analysis_pb2.StartCameraResponse(
                success=False,
                message=fail_message,
                source_width=w,
                source_height=h,
                source_fps=fps,
            )

        success_message = "任务已启动" if ready else "任务已启动，流信息探测中"
        if not ready:
            slog.info(
                "camera start accepted but stream info pending: camera_id=%s retry_limit=%s",
                camera_id,
                task.stream_retry_limit,
            )
        return analysis_pb2.StartCameraResponse(
            success=True,
            message=success_message,
            source_width=w,
            source_height=h,
            source_fps=fps,
        )

    def StopCamera(self, request, context):
        camera_id = request.camera_id
        with self._lock:
            if camera_id not in self._camera_tasks:
                return analysis_pb2.StopCameraResponse(
                    success=False, message="Camera not found"
                )

            task = self._camera_tasks.pop(camera_id)
        task.stop()
        task.send_stopped_callback("user_requested", "task stopped by user request")
        return analysis_pb2.StopCameraResponse(success=True, message="任务已停止")

    def GetStatus(self, request, context):
        response = analysis_pb2.StatusResponse()
        response.is_ready = self._is_ready

        with self._lock:
            response.stats.active_streams = len(self._camera_tasks)
            response.stats.total_detections = self._total_detections
            response.stats.uptime_seconds = int(time.time() - self._start_time)
            for cid, task in self._camera_tasks.items():
                cam_status = analysis_pb2.CameraStatus(
                    camera_id=cid,
                    status=task.status,
                    frames_processed=task.frames_processed,
                    retry_count=task.retry_count,
                    last_error=task.last_error,
                )
                response.cameras.append(cam_status)
        return response

    def AnalyzeImage(self, request, context):
        if not self._is_ready:
            context.set_details("model loadding")
            context.set_code(grpc.StatusCode.UNAVAILABLE)
            return analysis_pb2.AnalyzeImageResponse(
                success=False,
                message="model loadding",
                camera_id=request.camera_id,
            )

        camera_id = request.camera_id or ""
        if not request.image_base64:
            context.set_details("image_base64 is required")
            context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
            return analysis_pb2.AnalyzeImageResponse(
                success=False,
                message="image_base64 is required",
                camera_id=camera_id,
            )
        if len(request.algorithm_configs) == 0:
            context.set_details("algorithm_configs is required")
            context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
            return analysis_pb2.AnalyzeImageResponse(
                success=False,
                message="algorithm_configs is required",
                camera_id=camera_id,
            )
        algorithm_configs = []
        for item in request.algorithm_configs:
            algorithm_configs.append(
                {
                    "algorithm_id": item.algorithm_id,
                    "task_code": item.task_code,
                    "detect_mode": item.detect_mode,
                    "labels": list(item.labels),
                    "yolo_threshold": item.yolo_threshold,
                    "iou_threshold": item.iou_threshold,
                    "labels_trigger_mode": item.labels_trigger_mode,
                }
            )

        ok, message, payload = self._analyze_image(
            camera_id=camera_id,
            image_base64=request.image_base64,
            raw_algorithm_configs=algorithm_configs,
            llm_api_url=request.llm_api_url,
            llm_api_key=request.llm_api_key,
            llm_model=request.llm_model,
            llm_prompt=request.llm_prompt,
        )
        if not ok:
            context.set_details(message)
            context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
            return analysis_pb2.AnalyzeImageResponse(
                success=False,
                message=message,
                camera_id=camera_id,
            )

        detections = []
        for det in payload.get("detections", []):
            box = det.get("box", {})
            x_min = int(box.get("x_min", 0))
            y_min = int(box.get("y_min", 0))
            x_max = int(box.get("x_max", 0))
            y_max = int(box.get("y_max", 0))
            area = int(det.get("area") or max(0, x_max - x_min) * max(0, y_max - y_min))
            detections.append(
                analysis_pb2.Detection(
                    label=str(det.get("label", "")),
                    confidence=float(det.get("confidence", 0.0)),
                    box=analysis_pb2.BoundingBox(
                        x_min=x_min, y_min=y_min, x_max=x_max, y_max=y_max
                    ),
                    area=area,
                )
            )

        algorithm_results_pb = []
        for item in payload.get("algorithm_results", []):
            boxes_pb = []
            boxes = item.get("boxes")
            if isinstance(boxes, list):
                for det in boxes:
                    box = det.get("box", {})
                    x_min = int(box.get("x_min", 0))
                    y_min = int(box.get("y_min", 0))
                    x_max = int(box.get("x_max", 0))
                    y_max = int(box.get("y_max", 0))
                    area = int(det.get("area") or max(0, x_max - x_min) * max(0, y_max - y_min))
                    boxes_pb.append(
                        analysis_pb2.Detection(
                            label=str(det.get("label", "")),
                            confidence=float(det.get("confidence", 0.0)),
                            box=analysis_pb2.BoundingBox(
                                x_min=x_min, y_min=y_min, x_max=x_max, y_max=y_max
                            ),
                            area=area,
                        )
                    )
            algorithm_results_pb.append(
                analysis_pb2.AlgorithmResult(
                    algorithm_id=str(item.get("algorithm_id", "")),
                    task_code=str(item.get("task_code", "")),
                    alarm=int(item.get("alarm") or 0),
                    source=str(item.get("source", "")),
                    reason=str(item.get("reason", "")),
                    boxes=boxes_pb,
                )
            )

        return analysis_pb2.AnalyzeImageResponse(
            success=True,
            message=message,
            camera_id=camera_id,
            detections=detections,
            llm_result=payload.get("llm_result", ""),
            snapshot=payload.get("snapshot", ""),
            snapshot_width=int(payload.get("snapshot_width", 0)),
            snapshot_height=int(payload.get("snapshot_height", 0)),
            algorithm_results=algorithm_results_pb,
        )


def send_callback(config: dict, path: str, payload: dict):
    """
    发送回调到指定路径，路径会拼接到 callback_url 后面。
    例如: callback_url=http://127.0.0.1:15123, path=/events
    最终请求: POST http://127.0.0.1:15123/events
    """
    url = config.get("callback_url", "")
    secret = config.get("callback_secret", "")
    if not url:
        return

    full_url = url.rstrip("/") + path
    headers = {"Content-Type": "application/json"}
    if secret:
        headers["Authorization"] = secret

    try:
        threading.Thread(
            target=requests.post,
            args=(full_url,),
            kwargs={
                "json": payload,
                "headers": headers,
                "timeout": 5.0,
            },
        ).start()
    except Exception as e:
        slog.error(f"Failed to send callback to {path}: {e}")


def send_started_callback():
    """
    向 Go 服务发送启动通知，用于确认 Python 进程与 Go 服务的连接是否正常。
    如果 Go 服务返回 404，说明回调接口不存在，Python 进程应该退出，避免成为孤儿进程。
    """
    url = GLOBAL_CONFIG["callback_url"]
    secret = GLOBAL_CONFIG["callback_secret"]
    if not url:
        return

    full_url = url.rstrip("/") + "/started"
    headers = {"Content-Type": "application/json"}
    if secret:
        headers["Authorization"] = secret

    payload = {
        "timestamp": int(time.time() * 1000),
        "message": "AI Analysis Service Started",
    }

    max_retries = 3
    retry_interval = 2

    for attempt in range(1, max_retries + 1):
        slog.info(f"Sending started callback (attempt {attempt}/{max_retries})...")
        try:
            resp = requests.post(full_url, json=payload, headers=headers, timeout=5)
            if resp.status_code == 404 and attempt == max_retries - 1:
                slog.error(f"回调接口返回 404，Go 服务可能已停止，退出 Python 进程")
                os._exit(1)
            if resp.ok:
                slog.info("启动通知发送成功")
                return
            slog.warning(f"启动通知返回非成功状态: {resp.status_code} {full_url}")
        except requests.exceptions.ConnectionError as e:
            slog.warning(f"发送启动通知失败 (连接错误): {e}")
        except Exception as e:
            slog.error(f"发送启动通知失败: {e}")

        if attempt < max_retries:
            time.sleep(retry_interval)

    slog.error(f"启动通知发送失败，已重试 {max_retries} 次")


def send_keepalive_callback(stats: dict):
    """
    发送心跳回调，用于定期向 Go 服务报告 AI 服务状态。
    """
    url = GLOBAL_CONFIG["callback_url"]
    secret = GLOBAL_CONFIG["callback_secret"]
    if not url:
        return

    full_url = url.rstrip("/") + "/keepalive"
    headers = {"Content-Type": "application/json"}
    if secret:
        headers["Authorization"] = secret

    payload = {
        "timestamp": int(time.time() * 1000),
        "stats": stats,
        "message": "Service running normally",
    }

    try:
        requests.post(full_url, json=payload, headers=headers, timeout=5)
    except Exception as e:
        slog.debug(f"Failed to send keepalive callback: {e}")


def _keepalive_loop(servicer: AnalysisServiceServicer, interval_sec: int = 60):
    """定期发送 keepalive 回调。"""
    while True:
        time.sleep(interval_sec)
        if not servicer.is_ready():
            continue
        try:
            send_keepalive_callback(servicer.snapshot_stats())
        except Exception as e:
            slog.debug(f"Keepalive loop error: {e}")


def serve(port, model_path, http_port=50052, keepalive_interval=60):
    # 启动父进程监控线程，确保 Go 退出时 Python 也退出
    threading.Thread(target=_watch_parent_process, daemon=True).start()

    server = grpc.server(futures.ThreadPoolExecutor(max_workers=20))
    servicer = AnalysisServiceServicer(model_path)
    analysis_pb2_grpc.add_AnalysisServiceServicer_to_server(servicer, server)

    health_servicer = HealthServicer(servicer)
    analysis_pb2_grpc.add_HealthServicer_to_server(health_servicer, server)

    server.add_insecure_port(f"[::]:{port}")
    server.start()
    slog.info(f"gRPC service started: 0.0.0.0:{port}")

    # 启动 HTTP API 服务（供 Java 等服务调用）
    if http_port > 0:
        start_http_server(servicer, http_port)

    keepalive_interval = max(1, int(keepalive_interval))
    threading.Thread(target=servicer.initialize).start()
    threading.Thread(
        target=_keepalive_loop,
        args=(servicer, keepalive_interval),
        daemon=True,
    ).start()

    try:
        server.wait_for_termination()
    except KeyboardInterrupt:
        server.stop(0)


def discover_model(model_arg: str) -> str:
    """
    自动发现可用模型文件
    优先级：../configs/yolo.tflite > ../configs/yolo.onnx > ./yolo.tflite > ./yolo.onnx > 命令行参数
    """
    script_dir = os.path.dirname(os.path.abspath(__file__))

    for rel_path, _ in MODEL_SEARCH_PATHS:
        full_path = os.path.normpath(os.path.join(script_dir, rel_path))
        if os.path.exists(full_path):
            slog.info(f"发现模型文件: {full_path}")
            return full_path

    # 回退到命令行参数指定的模型
    if os.path.isabs(model_arg):
        return model_arg

    # 相对路径基于脚本目录解析
    return os.path.normpath(os.path.join(script_dir, model_arg))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, default=50051)
    parser.add_argument("--model", type=str, default="yolo.onnx")
    parser.add_argument(
        "--callback-url",
        type=str,
        default="http://127.0.0.1:15123",
        help="回调基础URL，各回调路由会自动拼接",
    )
    parser.add_argument("--callback-secret", type=str, default="", help="回调秘钥")
    parser.add_argument(
        "--http-port",
        type=int,
        default=50052,
        help="HTTP API 端口，设为 0 禁用 (默认 50052)",
    )
    parser.add_argument(
        "--keepalive-interval",
        type=int,
        default=60,
        help="心跳回调周期秒数 (默认 60)",
    )
    parser.add_argument(
        "--log-level",
        type=str,
        default="INFO",
        help="日志级别 (DEBUG/INFO/ERROR)",
    )
    parser.add_argument(
        "--algorithm-test-root",
        type=str,
        default="",
        help="算法测试媒体根目录",
    )
    parser.add_argument(
        "--log-dir",
        type=str,
        default="",
        help="AI 日志目录",
    )
    args = parser.parse_args()
    logger.setup_logging(level_str=args.log_level, log_dir=args.log_dir)
    threading.excepthook = _handle_thread_exception
    sys.excepthook = _handle_system_exception
    atexit.register(_log_process_exit)

    GLOBAL_CONFIG["callback_url"] = args.callback_url
    GLOBAL_CONFIG["callback_secret"] = args.callback_secret
    GLOBAL_CONFIG["algorithm_test_root"] = args.algorithm_test_root

    # 自动发现模型文件
    model_path = discover_model(args.model)

    slog.debug(
        f"log level: {args.log_level}, model: {model_path}, callback url: {args.callback_url}, callback secret: {args.callback_secret}"
    )

    serve(
        args.port,
        model_path,
        http_port=args.http_port,
        keepalive_interval=args.keepalive_interval,
    )


if __name__ == "__main__":
    main()
