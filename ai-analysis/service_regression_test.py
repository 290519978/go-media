#!/usr/bin/env python3
import base64
import json
import os
import queue
import ssl
import sys
import tempfile
import threading
import unittest
from types import SimpleNamespace
from unittest.mock import patch

import cv2
import numpy as np

# add current directory to sys.path
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import analysis_pb2
import frame_capture
import http_api
import llm_client
import main


class _FakeTask:
    def __init__(self):
        self.stop_called = False
        self.stopped_reason = None
        self.stopped_message = None

    def stop(self):
        self.stop_called = True

    def send_stopped_callback(self, reason: str, message: str):
        self.stopped_reason = reason
        self.stopped_message = message


class _DummyServicer:
    def __init__(self, ready: bool):
        self._ready = ready

    def is_ready(self):
        return self._ready

    def snapshot_stats(self):
        return {"active_streams": 1, "total_detections": 2, "uptime_seconds": 3}

    def _analyze_image(self, **_kwargs):
        return True, "ok", {"detections": []}

    def _analyze_video_test(self, **_kwargs):
        return True, "ok", {"llm_result": '{"alarm":"0","reason":"","anomaly_times":[]}'}


class StopCallbackTests(unittest.TestCase):
    def test_grpc_stop_sends_user_requested(self):
        servicer = main.AnalysisServiceServicer("dummy.onnx")
        task = _FakeTask()
        servicer._camera_tasks["cam_01"] = task

        resp = servicer.StopCamera(analysis_pb2.StopCameraRequest(camera_id="cam_01"), None)

        self.assertTrue(resp.success)
        self.assertTrue(task.stop_called)
        self.assertEqual(task.stopped_reason, "user_requested")
        self.assertEqual(task.stopped_message, "task stopped by user request")

    def test_http_stop_sends_user_requested(self):
        task = _FakeTask()
        servicer = type("S", (), {})()
        servicer._lock = threading.Lock()
        servicer._camera_tasks = {"cam_01": task}

        http_api.APIHandler.servicer = servicer
        handler = object.__new__(http_api.APIHandler)
        status, result = handler._stop_camera_one({"camera_id": "cam_01"})

        self.assertEqual(status, 200)
        self.assertTrue(result.get("success"))
        self.assertTrue(task.stop_called)
        self.assertEqual(task.stopped_reason, "user_requested")
        self.assertEqual(task.stopped_message, "task stopped by user request")


class HTTPAPIFaultToleranceTests(unittest.TestCase):
    def test_dispatch_request_returns_json_500_on_unhandled_exception(self):
        handler = object.__new__(http_api.APIHandler)
        handler.path = "/api/analyze_image"
        handler.command = "POST"
        captured = {}

        def fake_send(status, data):
            captured["status"] = status
            captured["data"] = data

        def boom():
            raise RuntimeError("boom")

        handler._safe_send_json = fake_send
        handler._handle_analyze_image = boom

        handler._dispatch_request("POST")

        self.assertEqual(captured["status"], 500)
        self.assertFalse(captured["data"]["success"])
        self.assertEqual(captured["data"]["message"], "AI 服务内部错误")

    def test_start_http_server_uses_threaded_server(self):
        servicer = _DummyServicer(ready=True)
        server = http_api.start_http_server(servicer, port=0)
        try:
            self.assertIsInstance(server, http_api.ThreadedAPIHTTPServer)
        finally:
            server.shutdown()
            server.server_close()

    def test_analyze_image_logs_full_lifecycle(self):
        http_api.APIHandler.servicer = _DummyServicer(ready=True)
        handler = object.__new__(http_api.APIHandler)
        handler.path = "/api/analyze_image"
        handler.command = "POST"
        handler._read_json = lambda: {
            "image_rel_path": "20260326/batch-a/sample.jpg",
            "algorithm_configs": [{"algorithm_id": "alg-1"}],
            "llm_api_url": "",
            "llm_api_key": "",
            "llm_model": "",
            "llm_prompt": "",
        }
        handler._send_json = lambda status, data: None

        with self.assertLogs("HTTP", level="INFO") as captured:
            handler._handle_analyze_image()

        joined = "\n".join(captured.output)
        self.assertIn("request accepted", joined)
        self.assertIn("business finished", joined)
        self.assertIn("response sent", joined)

    def test_analyze_video_logs_full_lifecycle(self):
        http_api.APIHandler.servicer = _DummyServicer(ready=True)
        handler = object.__new__(http_api.APIHandler)
        handler.path = "/api/analyze_video_test"
        handler.command = "POST"
        handler._read_json = lambda: {
            "video_rel_path": "20260326/batch-b/sample.mp4",
            "fps": 1,
            "algorithm_configs": [{"algorithm_id": "alg-1"}],
            "llm_api_url": "",
            "llm_api_key": "",
            "llm_model": "",
            "llm_prompt": "",
        }
        handler._send_json = lambda status, data: None

        with self.assertLogs("HTTP", level="INFO") as captured:
            handler._handle_analyze_video_test()

        joined = "\n".join(captured.output)
        self.assertIn("request accepted", joined)
        self.assertIn("business finished", joined)
        self.assertIn("response sent", joined)


