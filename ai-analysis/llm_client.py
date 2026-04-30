"""
大模型客户端：调用 DashScope / OpenAI 兼容接口（多模态）。
"""

from dataclasses import dataclass
import logging
import os
import socket
import ssl
import time
from typing import Any
from urllib.parse import urlparse, urlunparse
from uuid import uuid4

slog = logging.getLogger("LLM")

_DASHSCOPE_HOSTS = {
    "dashscope.aliyuncs.com",
    "dashscope-intl.aliyuncs.com",
    "dashscope-us.aliyuncs.com",
}


@dataclass
class LLMCallResult:
    call_id: str
    content: str
    success: bool
    call_status: str
    error_message: str
    model: str
    latency_ms: float
    prompt_tokens: int | None
    completion_tokens: int | None
    total_tokens: int | None
    usage_available: bool
    request_context: str

    def to_payload(self) -> dict[str, Any]:
        return {
            "call_id": self.call_id,
            "call_status": self.call_status,
            "usage_available": self.usage_available,
            "prompt_tokens": self.prompt_tokens,
            "completion_tokens": self.completion_tokens,
            "total_tokens": self.total_tokens,
            "latency_ms": self.latency_ms,
            "model": self.model,
            "error_message": self.error_message,
            "request_context": self.request_context,
        }


def _extract_message_content(content: Any) -> str:
    if isinstance(content, str):
        return content.strip()

    if isinstance(content, list):
        text_parts: list[str] = []
        for block in content:
            if isinstance(block, dict):
                text = block.get("text")
            else:
                text = getattr(block, "text", None)
            if isinstance(text, str) and text:
                text_parts.append(text)
        return "\n".join(text_parts).strip()

    if content is None:
        return ""

    return str(content).strip()


def _extract_obj_value(data: Any, field: str, default: Any = None) -> Any:
    if isinstance(data, dict):
        return data.get(field, default)
    return getattr(data, field, default)


def _is_dashscope_provider(api_url: str) -> bool:
    normalized = str(api_url or "").strip()
    if not normalized:
        return False
    parsed = urlparse(normalized)
    hostname = (parsed.hostname or "").strip().lower()
    return hostname in _DASHSCOPE_HOSTS


def _normalize_dashscope_api_base(api_url: str) -> str:
    normalized = str(api_url or "").strip()
    if not normalized:
        return ""
    parsed = urlparse(normalized)
    scheme = parsed.scheme or "https"
    netloc = parsed.netloc
    if not netloc and parsed.path:
        netloc = parsed.path
    if not netloc:
        return ""
    return urlunparse((scheme, netloc, "/api/v1", "", "", ""))


def _extract_dashscope_text(response: Any) -> str:
    output = _extract_obj_value(response, "output")
    choices = _extract_obj_value(output, "choices", []) or []
    if not choices:
        return ""
    message = _extract_obj_value(choices[0], "message")
    content = _extract_obj_value(message, "content", "")
    return _extract_message_content(content)


def _extract_usage_value(usage: Any, field: str) -> int | None:
    if usage is None:
        return None
    value = getattr(usage, field, None)
    if value is None and isinstance(usage, dict):
        value = usage.get(field)
    if value is None:
        return None
    try:
        normalized = int(value)
    except (TypeError, ValueError):
        return None
    if normalized < 0:
        return None
    return normalized


def _build_result(
    *,
    call_id: str,
    content: str,
    success: bool,
    call_status: str,
    error_message: str,
    model: str,
    start_at: float,
    prompt_tokens: int | None = None,
    completion_tokens: int | None = None,
    total_tokens: int | None = None,
    request_context: str,
) -> LLMCallResult:
    usage_available = (
        prompt_tokens is not None
        or completion_tokens is not None
        or total_tokens is not None
    )
    return LLMCallResult(
        call_id=call_id,
        content=content,
        success=success,
        call_status=call_status,
        error_message=error_message,
        model=model,
        latency_ms=(time.perf_counter() - start_at) * 1000,
        prompt_tokens=prompt_tokens,
        completion_tokens=completion_tokens,
        total_tokens=total_tokens,
        usage_available=usage_available,
        request_context=request_context,
    )


def _extract_provider_host(api_url: str) -> str:
    normalized = str(api_url or "").strip()
    if not normalized:
        return "-"
    parsed = urlparse(normalized)
    return (parsed.hostname or "").strip().lower() or "-"


