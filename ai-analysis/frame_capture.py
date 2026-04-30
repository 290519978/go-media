from collections import deque
import logging
import os
import queue
import select
import subprocess
import threading
import time
from typing import Deque, Optional
import numpy as np


slog = logging.getLogger("Capture")

DEFAULT_STREAM_RETRY_LIMIT = 20


def build_sampling_expr(detect_rate_mode: str, detect_rate_value: int) -> str:
    mode = str(detect_rate_mode or "").strip().lower()
    try:
        value = int(detect_rate_value or 0)
    except (TypeError, ValueError):
        value = 0
    if mode not in {"fps", "interval"}:
        slog.warning("invalid detect_rate_mode=%s, fallback to fps", detect_rate_mode)
        mode = "fps"
    if value <= 0:
        slog.warning("invalid detect_rate_value=%s, fallback to 5", detect_rate_value)
        value = 5
    if value > 60:
        slog.warning("detect_rate_value=%s out of range, clamp to 60", detect_rate_value)
        value = 60
    if mode == "interval":
        return f"fps=1/{value}"
    return f"fps={value}"


class LogPipe(threading.Thread):
    def __init__(self, log_name: str):
        super().__init__(daemon=True)
        self.logger = logging.getLogger(log_name)
        self.deque: Deque[str] = deque(maxlen=100)
        self.fd_read, self.fd_write = os.pipe()
        self.pipe_reader = os.fdopen(self.fd_read)
        self._closed = False
        self.start()

    def fileno(self):
        return self.fd_write

    def run(self):
        # 使用 iter() 包装 self.pipe_reader.readline 方法和空字符串""作为哨兵，使其不断读取管道内容。
        # iter(self.pipe_reader.readline, "") 会不断调用 readline()，直到返回空字符串（代表 EOF），循环终止。
        try:
            for line in iter(self.pipe_reader.readline, ""):
                self.deque.append(line)
        except (OSError, ValueError):
            # 管道已关闭，忽略错误
            pass
        finally:
            try:
                if not self._closed:
                    self.pipe_reader.close()
            except (OSError, ValueError):
                pass

    def dump(self):
        while len(self.deque) > 0:
            self.logger.error(self.deque.popleft())

    def close(self):
        # 先关闭写端，让读端线程收到 EOF 并退出
        if self._closed:
            return
        self._closed = True
        try:
            os.close(self.fd_write)
        except OSError:
            pass
        # 等待读线程结束
        self.join(timeout=1)