class KeepaliveCadenceTests(unittest.TestCase):
    def test_keepalive_loop_uses_configured_interval(self):
        servicer = _DummyServicer(ready=True)
        sleep_calls = []
        sent_stats = []

        class _BreakLoop(Exception):
            pass

        def fake_sleep(sec):
            sleep_calls.append(sec)
            if len(sleep_calls) >= 2:
                raise _BreakLoop()

        with patch.object(main.time, "sleep", side_effect=fake_sleep):
            with patch.object(main, "send_keepalive_callback", side_effect=lambda s: sent_stats.append(s)):
                with self.assertRaises(_BreakLoop):
                    main._keepalive_loop(servicer, interval_sec=7)

        self.assertEqual(sleep_calls[0], 7)
        self.assertEqual(len(sent_stats), 1)
        self.assertEqual(sent_stats[0]["active_streams"], 1)

    def test_keepalive_loop_skips_when_not_ready(self):
        servicer = _DummyServicer(ready=False)
        sent_stats = []
        sleep_calls = []

        class _BreakLoop(Exception):
            pass

        def fake_sleep(sec):
            sleep_calls.append(sec)
            if len(sleep_calls) >= 2:
                raise _BreakLoop()

        with patch.object(main.time, "sleep", side_effect=fake_sleep):
            with patch.object(main, "send_keepalive_callback", side_effect=lambda s: sent_stats.append(s)):
                with self.assertRaises(_BreakLoop):
                    main._keepalive_loop(servicer, interval_sec=9)

        self.assertEqual(sleep_calls[0], 9)
        self.assertEqual(len(sent_stats), 0)


class LLMFailureDiagnosticsTests(unittest.TestCase):
    def test_classify_llm_failure_variants(self):
        self.assertEqual(
            llm_client._classify_llm_failure(ConnectionError("Connection error.")),
            "connect",
        )
        self.assertEqual(
            llm_client._classify_llm_failure(TimeoutError("request timed out")),
            "timeout",
        )
        self.assertEqual(
            llm_client._classify_llm_failure(
                ssl.SSLEOFError(8, "UNEXPECTED_EOF_WHILE_READING")
            ),
            "tls",
        )
        self.assertEqual(
            llm_client._classify_llm_failure(
                status_code=429, error_message="quota exceeded"
            ),
            "provider_status",
        )
        self.assertEqual(
            llm_client._classify_llm_failure(
                call_status="empty_content",
                error_message="LLM API returned empty message.content",
            ),
            "empty_content",
        )

    def test_image_llm_failure_logs_failure_type(self):
        class _FakeOpenAI:
            def __init__(self, *args, **kwargs):
                self.chat = SimpleNamespace(
                    completions=SimpleNamespace(create=self._create)
                )

            def _create(self, **kwargs):
                raise ConnectionError("Connection error.")

        with patch.dict(sys.modules, {"openai": SimpleNamespace(OpenAI=_FakeOpenAI)}):
            with self.assertLogs("LLM", level="ERROR") as captured:
                result = llm_client.call_llm_with_images(
                    api_url="https://dashscope.aliyuncs.com/compatible-mode/v1",
                    api_key="test-key",
                    model="qwen3-vl-plus",
                    prompt="detect crowding",
                    images=[{"image_b64": "ZmFrZQ==", "mime_type": "image/jpeg"}],
                    log_context="image_test",
                )

        self.assertFalse(result.success)
        self.assertEqual(result.call_status, "error")
        joined = "\n".join(captured.output)
        self.assertIn("failure_type=connect", joined)
        self.assertIn("context=image_test", joined)
        self.assertIn("provider_host=dashscope.aliyuncs.com", joined)

    def test_video_llm_failure_logs_failure_type(self):
        class _FakeMultiModalConversation:
            @staticmethod
            def call(*args, **kwargs):
                raise ssl.SSLEOFError(8, "UNEXPECTED_EOF_WHILE_READING")

        fake_dashscope = SimpleNamespace(
            base_http_api_url="",
            MultiModalConversation=_FakeMultiModalConversation,
        )
        with tempfile.NamedTemporaryFile(suffix=".mp4", delete=False) as temp_video:
            temp_video.write(b"fake-video")
            video_path = temp_video.name
        try:
            with patch.dict(sys.modules, {"dashscope": fake_dashscope}):
                with self.assertLogs("LLM", level="ERROR") as captured:
                    result = llm_client.call_llm_with_video(
                        api_url="https://dashscope.aliyuncs.com/compatible-mode/v1",
                        api_key="test-key",
                        model="qwen3-vl-plus",
                        prompt="detect anomaly",
                        video_path=video_path,
                        mime_type="video/mp4",
                        log_context="video_test",
                    )
        finally:
            if os.path.exists(video_path):
                os.unlink(video_path)

        self.assertFalse(result.success)
        self.assertEqual(result.call_status, "error")
        joined = "\n".join(captured.output)
        self.assertIn("failure_type=tls", joined)
        self.assertIn("context=video_test", joined)
        self.assertIn("provider_host=dashscope.aliyuncs.com", joined)


