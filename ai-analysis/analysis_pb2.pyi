from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class StartCameraRequest(_message.Message):
    __slots__ = ("camera_id", "camera_name", "rtsp_url", "detect_rate_mode", "algorithm_configs", "retry_limit", "callback_url", "callback_secret", "llm_api_url", "llm_api_key", "llm_model", "llm_prompt", "detect_rate_value")
    CAMERA_ID_FIELD_NUMBER: _ClassVar[int]
    CAMERA_NAME_FIELD_NUMBER: _ClassVar[int]
    RTSP_URL_FIELD_NUMBER: _ClassVar[int]
    DETECT_RATE_MODE_FIELD_NUMBER: _ClassVar[int]
    ALGORITHM_CONFIGS_FIELD_NUMBER: _ClassVar[int]
    RETRY_LIMIT_FIELD_NUMBER: _ClassVar[int]
    CALLBACK_URL_FIELD_NUMBER: _ClassVar[int]
    CALLBACK_SECRET_FIELD_NUMBER: _ClassVar[int]
    LLM_API_URL_FIELD_NUMBER: _ClassVar[int]
    LLM_API_KEY_FIELD_NUMBER: _ClassVar[int]
    LLM_MODEL_FIELD_NUMBER: _ClassVar[int]
    LLM_PROMPT_FIELD_NUMBER: _ClassVar[int]
    DETECT_RATE_VALUE_FIELD_NUMBER: _ClassVar[int]
    camera_id: str
    camera_name: str
    rtsp_url: str
    detect_rate_mode: str
    algorithm_configs: _containers.RepeatedCompositeFieldContainer[AlgorithmConfig]
    retry_limit: int
    callback_url: str
    callback_secret: str
    llm_api_url: str
    llm_api_key: str
    llm_model: str
    llm_prompt: str
    detect_rate_value: int
    def __init__(self, camera_id: _Optional[str] = ..., camera_name: _Optional[str] = ..., rtsp_url: _Optional[str] = ..., detect_rate_mode: _Optional[str] = ..., algorithm_configs: _Optional[_Iterable[_Union[AlgorithmConfig, _Mapping]]] = ..., retry_limit: _Optional[int] = ..., callback_url: _Optional[str] = ..., callback_secret: _Optional[str] = ..., llm_api_url: _Optional[str] = ..., llm_api_key: _Optional[str] = ..., llm_model: _Optional[str] = ..., llm_prompt: _Optional[str] = ..., detect_rate_value: _Optional[int] = ...) -> None: ...

class AlgorithmConfig(_message.Message):
    __slots__ = ("algorithm_id", "task_code", "labels", "yolo_threshold", "iou_threshold", "labels_trigger_mode", "detect_mode")
    ALGORITHM_ID_FIELD_NUMBER: _ClassVar[int]
    TASK_CODE_FIELD_NUMBER: _ClassVar[int]
    LABELS_FIELD_NUMBER: _ClassVar[int]
    YOLO_THRESHOLD_FIELD_NUMBER: _ClassVar[int]
    IOU_THRESHOLD_FIELD_NUMBER: _ClassVar[int]
    LABELS_TRIGGER_MODE_FIELD_NUMBER: _ClassVar[int]
    DETECT_MODE_FIELD_NUMBER: _ClassVar[int]
    algorithm_id: str
    task_code: str
    labels: _containers.RepeatedScalarFieldContainer[str]
    yolo_threshold: float
    iou_threshold: float
    labels_trigger_mode: str
    detect_mode: int
    def __init__(self, algorithm_id: _Optional[str] = ..., task_code: _Optional[str] = ..., labels: _Optional[_Iterable[str]] = ..., yolo_threshold: _Optional[float] = ..., iou_threshold: _Optional[float] = ..., labels_trigger_mode: _Optional[str] = ..., detect_mode: _Optional[int] = ...) -> None: ...

class StartCameraResponse(_message.Message):
    __slots__ = ("success", "message", "source_width", "source_height", "source_fps")
    SUCCESS_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    SOURCE_WIDTH_FIELD_NUMBER: _ClassVar[int]
    SOURCE_HEIGHT_FIELD_NUMBER: _ClassVar[int]
    SOURCE_FPS_FIELD_NUMBER: _ClassVar[int]
    success: bool
    message: str
    source_width: int
    source_height: int
    source_fps: float
    def __init__(self, success: bool = ..., message: _Optional[str] = ..., source_width: _Optional[int] = ..., source_height: _Optional[int] = ..., source_fps: _Optional[float] = ...) -> None: ...

