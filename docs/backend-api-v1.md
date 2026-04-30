# maas-box 后端 API V1

基础路径：
- 管理接口：`/api/v1`
- AI 回调：`/ai`
- 实时告警 WebSocket：`/ws/alerts`

统一响应包：
```json
{
  "code": 0,
  "msg": "ok",
  "data": {}
}
```

错误响应采用 HTTP 状态码，并返回：
```json
{
  "code": 400,
  "msg": "错误信息"
}
```

## 1. 认证
- `POST /api/v1/auth/login`
- `GET /api/v1/auth/me`

鉴权头：
- `Authorization: Bearer <jwt>`

## 2. 区域管理
- `GET /api/v1/areas`
- `POST /api/v1/areas`
- `PUT /api/v1/areas/:id`
- `DELETE /api/v1/areas/:id`

规则：
- 根区域可编辑不可删除
- 存在设备或子区域时不可删除

## 3. 设备管理
- `GET /api/v1/devices`
- `GET /api/v1/devices/:id`
- `POST /api/v1/devices`
- `PUT /api/v1/devices/:id`
- `DELETE /api/v1/devices/:id`
- `GET /api/v1/devices/:id/recording-status`
- `GET /api/v1/devices/:id/recordings`
- `GET /api/v1/devices/:id/recordings/file/*path`
- `POST /api/v1/devices/:id/recordings/export`
- `DELETE /api/v1/devices/:id/recordings`
- `GET /api/v1/devices/gb28181/info`
- `POST /api/v1/devices/gb28181/verify`
- `GET /api/v1/devices/gb28181/devices`
- `POST /api/v1/devices/gb28181/devices`
- `PUT /api/v1/devices/gb28181/devices/:device_id`
- `DELETE /api/v1/devices/gb28181/devices/:device_id`
- `POST /api/v1/devices/gb28181/devices/:device_id/catalog`
- `GET /api/v1/devices/gb28181/devices/:device_id/channels`
- `PUT /api/v1/devices/gb28181/channels/:channel_id`
- `GET /api/v1/devices/gb28181/stats`
- `GET /api/v1/devices/blacklist`
- `POST /api/v1/devices/blacklist/gb28181`
- `DELETE /api/v1/devices/blacklist/gb28181/:device_id`
- `POST /api/v1/devices/blacklist/rtmp`
- `DELETE /api/v1/devices/blacklist/rtmp/:app/:stream_id`
- `GET /api/v1/devices/discover/lan`

规则：
- `/devices` 语义为 `media_source` 主表
- `POST /devices` 仅允许创建：
  - `source_type=pull`（RTSP 拉流）
  - `source_type=push`（RTMP 推流）
- `POST/PUT /devices` 不再作为录像策略配置入口（录像策略由任务设备配置驱动）
- 设备页新增/编辑 RTSP 时，前端会显示“保存中”并阻止重复提交，直到接口返回；这是因为保存流程会同步等待 ZLM 建流结果
- `GET /devices` 支持筛选：`source_type`、`row_kind`、`area_id`、`status`、`keyword`
- `GET /devices` 额外返回：
  - `allow_continuous_recording`（来自 `Server.Recording.AllowContinuous`）
- `row_kind` 继续用于后端业务约束（任务绑定、预览、GB 父子关系），前端列表可按需隐藏
- 设备页列表字段建议保留“协议（protocol）”，不再重复展示“来源类型（source_type）”
- 分析中的设备不可编辑
- 已被任务绑定的设备不可直接删除
- GB28181 主表行为自动维护（设备行 + 通道行），仅允许通过 GB API 管理
- 删除 GB28181 设备会写入阻断记录；同一 `device_id` 后续注册将被拒绝，直到手工重新创建该设备
- 可通过黑名单接口快速查看、手工添加或移除 GB/RTMP 阻断记录
- GB28181 设备删除会先尝试发送 SIP BYE 停流，再触发 ZLM 关流清理（关闭关联通道的活跃流与 RTP server）
- GB28181 推流邀请由注册/目录回调异步触发：注册成功后邀请已知通道，目录同步后对新增通道补邀（失败后台重试，不阻塞注册）
- ZLM `on_stream_not_found` 触发缺流自愈：RTSP pull 会异步重建代理，GB28181 仅对当前缺失通道补邀（按流去重合并）
- RTSP 播放地址固定生成，不受配置开关控制（AI 输入链路依赖 RTSP）
- RTSP pull 的 `mb_stream_proxies.retry_count` 会直接下发到 ZLM `addStreamProxy`，用于代理重连/恢复；当前默认值已收敛为 `1`，优先由 Go 侧负责长时间恢复编排
- RTMP 播放地址固定生成，不受配置开关控制（ZLM 默认启用 RTMP 输出）
- 前端播放输出协议由 `Server.ZLM.Output` 控制：`webrtc`、`ws_flv`、`http_flv`、`hls`
- 默认最小输出策略：`WebRTC + WS-FLV`；其余 Web 协议按配置开启
- 录像策略由任务设备配置控制（两选一互斥）：
  - `recording_policy=none`：不录制
  - `recording_policy=alarm_clip`：仅报警片段录制（预录缓冲 + 归档）