class SnapshotSourceTests(unittest.TestCase):
    def test_detection_callback_snapshot_uses_raw_frame(self):
        frame = np.zeros((24, 24, 3), dtype=np.uint8)
        frame[:, :] = (24, 88, 160)
        ok, encoded = cv2.imencode(".jpg", frame)
        self.assertTrue(ok)
        expected_snapshot = base64.b64encode(encoded).decode("utf-8")

        task = object.__new__(main.CameraTask)
        task.camera_id = "cam-raw-1"
        task.detect_mode = 1
        task.algorithm_configs = []
        task.config = {}
        task.total_detections = 0
        task._on_detection_callback = None

        detections = [
            {
                "label": "person",
                "confidence": 0.9,
                "box": {"x_min": 2, "y_min": 3, "x_max": 16, "y_max": 20},
            }
        ]
        with patch.object(main, "send_callback") as mock_send:
            task._send_detection_callback(detections, frame)

        self.assertEqual(mock_send.call_count, 1)
        _, _, payload = mock_send.call_args.args
        self.assertEqual(payload["snapshot"], expected_snapshot)
        self.assertEqual(payload["snapshot_width"], frame.shape[1])
        self.assertEqual(payload["snapshot_height"], frame.shape[0])

    def test_detection_callback_forwards_llm_usage(self):
        frame = np.zeros((16, 16, 3), dtype=np.uint8)
        task = object.__new__(main.CameraTask)
        task.camera_id = "cam-llm-usage-1"
        task.detect_mode = 2
        task.algorithm_configs = []
        task.config = {}
        task.total_detections = 0
        task._on_detection_callback = None

        llm_usage = {
            "call_id": "call-test-1",
            "call_status": "success",
            "usage_available": True,
            "prompt_tokens": 12,
            "completion_tokens": 8,
            "total_tokens": 20,
            "latency_ms": 123.4,
            "model": "qwen-test",
            "error_message": "",
            "request_context": "camera_id=cam-llm-usage-1",
        }

        with patch.object(main, "send_callback") as mock_send:
            task._send_detection_callback([], frame, llm_result="", llm_usage=llm_usage)

        self.assertEqual(mock_send.call_count, 1)
        _, _, payload = mock_send.call_args.args
        self.assertIn("llm_usage", payload)
        self.assertEqual(payload["llm_usage"]["call_id"], "call-test-1")
        self.assertEqual(payload["llm_usage"]["total_tokens"], 20)

    def test_analyze_image_snapshot_uses_raw_frame(self):
        frame = np.zeros((18, 30, 3), dtype=np.uint8)
        frame[:, :] = (32, 120, 10)
        ok, encoded = cv2.imencode(".jpg", frame)
        self.assertTrue(ok)
        expected_snapshot = base64.b64encode(encoded).decode("utf-8")

        servicer = main.AnalysisServiceServicer("dummy.onnx")
        servicer._run_yolo_once = lambda *_args, **_kwargs: [
            {
                "label": "person",
                "confidence": 0.8,
                "box": {"x_min": 1, "y_min": 1, "x_max": 10, "y_max": 12},
            }
        ]

        ok, message, payload = servicer._analyze_image(
            camera_id="cam-analyze-1",
            image_base64=expected_snapshot,
            raw_algorithm_configs=[
                {
                    "algorithm_id": "alg-analyze-1",
                    "task_code": "ALG_ANALYZE_1",
                    "detect_mode": 1,
                    "labels": ["person"],
                    "yolo_threshold": 0.5,
                    "iou_threshold": 0.8,
                    "labels_trigger_mode": "any",
                }
            ],
            llm_api_url="",
            llm_api_key="",
            llm_model="",
            llm_prompt="",
        )

        self.assertTrue(ok)
        self.assertEqual(message, "ok")
        self.assertEqual(payload["snapshot"], expected_snapshot)
        self.assertEqual(payload["snapshot_width"], frame.shape[1])
        self.assertEqual(payload["snapshot_height"], frame.shape[0])

    def test_analyze_image_returns_llm_usage(self):
        frame = np.zeros((18, 30, 3), dtype=np.uint8)
        frame[:, :] = (32, 120, 10)
        ok, encoded = cv2.imencode(".jpg", frame)
        self.assertTrue(ok)
        image_base64 = base64.b64encode(encoded).decode("utf-8")

        servicer = main.AnalysisServiceServicer("dummy.onnx")
        mock_result = main.LLMCallResult(
            call_id="call-analyze-1",
            content="detection done",
            success=True,
            call_status="success",
            error_message="",
            model="qwen-test",
            latency_ms=88.5,
            prompt_tokens=21,
            completion_tokens=9,
            total_tokens=30,
            usage_available=True,
            request_context="analyze_image camera_id=cam-analyze-llm-1, detect_mode=per_algorithm",
        )

        with patch.object(main, "call_llm", return_value=mock_result):
            ok, message, payload = servicer._analyze_image(
                camera_id="cam-analyze-llm-1",
                image_base64=image_base64,
                raw_algorithm_configs=[
                    {
                        "algorithm_id": "alg-analyze-llm-1",
                        "task_code": "ALG_ANALYZE_LLM_1",
                        "detect_mode": 2,
                        "labels": [],
                        "yolo_threshold": 0.5,
                        "iou_threshold": 0.8,
                        "labels_trigger_mode": "any",
                    }
                ],
                llm_api_url="http://llm.example.com/v1",
                llm_api_key="test-key",
                llm_model="qwen-test",
                llm_prompt="test prompt",
            )

        self.assertTrue(ok)
        self.assertEqual(message, "ok")
        self.assertEqual(payload["llm_result"], "detection done")
        self.assertIn("llm_usage", payload)
        self.assertEqual(payload["llm_usage"]["call_id"], "call-analyze-1")
        self.assertEqual(payload["llm_usage"]["prompt_tokens"], 21)
        self.assertEqual(payload["llm_usage"]["total_tokens"], 30)

    def test_analyze_image_parses_fenced_llm_json_to_algorithm_results(self):
        frame = np.zeros((18, 30, 3), dtype=np.uint8)
        frame[:, :] = (32, 120, 10)
        ok, encoded = cv2.imencode(".jpg", frame)
        self.assertTrue(ok)
        image_base64 = base64.b64encode(encoded).decode("utf-8")

        servicer = main.AnalysisServiceServicer("dummy.onnx")
        fenced_llm_result = """```json
{
  "task_results": [
    {
      "task_code": "ALG_FENCED",
      "alarm": 1,
      "reason": "fenced json parsed",
      "object_ids": ["obj-1"]
    }
  ],
  "objects": [
    {
      "object_id": "obj-1",
      "task_code": "ALG_FENCED",
      "bbox2d": [100, 100, 500, 500],
      "label": "person",
      "confidence": 0.88
    }
  ]
}
```"""
        mock_result = main.LLMCallResult(
            call_id="call-analyze-fenced-1",
            content=fenced_llm_result,
            success=True,
            call_status="success",
            error_message="",
            model="qwen-test",
            latency_ms=60.0,
            prompt_tokens=22,
            completion_tokens=10,
            total_tokens=32,
            usage_available=True,
            request_context="analyze_image fenced",
        )

        with patch.object(main, "call_llm", return_value=mock_result):
            ok, message, payload = servicer._analyze_image(
                camera_id="cam-analyze-fenced-1",
                image_base64=image_base64,
                raw_algorithm_configs=[
                    {
                        "algorithm_id": "alg-analyze-fenced-1",
                        "task_code": "ALG_FENCED",
                        "detect_mode": 2,
                        "labels": [],
                        "yolo_threshold": 0.5,
                        "iou_threshold": 0.8,
                        "labels_trigger_mode": "any",
                    }
                ],
                llm_api_url="http://llm.example.com/v1",
                llm_api_key="test-key",
                llm_model="qwen-test",
                llm_prompt="test prompt",
            )

        self.assertTrue(ok)
        self.assertEqual(message, "ok")
        results = payload.get("algorithm_results") or []
        self.assertEqual(len(results), 1)
        self.assertEqual(results[0]["algorithm_id"], "alg-analyze-fenced-1")
        self.assertEqual(results[0]["source"], "llm")