class StopCameraRequest(_message.Message):
    __slots__ = ("camera_id",)
    CAMERA_ID_FIELD_NUMBER: _ClassVar[int]
    camera_id: str
    def __init__(self, camera_id: _Optional[str] = ...) -> None: ...

class StopCameraResponse(_message.Message):
    __slots__ = ("success", "message")
    SUCCESS_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    success: bool
    message: str
    def __init__(self, success: bool = ..., message: _Optional[str] = ...) -> None: ...

class StatusRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class CameraStatus(_message.Message):
    __slots__ = ("camera_id", "status", "frames_processed", "last_error", "retry_count")
    CAMERA_ID_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    FRAMES_PROCESSED_FIELD_NUMBER: _ClassVar[int]
    LAST_ERROR_FIELD_NUMBER: _ClassVar[int]
    RETRY_COUNT_FIELD_NUMBER: _ClassVar[int]
    camera_id: str
    status: str
    frames_processed: int
    last_error: str
    retry_count: int
    def __init__(self, camera_id: _Optional[str] = ..., status: _Optional[str] = ..., frames_processed: _Optional[int] = ..., last_error: _Optional[str] = ..., retry_count: _Optional[int] = ...) -> None: ...

class GlobalStats(_message.Message):
    __slots__ = ("active_streams", "total_detections", "uptime_seconds")
    ACTIVE_STREAMS_FIELD_NUMBER: _ClassVar[int]
    TOTAL_DETECTIONS_FIELD_NUMBER: _ClassVar[int]
    UPTIME_SECONDS_FIELD_NUMBER: _ClassVar[int]
    active_streams: int
    total_detections: int
    uptime_seconds: int
    def __init__(self, active_streams: _Optional[int] = ..., total_detections: _Optional[int] = ..., uptime_seconds: _Optional[int] = ...) -> None: ...

class StatusResponse(_message.Message):
    __slots__ = ("is_ready", "cameras", "stats")
    IS_READY_FIELD_NUMBER: _ClassVar[int]
    CAMERAS_FIELD_NUMBER: _ClassVar[int]
    STATS_FIELD_NUMBER: _ClassVar[int]
    is_ready: bool
    cameras: _containers.RepeatedCompositeFieldContainer[CameraStatus]
    stats: GlobalStats
    def __init__(self, is_ready: bool = ..., cameras: _Optional[_Iterable[_Union[CameraStatus, _Mapping]]] = ..., stats: _Optional[_Union[GlobalStats, _Mapping]] = ...) -> None: ...

class HealthCheckRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class HealthCheckResponse(_message.Message):
    __slots__ = ("status",)
    class ServingStatus(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
        __slots__ = ()
        UNKNOWN: _ClassVar[HealthCheckResponse.ServingStatus]
        SERVING: _ClassVar[HealthCheckResponse.ServingStatus]
        NOT_SERVING: _ClassVar[HealthCheckResponse.ServingStatus]
    UNKNOWN: HealthCheckResponse.ServingStatus
    SERVING: HealthCheckResponse.ServingStatus
    NOT_SERVING: HealthCheckResponse.ServingStatus
    STATUS_FIELD_NUMBER: _ClassVar[int]
    status: HealthCheckResponse.ServingStatus
    def __init__(self, status: _Optional[_Union[HealthCheckResponse.ServingStatus, str]] = ...) -> None: ...

class BoundingBox(_message.Message):
    __slots__ = ("x_min", "y_min", "x_max", "y_max")
    X_MIN_FIELD_NUMBER: _ClassVar[int]
    Y_MIN_FIELD_NUMBER: _ClassVar[int]
    X_MAX_FIELD_NUMBER: _ClassVar[int]
    Y_MAX_FIELD_NUMBER: _ClassVar[int]
    x_min: int
    y_min: int
    x_max: int
    y_max: int
    def __init__(self, x_min: _Optional[int] = ..., y_min: _Optional[int] = ..., x_max: _Optional[int] = ..., y_max: _Optional[int] = ...) -> None: ...

class Detection(_message.Message):
    __slots__ = ("label", "confidence", "box", "area")
    LABEL_FIELD_NUMBER: _ClassVar[int]
    CONFIDENCE_FIELD_NUMBER: _ClassVar[int]
    BOX_FIELD_NUMBER: _ClassVar[int]
    AREA_FIELD_NUMBER: _ClassVar[int]
    label: str
    confidence: float
    box: BoundingBox
    area: int
    def __init__(self, label: _Optional[str] = ..., confidence: _Optional[float] = ..., box: _Optional[_Union[BoundingBox, _Mapping]] = ..., area: _Optional[int] = ...) -> None: ...

class AnalyzeImageRequest(_message.Message):
    __slots__ = ("camera_id", "image_base64", "algorithm_configs", "llm_api_url", "llm_api_key", "llm_model", "llm_prompt")
    CAMERA_ID_FIELD_NUMBER: _ClassVar[int]
    IMAGE_BASE64_FIELD_NUMBER: _ClassVar[int]
    ALGORITHM_CONFIGS_FIELD_NUMBER: _ClassVar[int]
    LLM_API_URL_FIELD_NUMBER: _ClassVar[int]
    LLM_API_KEY_FIELD_NUMBER: _ClassVar[int]
    LLM_MODEL_FIELD_NUMBER: _ClassVar[int]
    LLM_PROMPT_FIELD_NUMBER: _ClassVar[int]
    camera_id: str
    image_base64: str
    algorithm_configs: _containers.RepeatedCompositeFieldContainer[AlgorithmConfig]
    llm_api_url: str
    llm_api_key: str
    llm_model: str
    llm_prompt: str
    def __init__(self, camera_id: _Optional[str] = ..., image_base64: _Optional[str] = ..., algorithm_configs: _Optional[_Iterable[_Union[AlgorithmConfig, _Mapping]]] = ..., llm_api_url: _Optional[str] = ..., llm_api_key: _Optional[str] = ..., llm_model: _Optional[str] = ..., llm_prompt: _Optional[str] = ...) -> None: ...

class AlgorithmResult(_message.Message):
    __slots__ = ("algorithm_id", "task_code", "alarm", "source", "reason", "boxes")
    ALGORITHM_ID_FIELD_NUMBER: _ClassVar[int]
    TASK_CODE_FIELD_NUMBER: _ClassVar[int]
    ALARM_FIELD_NUMBER: _ClassVar[int]
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    REASON_FIELD_NUMBER: _ClassVar[int]
    BOXES_FIELD_NUMBER: _ClassVar[int]
    algorithm_id: str
    task_code: str
    alarm: int
    source: str
    reason: str
    boxes: _containers.RepeatedCompositeFieldContainer[Detection]
    def __init__(self, algorithm_id: _Optional[str] = ..., task_code: _Optional[str] = ..., alarm: _Optional[int] = ..., source: _Optional[str] = ..., reason: _Optional[str] = ..., boxes: _Optional[_Iterable[_Union[Detection, _Mapping]]] = ...) -> None: ...

class AnalyzeImageResponse(_message.Message):
    __slots__ = ("success", "message", "camera_id", "detections", "llm_result", "snapshot", "snapshot_width", "snapshot_height", "algorithm_results")
    SUCCESS_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    CAMERA_ID_FIELD_NUMBER: _ClassVar[int]
    DETECTIONS_FIELD_NUMBER: _ClassVar[int]
    LLM_RESULT_FIELD_NUMBER: _ClassVar[int]
    SNAPSHOT_FIELD_NUMBER: _ClassVar[int]
    SNAPSHOT_WIDTH_FIELD_NUMBER: _ClassVar[int]
    SNAPSHOT_HEIGHT_FIELD_NUMBER: _ClassVar[int]
    ALGORITHM_RESULTS_FIELD_NUMBER: _ClassVar[int]
    success: bool
    message: str
    camera_id: str
    detections: _containers.RepeatedCompositeFieldContainer[Detection]
    llm_result: str
    snapshot: str
    snapshot_width: int
    snapshot_height: int
    algorithm_results: _containers.RepeatedCompositeFieldContainer[AlgorithmResult]
    def __init__(self, success: bool = ..., message: _Optional[str] = ..., camera_id: _Optional[str] = ..., detections: _Optional[_Iterable[_Union[Detection, _Mapping]]] = ..., llm_result: _Optional[str] = ..., snapshot: _Optional[str] = ..., snapshot_width: _Optional[int] = ..., snapshot_height: _Optional[int] = ..., algorithm_results: _Optional[_Iterable[_Union[AlgorithmResult, _Mapping]]] = ...) -> None: ...