- 任务设备录制窗口参数：
  - `recording_pre_seconds`
  - `recording_post_seconds`
- 录像策略触发时机：
  - 页面新增/编辑后立即按新策略重评估并触发启停
  - 设备自动接入（GB 注册/RTMP on_publish 自动入库）时应用默认策略
  - 流上下线（RTSP/RTMP/GB28181 的 `on_stream_changed`）时自动触发录制启停
- 设备在线/离线状态口径：
  - `on_stream_not_found` 只负责触发后台自愈，不直接改写 `mb_media_sources.status`
  - `mb_gb_devices.status` 保持 SIP 信令状态口径（REGISTER/KEEPALIVE）
  - `on_server_started` 的 RTSP pull 恢复不会在 `addStreamProxy` 成功后立刻写 `online`，只有确认 ZLM 已看到活跃流后才切在线
- 报警片段/事件视频触发规则：
  - AI 回调创建告警事件后，系统按 `recording_pre_seconds/recording_post_seconds` 处理
  - `recording_policy=alarm_clip`：采用“预录缓冲 + 会话续命”模式
    - 首次告警创建会话并设定 `expected_end_at`
    - 会话内后续告警仅续命（更新 `expected_end_at`），不新建片段文件
    - 到达结束时间后统一封口，批量回写同会话内事件 `clip_path/clip_files_json`
    - 会话封口前事件保持 `clip_ready=false`；封口后统一可播放
    - 单会话受 `Server.Recording.AlarmClip.MaxSessionSeconds` 上限约束
    - 合并成功：`clip_files_json` 通常仅保留 1 条 merged 文件
    - 合并失败：回退多段文件（不中断事件可用）
  - `recording_policy=none`：事件直接终态为无片段（`clip_ready=true` 且 `clip_files_json=[]`）
- `GET /devices/:id/recordings` 查询参数：
  - `kind=normal|alarm`（默认 `normal`）
  - `kind=normal`：返回普通录制文件
  - `kind=alarm`：返回该设备报警片段，按 `event_occurred_at desc -> mod_time desc -> path` 排序
- `GET /devices/:id/recordings` 会忽略 `.json` 文件（包括历史 `manifest.json`）
- `GET /devices/:id/recordings` 返回项扩展：
  - `kind`：`normal|alarm`
  - `event_id`：报警片段所属事件 ID（普通录制为空）
  - `event_occurred_at`：事件时间（普通录制为空）
- `DELETE /devices/:id/recordings` 删除后会同步修剪事件片段字段（`clip_files_json`、`clip_path`），避免事件继续引用已删除文件
- `POST /devices/:id/recordings/export`：
  - 请求体：`{ "paths": string[] }`
  - 返回：`application/zip`（附件下载）
  - 导出上限：最多 `200` 个文件，总大小最多 `2GB`
  - ZIP 内路径保留原录制相对路径结构（不扁平化）
- GB 通道 `stream_id` 规则：`<设备ID>_<通道ID>`（字符清洗后拼接）
- RTMP 支持两条接入路径：
  - 手工创建推流通道（预分配区域、名称、录像策略）
  - 设备先推流到 ZLM，由 `POST /webhook/on_publish` 自动接入
- RTMP `on_publish` 鉴权优先级：
  - 已存在通道：`mb_stream_pushes.publish_token` > 推流 URL 内 token > `Server.ZLM.RTMPAutoPublishToken`
  - 未知通道：必须匹配 `Server.ZLM.RTMPAutoPublishToken` 才允许自动入库
- `on_publish` 仅做鉴权/自动接入与推流心跳更新，不直接把 source 状态置在线；在线状态等待 `on_stream_changed(regist=true)`
- `on_stream_not_found` 仅做缺流自愈触发，不直接把 source 状态置在线；RTSP pull 会异步重建代理，GB28181 只补当前缺失通道
- `on_server_started` 会全量将 source 置离线并异步恢复（pull 重拉、在线 GB 设备补邀）
- 删除 RTMP 推流通道会写入 `app+stream_id` 阻断记录；同名推流将被拒绝，直到手工重新创建同名通道