class AlgorithmDecisionTests(unittest.TestCase):
    def _new_task(self, configs):
        task = object.__new__(main.CameraTask)
        task.camera_id = "cam-algorithm-decision"
        task.detect_mode = 3
        task.config = {}
        task.algorithm_configs = main.CameraTask._normalize_algorithm_configs(configs)
        task._last_llm_detections_by_algorithm = {}
        task.llm_api_url = "http://llm.example.com/v1"
        task.llm_api_key = "test-key"
        task.llm_model = "qwen-test"
        task.llm_prompt = "test prompt"
        task.total_detections = 0
        task._on_detection_callback = None
        return task

    @staticmethod
    def _det(label: str, confidence: float):
        return {
            "label": label,
            "confidence": confidence,
            "box": {"x_min": 1, "y_min": 1, "x_max": 10, "y_max": 12},
            "area": 99,
        }

    def test_labels_trigger_mode_any_all(self):
        task = self._new_task([
            {
                "algorithm_id": "alg_any",
                "task_code": "ALG_ANY",
                "labels": ["person", "car"],
                "yolo_threshold": 0.5,
                "iou_threshold": 0.8,
                "labels_trigger_mode": "any",
            },
            {
                "algorithm_id": "alg_all",
                "task_code": "ALG_ALL",
                "labels": ["person", "car"],
                "yolo_threshold": 0.5,
                "iou_threshold": 0.8,
                "labels_trigger_mode": "all",
            },
        ])

        evals = task._evaluate_algorithm_hits([self._det("person", 0.9)])
        by_id = {item["config"]["algorithm_id"]: item for item in evals}
        self.assertTrue(by_id["alg_any"]["small_alarm"])
        self.assertFalse(by_id["alg_all"]["small_alarm"])

        evals = task._evaluate_algorithm_hits(
            [self._det("person", 0.9), self._det("car", 0.88)]
        )
        by_id = {item["config"]["algorithm_id"]: item for item in evals}
        self.assertTrue(by_id["alg_any"]["small_alarm"])
        self.assertTrue(by_id["alg_all"]["small_alarm"])

    def test_mode3_only_returns_llm_results(self):
        task = self._new_task([
            {
                "algorithm_id": "alg_m3",
                "task_code": "ALG_M3",
                "labels": ["person"],
                "detect_mode": 3,
                "yolo_threshold": 0.5,
                "iou_threshold": 0.8,
                "labels_trigger_mode": "any",
            },
        ])

        frame = np.zeros((32, 32, 3), dtype=np.uint8)
        det = self._det("person", 0.91)
        llm_result = json.dumps(
            {
                "task_results": [
                    {
                        "task_code": "ALG_M3",
                        "alarm": 1,
                        "reason": "llm positive",
                        "object_ids": ["obj-1"],
                    }
                ],
                "objects": [
                    {
                        "object_id": "obj-1",
                        "task_code": "ALG_M3",
                        "bbox2d": [100, 100, 500, 500],
                        "label": "person",
                        "confidence": 0.77,
                    }
                ],
            }
        )
        llm_call = main.LLMCallResult(
            call_id="call-mode3-1",
            content=llm_result,
            success=True,
            call_status="success",
            error_message="",
            model="qwen-test",
            latency_ms=50.0,
            prompt_tokens=10,
            completion_tokens=6,
            total_tokens=16,
            usage_available=True,
            request_context="mode3-test",
        )

        with patch.object(task, "_run_yolo_candidates", return_value=[det]):
            with patch.object(task, "_call_llm_with_frame", return_value=llm_call):
                with patch.object(task, "_send_detection_callback") as mock_send:
                    task._process_per_algorithm_modes(frame)

        self.assertEqual(mock_send.call_count, 1)
        kwargs = mock_send.call_args.kwargs
        results = kwargs.get("algorithm_results") or []
        self.assertEqual(len(results), 1)
        self.assertEqual(results[0]["algorithm_id"], "alg_m3")
        self.assertEqual(results[0]["source"], "llm")

    def test_mode2_present_skips_mode3_small_filter(self):
        task = self._new_task([
            {
                "algorithm_id": "alg_mode2",
                "task_code": "ALG_MODE2",
                "detect_mode": 2,
                "labels": [],
                "yolo_threshold": 0.5,
                "iou_threshold": 0.8,
                "labels_trigger_mode": "any",
            },
            {
                "algorithm_id": "alg_mode3",
                "task_code": "ALG_MODE3",
                "detect_mode": 3,
                "labels": ["person"],
                "yolo_threshold": 0.5,
                "iou_threshold": 0.8,
                "labels_trigger_mode": "any",
            },
        ])

        frame = np.zeros((32, 32, 3), dtype=np.uint8)
        llm_result = json.dumps(
            {
                "task_results": [
                    {
                        "task_code": "ALG_MODE3",
                        "alarm": 1,
                        "reason": "llm positive for mode3",
                    }
                ],
                "objects": [],
            }
        )
        llm_call = main.LLMCallResult(
            call_id="call-mode2-skip-mode3",
            content=llm_result,
            success=True,
            call_status="success",
            error_message="",
            model="qwen-test",
            latency_ms=30.0,
            prompt_tokens=8,
            completion_tokens=4,
            total_tokens=12,
            usage_available=True,
            request_context="mode2-skip-mode3",
        )

        with patch.object(task, "_run_yolo_candidates") as mock_yolo:
            with patch.object(task, "_call_llm_with_frame", return_value=llm_call):
                with patch.object(task, "_send_detection_callback") as mock_send:
                    task._process_per_algorithm_modes(frame)

        mock_yolo.assert_not_called()
        self.assertEqual(mock_send.call_count, 1)
        kwargs = mock_send.call_args.kwargs
        results = kwargs.get("algorithm_results") or []
        by_id = {item["algorithm_id"]: item for item in results}
        self.assertIn("alg_mode3", by_id)
        self.assertEqual(by_id["alg_mode3"]["source"], "llm")

    def test_build_llm_algorithm_results_parses_fenced_json(self):
        task = self._new_task([
            {
                "algorithm_id": "alg_fenced",
                "task_code": "ALG_FENCED",
                "detect_mode": 3,
                "labels": ["person"],
                "yolo_threshold": 0.5,
                "iou_threshold": 0.8,
                "labels_trigger_mode": "any",
            }
        ])
        fenced_llm_result = """```json
{
  "task_results": [
    {
      "task_code": "ALG_FENCED",
      "alarm": 1,
      "reason": "fenced parsed",
      "object_ids": ["obj-1"]
    }
  ],
  "objects": [
    {
      "object_id": "obj-1",
      "task_code": "ALG_FENCED",
      "bbox2d": [100, 100, 500, 500],
      "label": "person",
      "confidence": 0.77
    }
  ]
}
```"""
        results = task._build_llm_algorithm_results(
            fenced_llm_result,
            {"alg_fenced"},
            1920,
            1080,
            "call-fenced-1",
        )
        self.assertEqual(len(results), 1)
        self.assertEqual(results[0]["algorithm_id"], "alg_fenced")
        self.assertEqual(results[0]["source"], "llm")

    def test_mode1_and_mode3_merge_small_and_llm_results(self):
        task = self._new_task([
            {
                "algorithm_id": "alg_small",
                "task_code": "ALG_SMALL",
                "detect_mode": 1,
                "labels": ["person"],
                "yolo_threshold": 0.5,
                "iou_threshold": 0.8,
                "labels_trigger_mode": "any",
            },
            {
                "algorithm_id": "alg_llm",
                "task_code": "ALG_LLM",
                "detect_mode": 3,
                "labels": ["person"],
                "yolo_threshold": 0.5,
                "iou_threshold": 0.8,
                "labels_trigger_mode": "any",
            },
        ])

        frame = np.zeros((32, 32, 3), dtype=np.uint8)
        det = self._det("person", 0.93)
        llm_result = """```json
{
  "task_results": [
    {
      "task_code": "ALG_LLM",
      "alarm": 1,
      "reason": "llm positive",
      "object_ids": ["obj-1"]
    }
  ],
  "objects": [
    {
      "object_id": "obj-1",
      "task_code": "ALG_LLM",
      "bbox2d": [100, 100, 500, 500],
      "label": "person",
      "confidence": 0.8
    }
  ]
}
```"""
        llm_call = main.LLMCallResult(
            call_id="call-mode1-mode3-1",
            content=llm_result,
            success=True,
            call_status="success",
            error_message="",
            model="qwen-test",
            latency_ms=40.0,
            prompt_tokens=12,
            completion_tokens=7,
            total_tokens=19,
            usage_available=True,
            request_context="mode1+mode3-test",
        )

        with patch.object(task, "_run_yolo_candidates", return_value=[det]):
            with patch.object(task, "_call_llm_with_frame", return_value=llm_call):
                with patch.object(task, "_send_detection_callback") as mock_send:
                    task._process_per_algorithm_modes(frame)

        self.assertEqual(mock_send.call_count, 1)
        kwargs = mock_send.call_args.kwargs
        results = kwargs.get("algorithm_results") or []
        by_id = {item["algorithm_id"]: item for item in results}
        self.assertEqual(by_id["alg_small"]["source"], "small")
        self.assertEqual(by_id["alg_llm"]["source"], "llm")