def _extract_exception_status_code(exc: Exception | None) -> int | None:
    if exc is None:
        return None
    candidates = [
        getattr(exc, "status_code", None),
        getattr(exc, "status", None),
    ]
    response = getattr(exc, "response", None)
    if response is not None:
        candidates.extend(
            [
                getattr(response, "status_code", None),
                _extract_obj_value(response, "status_code"),
                _extract_obj_value(response, "status"),
            ]
        )
    for value in candidates:
        if value is None:
            continue
        try:
            return int(value)
        except (TypeError, ValueError):
            continue
    return None


def _classify_llm_failure(
    exc: Exception | None = None,
    *,
    call_status: str = "",
    status_code: int | None = None,
    error_message: str = "",
) -> str:
    if status_code is not None and status_code >= 400:
        return "provider_status"
    if str(call_status or "").strip().lower() == "empty_content":
        return "empty_content"

    exc_name = type(exc).__name__.lower() if exc is not None else ""
    detail = str(error_message or exc or "").strip().lower()
    tls_keywords = [
        "ssl",
        "tls",
        "certificate",
        "handshake",
        "unexpected eof while reading",
        "wrong version number",
    ]
    timeout_keywords = [
        "timeout",
        "timed out",
        "read timed out",
        "connect timeout",
    ]
    connect_keywords = [
        "connection error",
        "connection aborted",
        "connection reset",
        "connection refused",
        "connectex",
        "temporary failure in name resolution",
        "name or service not known",
        "nodename nor servname",
        "max retries exceeded",
        "network is unreachable",
    ]

    if isinstance(exc, (ssl.SSLError, ssl.SSLEOFError)) or any(keyword in exc_name or keyword in detail for keyword in tls_keywords):
        return "tls"
    if isinstance(exc, (TimeoutError, socket.timeout)) or any(keyword in exc_name or keyword in detail for keyword in timeout_keywords):
        return "timeout"
    if isinstance(exc, (ConnectionError, socket.gaierror, OSError)) or any(
        keyword in exc_name or keyword in detail for keyword in connect_keywords
    ):
        return "connect"
    return "unknown"


def _log_llm_failure(
    *,
    level: str,
    summary: str,
    call_id: str,
    context: str,
    base_url: str,
    model: str,
    failure_type: str,
    error_message: str,
    exc: Exception | None = None,
    status_code: int | None = None,
) -> None:
    log_fn = getattr(slog, level, slog.error)
    log_fn(
        "%s: call_id=%s context=%s failure_type=%s exception_type=%s provider_host=%s base_url=%s model=%s status_code=%s error=%s",
        summary,
        call_id,
        context,
        failure_type,
        type(exc).__name__ if exc is not None else "-",
        _extract_provider_host(base_url),
        (base_url or "").strip() or "-",
        model,
        status_code if status_code is not None else "-",
        (error_message or "").strip() or "-",
    )


def call_llm(
    api_url: str,
    api_key: str,
    model: str,
    prompt: str,
    image_b64: str,
    timeout: float = 30.0,
    log_context: str = "",
) -> LLMCallResult:
    return call_llm_with_images(
        api_url=api_url,
        api_key=api_key,
        model=model,
        prompt=prompt,
        images=[{"image_b64": image_b64, "mime_type": "image/jpeg"}],
        timeout=timeout,
        log_context=log_context,
    )