`Server.ZLM.Output` 示例：
```toml
[Server.ZLM.Output]
EnableWebRTC = true
EnableWSFLV = true
EnableHTTPFLV = false
EnableHLS = false
WebFallback = "ws_flv"
```

`configs/prod/zlm.ini` 默认输出协议（推荐，本地对应 `configs/local/zlm.ini`）：
```ini
[protocol]
enable_rtsp=1
enable_rtmp=1
enable_hls=0
enable_hls_fmp4=0
enable_mp4=0
enable_ts=0
```

录制列表查询参数：
- `page`（默认 `1`）
- `page_size`（默认 `20`，最大 `200`）
- `keyword`（可选，按文件名过滤）
- `order`（`asc|desc`，默认 `desc`）

删除录制请求体：
```json
{
  "paths": ["segment_20260213_150000.mp4"]
}
```

局域网发现参数：
- `cidr`：可选，默认自动识别私网 `/24`
- `ports`：可选 CSV，默认 `554,8554,1935,80,8080`
- `timeout_ms`：可选，默认 `250`，范围 `[50,3000]`
- `concurrency`：可选，默认 `32`，范围 `[1,128]`
- `max_hosts`：可选，默认 `256`，范围 `[1,512]`
- `limit`：可选，默认 `128`，范围 `[1,512]`

局域网发现返回重点字段：
- `items[]`：可达端点，包含 `ip`、`port`、`protocol_guess`、`stream_url`、`latency_ms`
- `host_truncated`：主机数超出 `max_hosts` 时为 `true`
- `result_truncated`：结果数超出 `limit` 时为 `true`

GB28181 校验请求体：
```json
{
  "sip_server_id": "34020000002000000001",
  "sip_domain": "3402000000",
  "sip_ip": "192.168.1.10",
  "sip_port": 15060,
  "transport": "udp",
  "device_id": "34020000001320000001",
  "password": "123456",
  "media_ip": "192.168.1.10",
  "media_port": 10000,
  "register_expires": 3600,
  "keepalive_interval": 60
}
```

GB28181 校验返回字段：
- `valid`：整体是否通过（任一硬校验失败即为 `false`）
- `checks[]`：逐项检查结果（`pass|fail|warn`）
- `normalized`：补默认值后的参数快照
- `GET /devices/gb28181/info` 额外提供 `keepalive_timeout_sec`（服务端离线判定超时）
- `GET /devices/gb28181/info` 额外提供 `sip_password`（平台配置密码，仅用于接入配置展示）
- `GET /devices/gb28181/info` 的默认值不再写死，统一来自 `configs/prod/config.toml`（本地为 `configs/local/config.toml`）的 `[Server.SIP]` 参数（`RecommendedTransport/RegisterExpires/KeepaliveInterval/MediaIP/MediaRTPPort/MediaPortRange/SampleDeviceID/GuideNote/Tips`）

GB28181 设备档案新增请求体（可选）：
```json
{
  "device_id": "34020000001320000001",
  "name": "园区南门摄像头",
  "area_id": "root",
  "password": "123456",
  "enabled": true
}
```

GB28181 统计返回字段：
- `devices_total`：GB28181 设备总数（含自动注册设备）
- `enabled_total`：启用设备数
- `online_total`：在线设备数
- `offline_total`：离线设备数
- `channels_total`：目录通道总数
- `sip_listen_ip` / `sip_listen_port`：SIP 实际监听配置

## 4. 算法中心

算法：
- `GET /api/v1/algorithms`
- `GET /api/v1/algorithms/:id`
- `POST /api/v1/algorithms`
- `PUT /api/v1/algorithms/:id`
- `DELETE /api/v1/algorithms/:id`

提示词版本：
- `GET /api/v1/algorithms/:id/prompts`
- `POST /api/v1/algorithms/:id/prompts`
- `PUT /api/v1/algorithms/:id/prompts/:prompt_id`
- `DELETE /api/v1/algorithms/:id/prompts/:prompt_id`
- `POST /api/v1/algorithms/:id/prompts/:prompt_id/activate`

算法测试：
- `GET /api/v1/algorithms/test-limits`
- `POST /api/v1/algorithms/:id/test`
- `GET /api/v1/algorithms/test-jobs/:job_id`
- `POST /api/v1/algorithms/draft-test`
- `GET /api/v1/algorithms/draft-test-jobs/:job_id`
- `GET /api/v1/algorithms/:id/tests`
- `DELETE /api/v1/algorithms/:id/tests`
- `GET /api/v1/algorithms/test-media/*path`
- `GET /api/v1/algorithms/test-image/*path`