class FrameSamplingExprTests(unittest.TestCase):
    def test_build_sampling_expr_fps(self):
        self.assertEqual(frame_capture.build_sampling_expr("fps", 5), "fps=5")

    def test_build_sampling_expr_interval(self):
        self.assertEqual(frame_capture.build_sampling_expr("interval", 5), "fps=1/5")

    def test_build_sampling_expr_invalid_fallback(self):
        self.assertEqual(frame_capture.build_sampling_expr("bad-mode", 0), "fps=5")


class StartCameraProbeFailureTests(unittest.TestCase):
    def test_http_start_camera_requires_algorithm_configs(self):
        servicer = type("S", (), {})()
        servicer._lock = threading.Lock()
        servicer._camera_tasks = {}
        servicer.object_detector = object()
        servicer.motion_detector = object()
        servicer.record_detections = lambda *_args, **_kwargs: None
        servicer.is_ready = lambda: True
        http_api.APIHandler.servicer = servicer
        handler = object.__new__(http_api.APIHandler)
        status, result = handler._start_camera_one({
            "camera_id": "cam-missing-config",
            "rtsp_url": "rtsp://127.0.0.1:1554/live/cam-missing-config",
            "callback_url": "http://127.0.0.1:15123/ai",
        })
        self.assertEqual(status, 400)
        self.assertIn("algorithm_configs", str(result.get("message", "")))

    def test_http_start_camera_accepts_pending_stream_probe(self):
        created_tasks = []

        class _FakeCapture:
            def __init__(self):
                self.width = 0
                self.height = 0
                self.fps = 0.0
                self.is_failed = False
                self.last_error = "probe stream info failed: mock timeout"

            def get_stream_info(self):
                return self.width, self.height, self.fps

        class _FakeCameraTask:
            def __init__(self, *args, **kwargs):
                self.capture = _FakeCapture()
                self.stop_called = False
                self.stream_retry_limit = 20
                created_tasks.append(self)

            def start(self):
                return

            def stop(self):
                self.stop_called = True

        servicer = type("S", (), {})()
        servicer._lock = threading.Lock()
        servicer._camera_tasks = {}
        servicer.object_detector = object()
        servicer.motion_detector = object()
        servicer.record_detections = lambda *_args, **_kwargs: None
        servicer.is_ready = lambda: True
        http_api.APIHandler.servicer = servicer
        handler = object.__new__(http_api.APIHandler)

        tick = {"count": 0}

        def fake_time():
            tick["count"] += 1
            if tick["count"] <= 2:
                return 100.0
            return 106.0

        with patch.object(main, "CameraTask", _FakeCameraTask):
            with patch.object(main.time, "time", side_effect=fake_time):
                with patch.object(main.time, "sleep", return_value=None):
                    status, result = handler._start_camera_one({
                        "camera_id": "cam-probe-fail-1",
                        "rtsp_url": "rtsp://127.0.0.1:1554/rtp/cam-probe-fail-1",
                        "callback_url": "http://127.0.0.1:15123/ai",
                        "algorithm_configs": [
                            {
                                "algorithm_id": "alg_probe_1",
                                "task_code": "ALG_PROBE_1",
                                "detect_mode": 3,
                                "labels": ["person"],
                                "yolo_threshold": 0.5,
                                "iou_threshold": 0.8,
                                "labels_trigger_mode": "any",
                            }
                        ],
                    })

        self.assertEqual(status, 200)
        self.assertTrue(result.get("success"))
        self.assertTrue(str(result.get("message", "")).strip())
        self.assertEqual(result.get("camera_id"), "cam-probe-fail-1")
        self.assertEqual(result.get("source_width"), 0)
        self.assertEqual(result.get("source_height"), 0)
        self.assertEqual(len(created_tasks), 1)
        self.assertFalse(created_tasks[0].stop_called)
        self.assertIn("cam-probe-fail-1", servicer._camera_tasks)