def call_llm_with_images(
    api_url: str,
    api_key: str,
    model: str,
    prompt: str,
    images: list[dict[str, str]],
    timeout: float = 30.0,
    log_context: str = "",
) -> LLMCallResult:
    call_id = uuid4().hex
    base_url = (api_url or "").strip()
    effective_model = (model or os.getenv("DASHSCOPE_MODEL") or "qwen3-vl-plus").strip()
    context = (log_context or "").strip() or "-"
    start_at = time.perf_counter()

    if not base_url:
        slog.error("LLM API url is empty")
        return _build_result(
            call_id=call_id,
            content="",
            success=False,
            call_status="error",
            error_message="LLM API url is empty",
            model=effective_model,
            start_at=start_at,
            request_context=context,
        )

    env_api_key = os.getenv("DASHSCOPE_API_KEY", "").strip()
    effective_api_key = env_api_key or (api_key or "").strip()
    if not effective_api_key:
        slog.error("LLM API key missing (set DASHSCOPE_API_KEY or llm_api_key)")
        return _build_result(
            call_id=call_id,
            content="",
            success=False,
            call_status="error",
            error_message="LLM API key missing",
            model=effective_model,
            start_at=start_at,
            request_context=context,
        )

    if not effective_model:
        slog.error("LLM model is empty")
        return _build_result(
            call_id=call_id,
            content="",
            success=False,
            call_status="error",
            error_message="LLM model is empty",
            model=effective_model,
            start_at=start_at,
            request_context=context,
        )

    normalized_images: list[dict[str, Any]] = []
    for item in images or []:
        if not isinstance(item, dict):
            continue
        image_b64 = str(item.get("image_b64", "")).strip()
        if not image_b64:
            continue
        mime_type = str(item.get("mime_type", "")).strip() or "image/jpeg"
        normalized_images.append({
            "type": "image_url",
            "image_url": {
                "url": f"data:{mime_type};base64,{image_b64}",
            },
        })

    if not normalized_images:
        return _build_result(
            call_id=call_id,
            content="",
            success=False,
            call_status="error",
            error_message="LLM images are empty",
            model=effective_model,
            start_at=start_at,
            request_context=context,
        )

    effective_prompt = (prompt or "图中描述的是什么场景？").strip()
    slog.info(
        "LLM prompt: call_id=%s context=%s chars=%d images=%d\n%s",
        call_id,
        context,
        len(effective_prompt),
        len(normalized_images),
        effective_prompt,
    )

    try:
        try:
            from openai import OpenAI
        except Exception as exc:
            slog.error("openai SDK not available: %s", exc)
            return _build_result(
                call_id=call_id,
                content="",
                success=False,
                call_status="error",
                error_message=f"openai SDK not available: {exc}",
                model=effective_model,
                start_at=start_at,
                request_context=context,
            )

        client = OpenAI(api_key=effective_api_key, base_url=base_url)
        completion = client.chat.completions.create(
            model=effective_model,
            messages=[
                {
                    "role": "user",
                    "content": [*normalized_images, {"type": "text", "text": effective_prompt}],
                }
            ],
            timeout=timeout,
        )

        usage = getattr(completion, "usage", None)
        prompt_tokens = _extract_usage_value(usage, "prompt_tokens")
        completion_tokens = _extract_usage_value(usage, "completion_tokens")
        total_tokens = _extract_usage_value(usage, "total_tokens")

        choices = getattr(completion, "choices", []) or []
        if not choices:
            error_message = "LLM API returned empty choices/content"
            _log_llm_failure(
                level="warning",
                summary="LLM API returned empty content",
                call_id=call_id,
                context=context,
                base_url=base_url,
                model=effective_model,
                failure_type=_classify_llm_failure(
                    call_status="empty_content", error_message=error_message
                ),
                error_message=error_message,
            )
            result = _build_result(
                call_id=call_id,
                content="",
                success=False,
                call_status="empty_content",
                error_message=error_message,
                model=effective_model,
                start_at=start_at,
                prompt_tokens=prompt_tokens,
                completion_tokens=completion_tokens,
                total_tokens=total_tokens,
                request_context=context,
            )
            slog.info(
                "LLM cost: call_id=%s model=%s status=%s latency_ms=%.1f total_tokens=%s",
                call_id,
                effective_model,
                result.call_status,
                result.latency_ms,
                result.total_tokens,
            )
            return result

        message = getattr(choices[0], "message", None)
        raw_content = getattr(message, "content", "") if message else ""
        content = _extract_message_content(raw_content)
        if not content:
            error_message = "LLM API returned empty message.content"
            _log_llm_failure(
                level="warning",
                summary="LLM API returned empty content",
                call_id=call_id,
                context=context,
                base_url=base_url,
                model=effective_model,
                failure_type=_classify_llm_failure(
                    call_status="empty_content", error_message=error_message
                ),
                error_message=error_message,
            )
            result = _build_result(
                call_id=call_id,
                content="",
                success=False,
                call_status="empty_content",
                error_message=error_message,
                model=effective_model,
                start_at=start_at,
                prompt_tokens=prompt_tokens,
                completion_tokens=completion_tokens,
                total_tokens=total_tokens,
                request_context=context,
            )
            slog.info(
                "LLM cost: call_id=%s model=%s status=%s latency_ms=%.1f total_tokens=%s",
                call_id,
                effective_model,
                result.call_status,
                result.latency_ms,
                result.total_tokens,
            )
            return result

        slog.info(
            "LLM result: call_id=%s context=%s chars=%d\n%s",
            call_id,
            context,
            len(content),
            content,
        )
        result = _build_result(
            call_id=call_id,
            content=content,
            success=True,
            call_status="success",
            error_message="",
            model=effective_model,
            start_at=start_at,
            prompt_tokens=prompt_tokens,
            completion_tokens=completion_tokens,
            total_tokens=total_tokens,
            request_context=context,
        )
        slog.info(
            "LLM cost: call_id=%s model=%s status=%s latency_ms=%.1f prompt_tokens=%s completion_tokens=%s total_tokens=%s usage_available=%s",
            call_id,
            effective_model,
            result.call_status,
            result.latency_ms,
            result.prompt_tokens,
            result.completion_tokens,
            result.total_tokens,
            result.usage_available,
        )
        return result
    except Exception as exc:
        _log_llm_failure(
            level="error",
            summary="LLM API call failed",
            call_id=call_id,
            context=context,
            base_url=base_url,
            model=effective_model,
            failure_type=_classify_llm_failure(
                exc, status_code=_extract_exception_status_code(exc), error_message=str(exc)
            ),
            error_message=str(exc),
            exc=exc,
            status_code=_extract_exception_status_code(exc),
        )
        result = _build_result(
            call_id=call_id,
            content="",
            success=False,
            call_status="error",
            error_message=str(exc),
            model=effective_model,
            start_at=start_at,
            request_context=context,
        )
        slog.info(
            "LLM cost: call_id=%s model=%s status=%s latency_ms=%.1f total_tokens=%s",
            call_id,
            effective_model,
            result.call_status,
            result.latency_ms,
            result.total_tokens,
        )
        return result