说明：
- 算法测试链路的 AI 内部接口字段契约与解析口径，统一以 [algorithm-test.md](/D:/workProject/maas/maas-box/docs/algorithm-test.md) 与 [ai-service.md](/D:/workProject/maas/maas-box/docs/ai-service.md) 的“最新口径”章节为准。

规则：
- `code` 唯一，格式：`^[A-Z][A-Z0-9_]{1,31}$`
- `POST /api/v1/algorithms` 中若未传 `code`，后端会自动生成 `CAM2ALG_xxx` 编码
- 算法模式固定为 `hybrid`（`POST/PUT /algorithms` 中传入的 `mode` 不生效）
- `small_model_label` 必填
- 大模型配置统一来自 `config.toml` 的 `[Server.AI]`（`LLMAPIURL/LLMAPIKey/LLMModel`），不再由 `model_provider_id` 控制
- 混合算法必须存在激活提示词
- 提示词 `version` 按算法内唯一（`trim` 后比较，大小写敏感）
- `POST /api/v1/algorithms` 允许附带 `prompt/prompt_version/activate_prompt`，用于创建算法时同步创建激活提示词
- `POST /api/v1/algorithms/:id/test` 当前有两种模式：
  - `application/json`：旧版单图同步测试
  - `multipart/form-data`：当前主用的批量媒体异步测试
- `GET /api/v1/algorithms/test-limits` 返回图片数、视频数、视频大小限制，对应 `config.toml -> Server.AI`
- `multipart/form-data` 模式会立即返回 `job_id`，结果需通过 `GET /api/v1/algorithms/test-jobs/:job_id` 轮询
- `POST /api/v1/algorithms/draft-test` 为 `camera2` 新增算法弹窗的草稿测试接口：
  - 固定只支持 `detect_mode=2`
  - 结果通过 `GET /api/v1/algorithms/draft-test-jobs/:job_id` 轮询
  - 不写 `AlgorithmTestRecord`
- 测试媒体当前统一存放于 `configs/test`
- 当前主用媒体访问地址为 `GET /api/v1/algorithms/test-media/*path`
- `GET /api/v1/algorithms/test-image/*path` 仍保留为旧别名，但不再作为主文档路径
- 详细说明见：
  - [algorithm-test.md](/D:/workProject/maas/maas-box/docs/algorithm-test.md)
  - [ai-service.md](/D:/workProject/maas/maas-box/docs/ai-service.md)
  - [storage-cleanup.md](/D:/workProject/maas/maas-box/docs/storage-cleanup.md)
- 激活中的提示词版本不可删除，需先切换到其他版本再删除

## 5. 标签管理

YOLO 标签：
- `GET /api/v1/yolo-labels`

规则：
- 标签数据由配置目录内置文件 `configs/yolo-label.json` 提供
- 后端在启动阶段加载并缓存标签，文件修改后需重启后端生效
- 返回结构：`[{ "label": "...", "name": "中文名" }]`
- 文件编码必须是 UTF-8（无 BOM）
- `label` 必填且全局唯一（大小写不敏感）
- `name` 必填，用于前端中文显示

## 6. 任务管理

报警等级：
- `GET /api/v1/alarm-levels`
- `POST /api/v1/alarm-levels`
- `PUT /api/v1/alarm-levels/:id`
- `DELETE /api/v1/alarm-levels/:id`

任务：
- `GET /api/v1/tasks`
- `GET /api/v1/tasks/defaults`
- `GET /api/v1/tasks/:id`
- `POST /api/v1/tasks`
- `PUT /api/v1/tasks/:id`
- `PUT /api/v1/tasks/:id/devices/:device_id/quick-config`
- `DELETE /api/v1/tasks/:id`
- `POST /api/v1/tasks/:id/start`
- `POST /api/v1/tasks/:id/stop`
- `GET /api/v1/tasks/:id/sync-status`
- `GET /api/v1/tasks/:id/prompt-preview`

规则：
- 报警等级固定为内置 3 级（`alarm_level_1..alarm_level_3`），按 `severity=1..3` 从低到高。
- `POST /api/v1/alarm-levels` 固定返回 `400`（禁止新增）。
- `DELETE /api/v1/alarm-levels/:id` 固定返回 `400`（禁止删除）。
- `PUT /api/v1/alarm-levels/:id` 仅允许修改 `name/color/description`，`severity` 固定不可改。
- 一个设备仅可属于一个任务
- 仅允许绑定 `row_kind=channel` 的媒体主表记录
- 任务创建/更新请求体使用 `device_configs[]`（按设备独立配置算法、阈值、录制策略）
- 每个 `device_config` 必须设置：
  - `algorithm_configs[]`（每算法独立配置）
  - `algorithm_configs[].algorithm_id`
  - `algorithm_configs[].alarm_level_id`（可选；为空时回退到 `Server.TaskDefaults.Video.AlarmLevelIDDefault`，非空时必须是内置 3 级之一）
  - `algorithm_configs[].alert_cycle_seconds`（`0..86400`，`0=不抑制`，默认值来自 `Server.TaskDefaults.Video.AlertCycleSecondsDefault`）
  - `frame_rate_mode`（允许值来自 `Server.TaskDefaults.Video.FrameRateModes`）
  - `frame_rate_value`（`1..60`，默认值来自 `Server.TaskDefaults.Video.FrameRateValueDefault`）
  - `recording_policy`（`none|alarm_clip`）
  - `recording_pre_seconds`
  - `recording_post_seconds`