class FrameCaptureRetryBudgetTests(unittest.TestCase):
    def test_capture_loop_recovers_before_retry_limit(self):
        capture = frame_capture.FrameCapture(
            "rtsp://127.0.0.1:1554/recover",
            queue.Queue(),
            retry_limit=3,
        )
        attempts = {"count": 0}

        class _FakeLogPipe:
            def __init__(self, _name: str):
                pass

            def fileno(self):
                return 1

            def dump(self):
                return None

            def close(self):
                return None

        class _FakeProcess:
            stdout = object()

            def poll(self):
                return None

            def terminate(self):
                return None

            def wait(self, timeout=None):
                return 0

            def kill(self):
                return None

        def fake_get_stream_info():
            attempts["count"] += 1
            if attempts["count"] < 3:
                capture.last_error = f"probe stream info failed: transient-{attempts['count']}"
                return False
            capture.width = 1920
            capture.height = 1080
            capture.fps = 15.0
            capture._stop_event.set()
            return True

        with patch.object(capture, "_get_stream_info", side_effect=fake_get_stream_info):
            with patch.object(frame_capture, "LogPipe", _FakeLogPipe):
                with patch.object(frame_capture.subprocess, "Popen", return_value=_FakeProcess()):
                    with patch.object(frame_capture.time, "sleep", return_value=None):
                        capture._capture_loop()

        self.assertFalse(capture.is_failed)
        self.assertEqual(capture.error_count, 0)
        self.assertEqual(capture.width, 1920)
        self.assertEqual(capture.height, 1080)

    def test_capture_loop_marks_failed_after_retry_limit(self):
        capture = frame_capture.FrameCapture(
            "rtsp://127.0.0.1:1554/fail",
            queue.Queue(),
            retry_limit=3,
        )

        def fake_get_stream_info():
            capture.last_error = "probe stream info failed: permanent timeout"
            return False

        with patch.object(capture, "_get_stream_info", side_effect=fake_get_stream_info):
            with patch.object(frame_capture.time, "sleep", return_value=None):
                capture._capture_loop()

        self.assertTrue(capture.is_failed)
        self.assertEqual(capture.error_count, 3)
        self.assertEqual(capture.last_error, "probe stream info failed: permanent timeout")