def call_llm_with_video(
    api_url: str,
    api_key: str,
    model: str,
    prompt: str,
    video_path: str,
    mime_type: str,
    fps: int = 1,
    timeout: float = 60.0,
    log_context: str = "",
) -> LLMCallResult:
    call_id = uuid4().hex
    base_url = (api_url or "").strip()
    effective_model = (model or os.getenv("DASHSCOPE_MODEL") or "qwen3-vl-plus").strip()
    context = (log_context or "").strip() or "-"
    start_at = time.perf_counter()

    if not base_url:
        slog.error("LLM API url is empty")
        return _build_result(
            call_id=call_id,
            content="",
            success=False,
            call_status="error",
            error_message="LLM API url is empty",
            model=effective_model,
            start_at=start_at,
            request_context=context,
        )

    env_api_key = os.getenv("DASHSCOPE_API_KEY", "").strip()
    effective_api_key = env_api_key or (api_key or "").strip()
    if not effective_api_key:
        slog.error("LLM API key missing (set DASHSCOPE_API_KEY or llm_api_key)")
        return _build_result(
            call_id=call_id,
            content="",
            success=False,
            call_status="error",
            error_message="LLM API key missing",
            model=effective_model,
            start_at=start_at,
            request_context=context,
        )

    normalized_video_path = str(video_path or "").strip()
    if not normalized_video_path:
        return _build_result(
            call_id=call_id,
            content="",
            success=False,
            call_status="error",
            error_message="LLM video path is empty",
            model=effective_model,
            start_at=start_at,
            request_context=context,
        )
    if not os.path.isfile(normalized_video_path):
        return _build_result(
            call_id=call_id,
            content="",
            success=False,
            call_status="error",
            error_message=f"LLM video file not found: {normalized_video_path}",
            model=effective_model,
            start_at=start_at,
            request_context=context,
        )

    effective_mime_type = str(mime_type or "").strip() or "video/mp4"
    effective_fps = 1
    try:
        effective_fps = int(fps)
    except (TypeError, ValueError):
        effective_fps = 1
    if effective_fps <= 0:
        effective_fps = 1

    effective_prompt = (prompt or "请分析这段视频").strip()
    slog.info(
        "LLM prompt: call_id=%s context=%s chars=%d video_mime=%s fps=%d path=%s\n%s",
        call_id,
        context,
        len(effective_prompt),
        effective_mime_type,
        effective_fps,
        normalized_video_path,
        effective_prompt,
    )

    if not _is_dashscope_provider(base_url):
        slog.error(
            "LLM video provider not supported: call_id=%s context=%s base_url=%s",
            call_id,
            context,
            base_url,
        )
        return _build_result(
            call_id=call_id,
            content="",
            success=False,
            call_status="error",
            error_message="当前视频测试仅支持 DashScope 本地文件视频输入",
            model=effective_model,
            start_at=start_at,
            request_context=context,
        )

    try:
        try:
            import dashscope
        except Exception as exc:
            slog.error("dashscope SDK not available: %s", exc)
            return _build_result(
                call_id=call_id,
                content="",
                success=False,
                call_status="error",
                error_message=f"dashscope SDK not available: {exc}",
                model=effective_model,
                start_at=start_at,
                request_context=context,
            )

        dashscope.base_http_api_url = _normalize_dashscope_api_base(base_url)
        slog.info(
            "LLM video call mode: call_id=%s context=%s provider=dashscope_local_video base_http_api_url=%s timeout=%.1fs",
            call_id,
            context,
            dashscope.base_http_api_url,
            timeout,
        )
        completion = dashscope.MultiModalConversation.call(
            api_key=effective_api_key,
            model=effective_model,
            messages=[
                {
                    "role": "user",
                    "content": [
                        {"video": normalized_video_path, "fps": effective_fps},
                        {"text": effective_prompt},
                    ],
                }
            ],
        )

        usage = _extract_obj_value(completion, "usage")
        prompt_tokens = _extract_usage_value(usage, "input_tokens")
        completion_tokens = _extract_usage_value(usage, "output_tokens")
        total_tokens = _extract_usage_value(usage, "total_tokens")
        status_code = _extract_usage_value(completion, "status_code")
        if status_code is not None and status_code >= 400:
            error_message = str(
                _extract_obj_value(completion, "message", "DashScope video call failed")
            )
            _log_llm_failure(
                level="error",
                summary="LLM provider returned error status",
                call_id=call_id,
                context=context,
                base_url=base_url,
                model=effective_model,
                failure_type=_classify_llm_failure(
                    status_code=status_code, error_message=error_message
                ),
                error_message=error_message,
                status_code=status_code,
            )
            return _build_result(
                call_id=call_id,
                content="",
                success=False,
                call_status="error",
                error_message=error_message,
                model=effective_model,
                start_at=start_at,
                prompt_tokens=prompt_tokens,
                completion_tokens=completion_tokens,
                total_tokens=total_tokens,
                request_context=context,
            )

        content = _extract_dashscope_text(completion)
        if not content:
            error_message = "LLM API returned empty choices/content"
            _log_llm_failure(
                level="warning",
                summary="LLM API returned empty content",
                call_id=call_id,
                context=context,
                base_url=base_url,
                model=effective_model,
                failure_type=_classify_llm_failure(
                    call_status="empty_content", error_message=error_message
                ),
                error_message=error_message,
            )
            return _build_result(
                call_id=call_id,
                content="",
                success=False,
                call_status="empty_content",
                error_message=error_message,
                model=effective_model,
                start_at=start_at,
                prompt_tokens=prompt_tokens,
                completion_tokens=completion_tokens,
                total_tokens=total_tokens,
                request_context=context,
            )

        slog.info(
            "LLM result: call_id=%s context=%s chars=%d\n%s",
            call_id,
            context,
            len(content),
            content,
        )
        result = _build_result(
            call_id=call_id,
            content=content,
            success=True,
            call_status="success",
            error_message="",
            model=effective_model,
            start_at=start_at,
            prompt_tokens=prompt_tokens,
            completion_tokens=completion_tokens,
            total_tokens=total_tokens,
            request_context=context,
        )
        slog.info(
            "LLM cost: call_id=%s model=%s status=%s latency_ms=%.1f prompt_tokens=%s completion_tokens=%s total_tokens=%s usage_available=%s",
            call_id,
            effective_model,
            result.call_status,
            result.latency_ms,
            result.prompt_tokens,
            result.completion_tokens,
            result.total_tokens,
            result.usage_available,
        )
        return result
    except Exception as exc:
        _log_llm_failure(
            level="error",
            summary="LLM API call failed",
            call_id=call_id,
            context=context,
            base_url=base_url,
            model=effective_model,
            failure_type=_classify_llm_failure(
                exc, status_code=_extract_exception_status_code(exc), error_message=str(exc)
            ),
            error_message=str(exc),
            exc=exc,
            status_code=_extract_exception_status_code(exc),
        )
        result = _build_result(
            call_id=call_id,
            content="",
            success=False,
            call_status="error",
            error_message=str(exc),
            model=effective_model,
            start_at=start_at,
            request_context=context,
        )
        slog.info(
            "LLM cost: call_id=%s model=%s status=%s latency_ms=%.1f total_tokens=%s",
            call_id,
            effective_model,
            result.call_status,
            result.latency_ms,
            result.total_tokens,
        )
        return result