- 运行中的任务不可编辑/删除
- `PUT /api/v1/tasks/:id/devices/:device_id/quick-config` 为 `camera2` 大屏单设备快速编辑接口，请求体仅允许：
  - `name`
  - `notes`
  - `recording_policy`
  - `algorithm_ids`
- 单设备快速编辑规则：
  - 仅修改当前设备的录制策略和算法绑定
  - 当前设备已存在算法继续沿用原报警周期、报警等级
  - 当前设备新绑定算法回退到 `GET /api/v1/tasks/defaults` 返回的默认报警周期、默认报警等级
  - 当前设备的抽帧模式、抽帧值、录制前后秒继续沿用原值
  - 同任务下其他设备配置不得被一并改动
  - 当前设备若已在运行，保存后只对当前设备执行 `stop + start`
  - 当前设备若未运行，保存后只对当前设备执行 `start`
  - 同任务下其他设备不会被停止或重启
- 启动任务时按设备生成合并后的启动计划
- 任务详情回显（`GET /api/v1/tasks`、`GET /api/v1/tasks/:id`）中，`algorithm_configs[]` 额外包含：
  - `alarm_level_id`
  - `alarm_level_name`
  - `alarm_level_color`
  - `alarm_level_severity`
- `GET /api/v1/tasks/defaults` 返回视频任务页面默认值，配置来源分两段：
  - 录制默认开关与前后秒数：`Server.Recording.AlarmClip.EnabledDefault/PreSeconds/PostSeconds`
  - 算法报警周期、告警等级、抽帧模式、默认抽帧值：`Server.TaskDefaults.Video.*`
- `GET /api/v1/tasks/defaults` 返回字段：
  - `recording_policy_default`
  - `alarm_clip_enabled_default`
  - `recording_pre_seconds_default`
  - `recording_post_seconds_default`
  - `alert_cycle_seconds_default`
  - `alarm_level_id_default`
  - `frame_rate_modes`
  - `frame_rate_mode_default`
  - `frame_rate_value_default`
- 大模型提示词按算法合并为统一任务清单：
  - `task_code` = `algorithm.code`
  - `task_name` = `algorithm.name`
- `task_mode`：固定为 `object`

## 7. 事件中心
- `GET /api/v1/events`
- `GET /api/v1/events/:id`
- `PUT /api/v1/events/:id/review`
- `GET /api/v1/events/image/*path`
- `GET /api/v1/events/:id/clips/file/*path`

事件列表查询参数：
- `status`
- `source`
  - 支持 `runtime|patrol`
  - 默认按 `runtime` 查询，保持实时报警与后台历史报警口径不混入任务巡查
- `area_id`
- `algorithm_id`
- `alarm_level_id`
- `start_at`（支持 RFC3339 或毫秒时间戳）
- `end_at`（支持 RFC3339 或毫秒时间戳）
- `task_name`
- `device_name`
- `algorithm_name`
- 兼容保留：`task_id`、`device_id`
- 事件输出新增：
  - `alarm_level_name`、`alarm_level_color`、`alarm_level_severity`
  - `area_id`、`area_name`
  - `algorithm_code`
  - `event_source`
    - `runtime`：实时任务/AI 回调产生的正式报警
    - `patrol`：`camera2` 任务巡查命中的巡查报警
  - `display_name`
    - 实时报警优先展示算法名
    - 巡查报警展示本次巡查的类型名；选算法时为算法名，自定义提示词时固定为 `任务巡查`
  - `prompt_text`：巡查实际执行的大模型提示词，仅巡查报警会返回
  - `boxes_json`：归一化中心点框数据，字段包含 `x/y/w/h/label/confidence`
  - `llm_json`：若包含 `task_results[].reason`，前端审核详情优先使用该字段展示 AI 分析结论