class VideoAnalyzeTests(unittest.TestCase):
    def _legacy_analyze_video_test_uses_local_temp_file_and_cleans_up(self):
        servicer = main.AnalysisServiceServicer("dummy.onnx")
        video_bytes = b"fake-video-binary-content"
        video_b64 = base64.b64encode(video_bytes).decode("utf-8")
        captured: dict[str, str | int] = {}

        def fake_call_llm_with_video(**kwargs):
            video_path = kwargs["video_path"]
            captured["video_path"] = video_path
            captured["prompt"] = kwargs["prompt"]
            captured["fps"] = kwargs["fps"]
            self.assertTrue(os.path.exists(video_path))
            with open(video_path, "rb") as fp:
                self.assertEqual(fp.read(), video_bytes)
            return main.LLMCallResult(
                call_id="call-video-test-1",
                content=json.dumps(
                    {
                        "alarm": "1",
                        "reason": "视频中存在聚集现象",
                        "anomaly_times": [
                            {
                                "timestamp_ms": 5000,
                                "timestamp_text": "00:05",
                                "reason": "多人持续聚集",
                            }
                        ],
                    },
                    ensure_ascii=False,
                ),
                success=True,
                call_status="success",
                error_message="",
                model="qwen-test",
                latency_ms=12.3,
                prompt_tokens=10,
                completion_tokens=12,
                total_tokens=22,
                usage_available=True,
                request_context="analyze_video_test",
            )

        with patch.object(main, "call_llm_with_video", side_effect=fake_call_llm_with_video):
            ok, message, payload = servicer._analyze_video_test(
                camera_id="cam-video-1",
                video_base64=video_b64,
                mime_type="video/mp4",
                fps=1,
                raw_algorithm_configs=[
                    {
                        "algorithm_id": "alg-video-1",
                        "task_code": "ALG_VIDEO_1",
                        "detect_mode": 2,
                        "labels": [],
                        "yolo_threshold": 0.5,
                        "iou_threshold": 0.8,
                        "labels_trigger_mode": "any",
                    }
                ],
                llm_api_url="https://dashscope.aliyuncs.com/compatible-mode/v1",
                llm_api_key="sk-test",
                llm_model="qwen3-vl-flash",
                llm_prompt="只判断是否存在人员聚集",
                duration_seconds=12,
            )

        self.assertTrue(ok)
        self.assertEqual(message, "ok")
        self.assertEqual(captured["fps"], 1)
        self.assertNotIn("输入帧时间顺序", str(captured["prompt"]))
        self.assertNotIn("抽帧", str(captured["prompt"]))
        self.assertFalse(os.path.exists(str(captured["video_path"])))
        self.assertEqual(payload["basis"], "视频中存在聚集现象")
        self.assertEqual(len(payload["anomaly_times"]), 1)

    def test_analyze_video_test_uses_shared_video_path(self):
        servicer = main.AnalysisServiceServicer("dummy.onnx")
        video_bytes = b"fake-video-binary-content"
        captured: dict[str, str | int] = {}

        with tempfile.TemporaryDirectory() as tmpdir:
            rel_path = os.path.join("20260325", "batch-test", "sample.mp4")
            full_path = os.path.join(tmpdir, rel_path)
            os.makedirs(os.path.dirname(full_path), exist_ok=True)
            with open(full_path, "wb") as fp:
                fp.write(video_bytes)

            def fake_call_llm_with_video(**kwargs):
                video_path = kwargs["video_path"]
                captured["video_path"] = video_path
                captured["prompt"] = kwargs["prompt"]
                captured["fps"] = kwargs["fps"]
                self.assertTrue(os.path.exists(video_path))
                with open(video_path, "rb") as fp:
                    self.assertEqual(fp.read(), video_bytes)
                return main.LLMCallResult(
                    call_id="call-video-test-1",
                    content=json.dumps(
                        {
                            "alarm": "1",
                            "reason": "视频中存在聚集现象",
                            "anomaly_times": [
                                {
                                    "timestamp_ms": 5000,
                                    "timestamp_text": "00:05",
                                    "reason": "多人持续聚集",
                                }
                            ],
                        },
                        ensure_ascii=False,
                    ),
                    success=True,
                    call_status="success",
                    error_message="",
                    model="qwen-test",
                    latency_ms=12.3,
                    prompt_tokens=10,
                    completion_tokens=12,
                    total_tokens=22,
                    usage_available=True,
                    request_context="analyze_video_test",
                )

            with patch.dict(main.GLOBAL_CONFIG, {"algorithm_test_root": tmpdir}, clear=False):
                with patch.object(main, "call_llm_with_video", side_effect=fake_call_llm_with_video):
                    ok, message, payload = servicer._analyze_video_test(
                        video_rel_path=rel_path,
                        fps=1,
                        raw_algorithm_configs=[
                            {
                                "algorithm_id": "alg-video-1",
                                "task_code": "ALG_VIDEO_1",
                                "detect_mode": 2,
                                "labels": [],
                                "yolo_threshold": 0.5,
                                "iou_threshold": 0.8,
                                "labels_trigger_mode": "any",
                            }
                        ],
                        llm_api_url="https://dashscope.aliyuncs.com/compatible-mode/v1",
                        llm_api_key="sk-test",
                        llm_model="qwen3-vl-flash",
                        llm_prompt="只判断是否存在人员聚集",
                    )

            self.assertTrue(ok)
            self.assertEqual(message, "ok")
            self.assertEqual(captured["fps"], 1)
            self.assertNotIn("输入帧时间顺序", str(captured["prompt"]))
            self.assertNotIn("抽帧", str(captured["prompt"]))
            self.assertEqual(str(captured["video_path"]), full_path)
            self.assertTrue(os.path.exists(str(captured["video_path"])))
            self.assertIn('"alarm"', payload["llm_result"])
            self.assertIn('"anomaly_times"', payload["llm_result"])

    def test_video_llm_rejects_non_dashscope_provider(self):
        result = llm_client.call_llm_with_video(
            api_url="https://api.openai.com/v1",
            api_key="sk-test",
            model="qwen3-vl-flash",
            prompt="测试视频",
            video_path=__file__,
            mime_type="video/mp4",
            fps=1,
            timeout=10.0,
            log_context="test_non_dashscope_video",
        )
        self.assertFalse(result.success)
        self.assertIn("DashScope", result.error_message)


if __name__ == "__main__":
    unittest.main()