class FrameCapture:
    def __init__(
        self,
        rtsp_url: str,
        output_queue: queue.Queue,
        detect_rate_mode: str = "fps",
        detect_rate_value: int = 5,
        retry_limit: int = DEFAULT_STREAM_RETRY_LIMIT,
    ):
        self.rtsp_url = rtsp_url
        self.output_queue = output_queue
        self.detect_rate_mode = str(detect_rate_mode or "fps")
        self.detect_rate_value = int(detect_rate_value or 5)
        self.sampling_expr = build_sampling_expr(
            self.detect_rate_mode, self.detect_rate_value
        )
        try:
            normalized_retry_limit = int(retry_limit or DEFAULT_STREAM_RETRY_LIMIT)
        except (TypeError, ValueError):
            normalized_retry_limit = DEFAULT_STREAM_RETRY_LIMIT
        if normalized_retry_limit <= 0:
            normalized_retry_limit = DEFAULT_STREAM_RETRY_LIMIT
        self.retry_limit = normalized_retry_limit
        self._stop_event = threading.Event()
        self._thread: Optional[threading.Thread] = None
        self._proccess: Optional[subprocess.Popen] = None

        # 流信息
        self.width = 0
        self.height = 0
        self.fps = 0.0

        # 错误状态，供外部查询
        self.error_count = 0
        self.last_error = ""
        self.is_failed = False
        # 防止 stdout.read(frame_size) 长时间阻塞
        self.read_timeout_sec = 15.0
        # 进度日志间隔
        self.progress_log_interval_sec = 10.0

    def start(self):
        if self._thread is not None and self._thread.is_alive():
            return
        self._stop_event.clear()
        self._thread = threading.Thread(target=self._capture_loop, daemon=True)
        self._thread.start()
        slog.info(f"FrameCapture started for {self.rtsp_url}")

    def stop(self):
        # 设置停止事件
        self._stop_event.set()
        # 终止进程
        self._terminate_process()
        # 等待线程结束
        if self._thread:
            self._thread.join(timeout=2)
        slog.info(f"FrameCapture stopped for {self.rtsp_url}")

    def _get_stream_info(self) -> bool:
        slog.debug(f"正在探测流信息... {self.rtsp_url}")
        ffprobe_cmd = [
            "ffprobe",
            "-v",
            "error",
            "-select_streams",
            "v:0",
            "-show_entries",
            "stream=width,height,r_frame_rate",
            "-of",
            "csv=p=0",
            "-rtsp_transport",
            "tcp",  # 强制 TCP 更稳定
            self.rtsp_url,
        ]
        try:
            # 执行一个外部命令（比如系统命令、shell 脚本、其他可执行程序），并直接返回该命令在标准输出（stdout）中打印的内容。
            output = (
                subprocess.check_output(ffprobe_cmd, timeout=15).decode("utf-8").strip()
            )
            parts = output.split(",")
            if len(parts) >= 2:
                self.width = int(parts[0])
                self.height = int(parts[1])
                if len(parts) >= 3 and "/" in parts[2]:
                    num, den = parts[2].split("/")
                    self.fps = float(num) / float(den)
                else:
                    self.fps = 25.0
                slog.info(
                    f"ffprobe 探测成功: {self.width}x{self.height} @ {self.fps:.2f}fps"
                )
                return True
            self.last_error = f"probe stream info failed: unexpected output={output}"
        except Exception as e:
            self.last_error = f"probe stream info failed: {e}"
            slog.error(f"探测流信息失败: {e}")

        return False

    def _capture_loop(self):
        log_pipe: Optional[LogPipe] = None
        while not self._stop_event.is_set():
            if self.width == 0 or self.height == 0:
                if not self._get_stream_info():
                    self.error_count += 1
                    slog.warning(
                        "stream probe retry %d/%d for %s after 3s: %s",
                        self.error_count,
                        self.retry_limit,
                        self.rtsp_url,
                        self.last_error or "probe stream info failed",
                    )
                    if self.error_count >= self.retry_limit:
                        self.is_failed = True
                        slog.error(
                            "stream probe exhausted retries %d/%d for %s: %s",
                            self.error_count,
                            self.retry_limit,
                            self.rtsp_url,
                            self.last_error or "probe stream info failed",
                        )
                        return
                    time.sleep(3)
                    continue
            # 成功获取流信息后重置错误计数
            self.error_count = 0
            if log_pipe:
                log_pipe.close()
            log_pipe = LogPipe(f"ffmpeg.{self.rtsp_url}")
            ffmpeg_cmd = [
                "ffmpeg",
                "-hide_banner",
                "-loglevel",
                "warning",  # 只输出 warning 以上，减少 IO
                "-rtsp_transport",
                "tcp",
                "-i",
                self.rtsp_url,
                "-vf",
                self.sampling_expr,
                "-f",
                "rawvideo",
                "-pix_fmt",
                "bgr24",  # ?? OpenCV ?????? BGR
                "pipe:1",
            ]
            slog.info(f"启动 ffmpeg 进程: {' '.join(ffmpeg_cmd[:-2])} ...")

            try:
                self._proccess = subprocess.Popen(
                    ffmpeg_cmd,
                    stdout=subprocess.PIPE,
                    stderr=log_pipe.fileno(),
                    bufsize=10**7,
                )
            except Exception as e:
                self.last_error = f"start ffmpeg failed: {e}"
                self.error_count += 1
                if log_pipe:
                    log_pipe.dump()
                    log_pipe.close()
                    log_pipe = None
                slog.warning(
                    "ffmpeg start retry %d/%d for %s after 3s: %s",
                    self.error_count,
                    self.retry_limit,
                    self.rtsp_url,
                    self.last_error,
                )
                if self.error_count >= self.retry_limit:
                    self.is_failed = True
                    slog.error(
                        "ffmpeg start exhausted retries %d/%d for %s: %s",
                        self.error_count,
                        self.retry_limit,
                        self.rtsp_url,
                        self.last_error,
                    )
                    return
                time.sleep(3)
                continue
            frame_size = self.width * self.height * 3
            slog.info(f"开始读取帧 (size={frame_size})...")
            frames_received = 0
            last_progress_log = time.time()

            while not self._stop_event.is_set():
                try:
                    if self._proccess.poll() is not None:
                        self.last_error = "ffmpeg process exited unexpectedly"
                        slog.error("FFmpeg 进程意外退出")
                        log_pipe.dump()
                        break
                    if self._proccess.stdout is None:
                        self.last_error = "ffmpeg stdout is nil"
                        slog.error("FFmpeg 进程 stdout 为空")
                        break
                    # Linux/macOS 下先等待可读，避免 read(frame_size) 卡死
                    if os.name != "nt":
                        ready, _, _ = select.select(
                            [self._proccess.stdout], [], [], self.read_timeout_sec
                        )
                        if not ready:
                            self.last_error = (
                                f"ffmpeg read timeout ({self.read_timeout_sec}s)"
                            )
                            slog.warning(self.last_error)
                            log_pipe.dump()
                            break

                    raw_bytes = self._proccess.stdout.read(frame_size)
                    if not raw_bytes:
                        self.last_error = "ffmpeg stdout EOF"
                        slog.warning(self.last_error)
                        log_pipe.dump()
                        break

                    if len(raw_bytes) != frame_size:
                        self.last_error = "incomplete frame read"
                        slog.warning("读取到不完整的帧 (流中断?)")
                        log_pipe.dump()  # 可能有网络错误
                        break
                    image = np.frombuffer(raw_bytes, dtype=np.uint8).reshape(
                        self.height, self.width, 3
                    )

                    try:
                        while not self.output_queue.empty():
                            try:
                                self.output_queue.get_nowait()
                            except queue.Empty:
                                break
                        self.output_queue.put_nowait(image)
                    except Exception as e:
                        pass

                    frames_received += 1
                    now = time.time()
                    if now - last_progress_log >= self.progress_log_interval_sec:
                        slog.info(
                            f"FrameCapture receiving frames: {frames_received}, "
                            f"queue={self.output_queue.qsize()}"
                        )
                        last_progress_log = now

                except Exception as e:
                    slog.error(f"读取帧失败: {e}")
                    self.last_error = f"read frame failed: {e}"
                    log_pipe.dump()
                    break
            self._terminate_process()

            if log_pipe:
                log_pipe.close()

            if self._stop_event.is_set():
                break

            # ffmpeg 进程异常退出也计入错误计数
            self.error_count += 1
            slog.warning(
                "stream read retry %d/%d for %s after 2s: %s",
                self.error_count,
                self.retry_limit,
                self.rtsp_url,
                self.last_error or "frame capture failed",
            )
            if self.error_count >= self.retry_limit:
                self.is_failed = True
                slog.error(
                    "stream read exhausted retries %d/%d for %s: %s",
                    self.error_count,
                    self.retry_limit,
                    self.rtsp_url,
                    self.last_error or "frame capture failed",
                )
                return
            time.sleep(2)

    def _terminate_process(self):
        if self._proccess:
            if self._proccess.poll() is None:
                self._proccess.terminate()
                try:
                    self._proccess.wait(timeout=2)
                except subprocess.TimeoutExpired:
                    self._proccess.kill()
            self._proccess = None

    def _hide_password(self, url):
        """隐藏 URL 中的密码"""
        try:
            if "@" in url:
                parts = url.split("@")
                if "//" in parts[0]:
                    protocol_auth = parts[0].split("//")
                    if ":" in protocol_auth[1]:
                        user = protocol_auth[1].split(":")[0]
                        return f"{protocol_auth[0]}//{user}:***@{parts[1]}"
            return url
        except:
            return url

    def get_stream_info(self):
        """返回流的基本信息"""
        return self.width, self.height, self.fps