报警片段字段：
- `clip_session_id`：报警片段会话 ID（`alarm_clip` 会话模式下用于事件归组）
- `clip_ready`：报警片段是否已生成
- `clip_path`：报警片段目录（相对设备录像目录，按会话共享）
  - 目录规则：`alarm_clips/session_<session_id>_<yyyyMMddHHmmss>`
- `clip_files_json`：报警片段文件列表（JSON 数组）
  - `alarm_clip` 合并成功时通常长度为 `1`，失败回退时可能为多段
  - 文件命名包含时间戳前缀：`<yyyyMMddHHmmss>_*.mp4`
  - 系统不再生成 `manifest.json`
  - 同一会话内多个事件可引用同一组文件（`clip_files_json` 可相同）
- 事件输出新增 `algorithm_code`（来自 `mb_algorithms.code`）

## 7.1 数据看板概览
- `GET /api/v1/dashboard/overview`

返回核心字段：
- `summary`：
  - `total_channels`
  - `online_channels`
  - `offline_channels`
  - `alarming_channels`（近 60 秒 `pending` 去重设备数）
- `algorithm_stats[]`：`algorithm_id`、`algorithm_name`、`alarm_count`
- `level_stats[]`：`alarm_level_id`、`alarm_level_name`、`alarm_level_color`、`alarm_count`
- `area_stats[]`：`area_id`、`area_name`、`alarm_count`
- `channels[]`：
  - `id`、`name`、`status`
  - `area_id`、`area_name`
  - `play_webrtc_url`、`play_ws_flv_url`
  - `today_alarm_count`、`total_alarm_count`
  - `alarming_60s`
  - `algorithms[]`（仅运行中任务绑定算法）
- `runtime`：
  - `version`
  - `uptime_seconds`
  - `cpu_percent`
  - `memory`、`disk`
  - `network`（`rx_bps`、`tx_bps`）
  - `gpu`（不可采集时 `supported=false`）
- `generated_at`

审核状态：
- `pending`
- `valid`
- `invalid`

## 7.2 第二大屏聚合概览
- `GET /api/v1/dashboard/camera2/overview`

查询参数：
- `range=today|7days|custom`
- `start_at`、`end_at`
  - 仅 `range=custom` 时必填
  - 支持 Unix 毫秒（前端默认）、Unix 秒、RFC3339、`2006-01-02 15:04:05`

返回核心字段：
- `range`、`start_at`、`end_at`
- `alarm_statistics`
  - `total_alarm_count`
  - `pending_count`
  - `handling_rate`
  - `false_alarm_rate`
  - `high_count`
  - `medium_count`
  - `low_count`
- `algorithm_statistics`
  - `deploy_total`：启用中的算法总数（`enabled=true`）
  - `running_total`：绑定到 `ai_status=running` 设备上的算法去重数，兼容现代绑定和旧任务绑定
  - `average_accuracy`：第二大屏展示口径为 `(有效告警 + 待处理) / 总报警数`
  - `today_call_count`：今日 `task_runtime` 来源的 LLM 调用次数
  - `items[]`
    - `algorithm_id`
    - `algorithm_name`
    - `alarm_count`
    - `accuracy`：第二大屏展示口径为 `(有效告警 + 待处理) / 该算法告警总数`
- `device_statistics`
  - `total_devices`
  - `area_count`
  - `online_devices`
  - `online_rate`
  - `alarm_devices`：与 `camera` 大屏一致的当前告警设备数（取 `dashboard/overview.summary.alarming_channels` 口径）
  - `offline_devices`
  - `top_devices[]`
    - `device_id`
    - `device_name`
    - `area_id`
    - `area_name`
    - `alarm_count`
- `analysis`
  - `area_distribution[]`
  - `type_distribution[]`
  - `trend[]`
    - `label`
    - `bucket_at`
    - `alarm_count`
  - `trend_unit`：`hour|day`
- `resource_statistics`
  - `cpu_percent`：单个百分比数值
  - `memory_percent`
  - `disk_percent`：单个百分比数值
  - `network_status`
  - `network_tx_bps`
  - `network_rx_bps`
  - `token_total_limit`
  - `token_used`

## 7.3 第二大屏任务巡查
- `POST /api/v1/dashboard/camera2/patrol-jobs`
- `GET /api/v1/dashboard/camera2/patrol-jobs/:job_id`

创建请求体：
- `device_ids: string[]`
- `algorithm_id?: string`
- `prompt?: string`

校验规则：
- `device_ids` 不能为空
- `algorithm_id` 与 `prompt` 必须二选一
- 选择 `algorithm_id` 时，后端复用该算法当前启用中的提示词，缺少启用提示词会直接返回 `400`

创建返回字段：
- `job_id`
- `status`
- `total_count`

轮询返回字段：
- `job_id`
- `status`
  - `pending|running|completed|partial_failed|failed`
- `total_count`
- `success_count`
- `failed_count`
- `alarm_count`
- `items[]`
  - `device_id`
  - `device_name`
  - `status`
  - `message`
  - `event_id?`

行为约定：
- 任务巡查只抓当前一帧图片并调用现有 `/api/analyze_image`
- 命中后才写入 `mb_alarm_events`，并写 `event_source=patrol`
- 未命中不写事件，只体现在 patrol job 的 `success_count/alarm_count`
- 不创建实时任务、不生成报警片段、不影响 `dashboard/overview`、`dashboard/camera2/overview` 与 `alarming_60s` 统计口径
- 巡查产生的 LLM 调用统一按 `direct_analyze` 记账

第二大屏历史报警详情前端取数约定：
- 详情主体使用 `GET /api/v1/events/:id`
- 实时报警列表与历史报警默认查 `source=runtime`
- 巡查报警列表与历史弹窗查 `source=patrol`
- 实时画面复用 `GET /api/v1/dashboard/overview` 返回的 `channels[]` 中直播地址
- 报警片段继续通过 `GET /api/v1/events/:id/clips/file/*path` 播放
  - 播放文件优先取 `clip_files_json` 中的实际片段，支持多段切换；`clip_path` 仅作为片段目录元信息
- 巡查报警默认不生成报警片段，详情页应提示“本次巡查仅抓取当前一帧并进行单次 LLM 分析”
- 监控画面的设备状态筛选由前端基于 `channels[].status` 与 `channels[].alarming_60s` 本地完成，固定选项为“在线 / 报警 / 全部”
- 监控画面全屏使用前端声明式覆盖层渲染；普通态筛选弹层挂在 `.app-container`，全屏态挂在 `.fullscreen-grid`，切换时依赖容器卸载自动关闭
- AI 分析结论只展示 `llm_json.task_results[].reason`；若无可用 `reason`，前端统一显示“暂无分析结论”
- 巡查报警详情优先展示 `display_name`，并额外展示 `prompt_text`
- `resource_statistics`
  - `token_remaining`
  - `token_usage_rate`
  - `estimated_remaining_days`
- `generated_at`

统计口径：
- 处理率：`(valid + invalid) / total`
- 误报率：`invalid / total`
- 算法准确率：`(valid + pending) / total`
- 平均准确率：`(全量 valid + 全量 pending) / 全量 total`
- 报警等级归并：
  - `severity = 1`：高
  - `severity = 2`：中
  - `severity >= 3`：低
- 趋势分桶：
  - `today`：按小时
  - `7days`：按天
  - `custom`：时间跨度 `<= 48h` 按小时，否则按天

配置联动：
- `resource_statistics.token_*` 依赖 `[Server.AI].TotalTokenLimit`
- 当 `TotalTokenLimit <= 0` 时，接口仍返回 `token_used`，但 `token_total_limit`、`token_remaining`、`estimated_remaining_days` 会退化为未配置态

## 8. 系统管理

用户：
- `GET /api/v1/system/users`
- `POST /api/v1/system/users`
- `PUT /api/v1/system/users/:id`
- `DELETE /api/v1/system/users/:id`
- `GET /api/v1/system/users/:id/roles`
- `PUT /api/v1/system/users/:id/roles`

角色：
- `GET /api/v1/system/roles`
- `POST /api/v1/system/roles`
- `PUT /api/v1/system/roles/:id`
- `DELETE /api/v1/system/roles/:id`
- `GET /api/v1/system/roles/:id/menus`
- `PUT /api/v1/system/roles/:id/menus`

菜单：
- `GET /api/v1/system/menus`
- `POST /api/v1/system/menus`
- `PUT /api/v1/system/menus/:id`
- `DELETE /api/v1/system/menus/:id`

系统指标：
- `GET /api/v1/system/metrics`

`GET /api/v1/system/metrics` 返回（兼容增强）：
- `timestamp`
- `version`
- `uptime_seconds`
- `cpu_percent`
- `memory`
- `disk`
- `network`
- `gpu`

LLM 模板字段（内部存储）：
- 字段：`llm_role`、`llm_output_requirement`
- 启动初始化来源：`configs/llm/llm_role.md` 与 `configs/llm/llm_output_requirement.md`
- 启动行为：每次启动覆盖写入数据库对应字段
- 文件缺失或空文件：写入空值（不阻断启动）
- 任务运行与算法测试（大模型/混合）统一使用：`llm_role + 任务清单(JSON) + llm_output_requirement`

## 9. AI 回调接口
- `POST /ai/events`
- `POST /ai/started`
- `POST /ai/stopped`
- `POST /ai/keepalive`

必需请求头：
- `Authorization: <Server.AI.CallbackToken>`

`/ai/events` 中 `llm_result` 解析协议（新协议）：
```json
{
  "overall": {
    "alarm": true,
    "alarm_task_codes": ["ALG_INTRUSION"]
  },
  "task_results": [
    {
      "task_code": "ALG_INTRUSION",
      "task_name": "人员入侵",
      "task_mode": "object",
      "alarm": true,
      "reason": "检测到入侵行为",
      "object_ids": ["obj_1"]
    }
  ],
  "objects": [
    {
      "object_id": "obj_1",
      "task_code": "ALG_INTRUSION",
      "bbox2d": [100, 120, 220, 360],
      "label": "person",
      "confidence": 0.92
    }
  ]
}
```

说明：
- 事件创建以 `task_results[].alarm` 为主。
- `objects` 仅用于补充框数据，不决定是否创建事件。
- `bbox2d` 使用 `0..1000` 坐标系，后端会转换为系统归一化中心点坐标。

## 10. WebSocket
- `GET /ws/alerts`

消息示例：
```json
{
  "type": "alarm",
  "event_id": "evt_xxx",
  "task_id": "task_xxx",
  "task_name": "夜间巡检任务",
  "device_id": "dev_xxx",
  "device_name": "南门摄像头",
  "area_id": "area_xxx",
  "area_name": "南门区域",
  "algorithm_id": "alg_xxx",
  "algorithm_code": "ALG_INTRUSION",
  "algorithm_name": "人员入侵",
  "alarm_level_id": "alarm_level_2",
  "alarm_level_name": "中",
  "alarm_level_color": "#faad14",
  "occurred_at": 1739416800000,
  "notified_at": "2026-02-26T10:00:00Z",
  "status": "pending"
}
```

说明：
- `alert_cycle_seconds` 仅抑制 WebSocket 告警提示，不影响事件落库。
- 被抑制的事件 `notified_at` 为空。

## 11. 关键请求示例

创建任务：
```json
{
  "name": "夜间巡检",
  "notes": "夜班策略",
  "device_configs": [
    {
      "device_id": "dev_1",
      "algorithm_configs": [
        { "algorithm_id": "alg_person", "alarm_level_id": "alarm_level_2", "alert_cycle_seconds": 60 },
        { "algorithm_id": "alg_fire", "alarm_level_id": "alarm_level_3", "alert_cycle_seconds": 30 }
      ],
      "frame_rate_mode": "fps",
      "frame_rate_value": 5,
      "small_confidence": 0.5,
      "large_confidence": 0.8,
      "small_iou": 0.8,
      "recording_policy": "alarm_clip",
      "recording_pre_seconds": 8,
      "recording_post_seconds": 12
    },
    {
      "device_id": "dev_2",
      "algorithm_configs": [
        { "algorithm_id": "alg_smoke", "alarm_level_id": "alarm_level_3", "alert_cycle_seconds": 0 }
      ],
      "frame_rate_mode": "interval",
      "frame_rate_value": 10,
      "small_confidence": 0.45,
      "large_confidence": 0.9,
      "small_iou": 0.7,
      "recording_policy": "alarm_clip",
      "recording_pre_seconds": 8,
      "recording_post_seconds": 12
    }
  ]
}
```

审核事件：
```json
{
  "status": "valid",
  "review_note": "值班员确认有效"
}
```

## 12. LLM 配额提醒

### 12.1 `GET /api/v1/auth/me`

在原有 `cleanup_notice` / `cleanup_notices` 之外，登录态查询现在还可能返回：

- `llm_quota_notice`

当当前配置启用了 LLM token 限额，且累计 `total_tokens >= TotalTokenLimit` 时，该字段会返回一条提醒对象；未超限时不返回。

示例字段：

```json
{
  "type": "llm_quota_notice",
  "notice_id": "llm-token-quota-1743657600000",
  "issued_at": "2026-04-03T10:00:00+08:00",
  "token_total_limit": 1000000,
  "used_tokens": 1005230,
  "title": "LLM 配额提醒",
  "message": "LLM 总 token 已达到限制，AI 识别已禁用。请前往 LLM 用量统计调整限额配置，解除限制后手动重启任务。"
}
```

### 12.2 `GET /ws/alerts`

WebSocket 告警通道新增消息类型：

- `type = "llm_quota_notice"`

载荷字段与 `/api/v1/auth/me` 中的 `llm_quota_notice` 保持一致，用于：

- 主系统壳层实时弹出 LLM 配额提醒
- `camera2` 大屏实时弹出 LLM 配额提醒
