# maas-box 架构设计

## 0. 专题文档

以下专题文档按当前代码实现维护，优先作为详细说明入口：

- AI 服务说明：[ai-service.md](/D:/workProject/maas/maas-box/docs/ai-service.md)
- 磁盘清理策略说明：[storage-cleanup.md](/D:/workProject/maas/maas-box/docs/storage-cleanup.md)
- 算法测试说明：[algorithm-test.md](/D:/workProject/maas/maas-box/docs/algorithm-test.md)

## 1. 设计原则
- 在功能覆盖与系统稳定性之间，优先保证稳定性与资源可控。
- 在 4GB 内存约束下，运行时架构保持轻量，不引入重型中间件。
- 模块边界清晰，便于后续替换协议、算法模型或数据库。
- 任务状态与回调链路必须可观测、可追溯、可审计。

## 2. 运行拓扑

核心服务：
1. `maas-box-backend`（Go，单二进制内嵌 Web）
2. `maas-box-ai`（Python）
3. `zlm`（ZLMediaKit）

主流程：
- Web -> Backend API（`/api/v1/**`）
- Backend <-> AI（`/api/start_camera`、`/api/stop_camera`、`/api/status`、`/api/analyze_image`）
- AI -> Backend 回调（`/ai/events`、`/ai/started`、`/ai/stopped`、`/ai/keepalive`）
- Backend -> WebSocket（`/ws/alerts`）
- 设备流按任务配置进入 ZLM 或 AI 分析链路

## 3. 后端分层（Go）

接入层：
- `Gin` 路由
- JWT 认证中间件
- AI 回调 Token 校验
- GB28181 SIP 接入服务（UDP/TCP 15060）

应用层：
- 区域/设备/算法/任务/事件/系统等用例处理
- 任务编排与 AI 请求参数组装
- 设备级抽帧策略：`frame_rate_mode + frame_rate_value`（`fps`=每秒几帧，`interval`=每几秒1帧）
- 设备级算法策略：`device_configs[].algorithm_configs[]`（`algorithm_id + alarm_level_id + alert_cycle_seconds`）
- AI 输入流固定使用 RTSP（`play_rtsp_url -> output_config.rtsp -> stream_url`）
- GB 设备注册成功后异步自动邀流；目录同步后会对新增通道补邀（同设备并发触发合并）
- 设备抓拍在无真实流或命中 ZLM 默认回退快照（如 `logo.png`）时直接失败，不写入 `snapshot_url`
- 局域网设备发现（CIDR/端口扫描，带超时与并发上限）
- 录制文件生命周期接口（列表/下载/导出/删除）
- 录像策略引擎（任务两态：`none/alarm_clip`）与策略触发（任务启动/停止、自动接入、流上下线）
- 录制策略在任务设备级配置：`device_configs[].recording_policy + recording_pre_seconds + recording_post_seconds`
- `recording_policy=alarm_clip`：运行预录缓冲（短切片）并在 AI 告警后归档报警片段，默认尝试合并为单视频（失败回退多段）；同批事件仅写一套文件，重叠窗口事件优先复用已有片段
- `recording_policy=none`：事件直接终态为无片段（`clip_ready=true` 且 `clip_files_json=[]`）
- 录制文件列表支持按 `kind=normal|alarm` 过滤；报警片段按事件时间倒序输出
- 录制文件删除后即时同步修剪事件 `clip_files_json/clip_path`
- 状态语义：`on_stream_not_found` 仅触发后台自愈，不直接改写 `mb_media_sources.status`
- 状态语义：`mb_gb_devices.status` 仅表示 SIP 信令在线状态（REGISTER/KEEPALIVE），不直接等同媒体流状态
- GB28181 接入信息与 SIP 参数校验（格式+端口/可达性）
- GB28181 自动注册、设备档案管理、注册鉴权、心跳保活、目录同步
- RTMP `on_publish` Hook 驱动自动接入（全局 Token 鉴权）
- ZLM 缺流自愈：`on_stream_not_found` 会对 RTSP pull 异步重建代理，对 GB28181 仅补邀当前缺失通道（按流去重合并）
- RTSP pull 代理创建会按 `source_id` 串行化，统一覆盖预览、抓拍、缺流自愈与重启恢复，避免同一路并发重复建代理
- ZLM 重启恢复：`on_server_started` 先全量置离线，再异步执行 pull 重拉与在线 GB 设备补邀；RTSP pull 会沿用 `mb_stream_proxies.retry_count`，当前默认值收敛为 `1`，且仅在确认 ZLM 已看到活跃流后才回写在线状态

领域层：
- 核心聚合：
  - `Area`
  - `MediaSource`（统一设备/通道主表）
  - `Algorithm`
  - `VideoTask`
  - `AlarmEvent`
- 关键约束：
  - 根区域可编辑不可删除
  - 含设备/子区域的区域不可删除
  - 同一设备不可被多个任务同时绑定
  - 运行中的任务不可编辑/删除

基础设施层：
- `GORM + SQLite`（主路径），模型结构兼容 Postgres/MySQL
- 配置：TOML（`go-toml/v2`）
- 主机指标采集：`gopsutil`
- 实时告警：WebSocket Hub

## 4. 数据模型要点

核心表：
- `mb_areas`、`mb_media_sources`
- `mb_stream_proxies`、`mb_stream_pushes`
- `mb_gb_devices`、`mb_gb_channels`
- `mb_algorithms`、`mb_algorithm_prompt_versions`
- `mb_model_providers`
- `mb_alarm_levels`、`mb_video_tasks`
- `mb_video_task_device_profiles`、`mb_video_task_device_algorithms`
- （兼容存量）`mb_video_task_devices`、`mb_video_task_algorithms`
- `mb_alarm_events`、`mb_algorithm_test_records`
- `mb_users`、`mb_roles`、`mb_menus`、`mb_user_roles`、`mb_role_menus`
- `mb_system_settings`

算法中心页面结构约束：
- “YOLO 标签管理”并入“模型管理/算法管理”的标签选择能力，不提供独立 CRUD
- 标签来源统一为配置目录内置文件 `configs/yolo-label.json`（字段仅 `label/name`）
- 标签在后端启动阶段加载到内存缓存，运行时不热更新；替换文件后需重启后端

存储策略：
- 实时报警回调与 `camera2` 任务巡查命中都会保存快照
- 事件图片路径：`configs/events/YYYYMMDD/*.jpg`
- 算法测试媒体路径：`configs/test/YYYYMMDD/<batch>/*`
- `camera2` 任务巡查抓拍会先暂存到 `configs/test/YYYYMMDD/*`，供 `/api/analyze_image` 读取
- 算法封面路径：`configs/cover/YYYYMMDD/*`
- 设备快照路径：`configs/device_snapshots/<device_id>.jpg`
- AI 日志路径：`configs/logs/ai/analysis.log`
- 录制目录由配置控制，必须启用保留策略
- 报警片段归档路径：`configs/recordings/<device_id>/alarm_clips/session_<session_id>_<yyyyMMddHHmmss>/*`（会话内事件共享）
- 报警片段文件命名包含时间戳前缀：`<yyyyMMddHHmmss>_*.mp4`，不再生成 `manifest.json`
- 报警预录缓冲路径：`configs/recordings-buffer/<device_id>/*`
- ZLM 抓拍临时路径：`configs/zlm-www/snap/*`
- 存储清理器定期执行，支持按保留期清理与磁盘阈值压缩
- 预录缓冲目录（`_alarm_buffer`）按秒级保留窗口自动回收
- 清理器采用分级水位策略：
  - `Soft`：先清临时与低价值图片，再回收持续录制
  - `Hard`：继续回收持续录制，并仅删除超保留期告警证据
  - `Critical + EmergencyBreakGlass=true`：允许按最旧顺序删除保留期内证据
- 磁盘清理详细说明见：[storage-cleanup.md](/D:/workProject/maas/maas-box/docs/storage-cleanup.md)

设备主表约束：
- `mb_media_sources.row_kind` 区分设备行/通道行，继续作为后端约束字段
- 任务绑定仅允许 `row_kind=channel` 的主表记录
- `mb_alarm_levels` 固定 3 级内置（`alarm_level_1..alarm_level_3`，`severity=1..3`），仅允许编辑名称/颜色/描述
- `mb_video_task_device_algorithms.alarm_level_id` 为事件等级主来源（按设备-算法粒度）
- GB28181 通道行 `stream_id` 统一为 `<设备ID>_<通道ID>`（清洗后）
- RTMP 未知流可由 Hook 自动生成通道行（默认区域 `root`，后续可编辑）
- RTSP、RTMP 输出地址固定生成，不纳入协议开关控制
- 删除接入源后写入阻断记录：GB 按 `device_id`、RTMP 按 `app+stream_id` 拦截再次自动接入

## 5. 任务与提示词编排

按设备生成启动计划：
- 合并启用算法中的小模型标签（labels）
- 将多个大模型/混合算法提示词合并为统一提示词块
- 提示词全局前后缀来源固定为系统设置：
  - `llm_role`
  - `llm_output_requirement`
  - 系统设置启动初始化来源：`configs/llm/llm_role.md`、`configs/llm/llm_output_requirement.md`
  - 启动时每次覆盖数据库值；文件缺失或空文件写入空值
- 提示词任务清单按算法编码升序拼接：
  - `task_code` = `algorithm.code`
  - `task_name` = `algorithm.name`
  - `task_mode`：固定为 `object`
- 告警提示按 `task_id + device_id + algorithm_id` 维度，基于 `alert_cycle_seconds` 节流
- 检测模式映射：
  - `1`：仅小模型
  - `2`：仅大模型
  - `3`：小模型+大模型

统一大模型输出契约：
- 后端在合并提示词中强制 JSON 输出格式（`overall + task_results + objects`）：
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
      "reason": "text",
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
- 用于在事件入库时通过 `task_code -> algorithm.code` 稳定映射至具体算法。
- 算法中心“单图测试”在大模型/混合模式下复用同一提示词协议（单算法任务清单）。
- 算法中心“图片/视频测试”统一先落盘到 `configs/test/`，Go 仅向 AI 下发相对路径；AI 通过共享挂载目录读取原始媒体文件。
- 算法中心“视频测试”仅支持 DashScope SDK 本地文件视频输入，默认限制为 `100MB`、`2 秒到 20 分钟`，`fps` 由 `Server.AI.AlgorithmTestVideoFPS` 配置。
- 算法中心“批量测试”当前按上传顺序逐项调用 AI，不再并发压测同一个 AI HTTP 入口。
- Go 侧 AI 客户端会区分建连失败、响应体读取失败、HTTP 状态失败和响应解析失败，不再把响应中断统一误报为 JSON 解析错误。
- AI HTTP API 使用多线程服务，并对未捕获异常统一返回 JSON 500，避免以 TCP 断连作为主要失败表现。
- AI 图片/视频测试接口会记录请求接收、业务开始、业务结束、响应就绪、响应发送等生命周期日志，便于定位“模型已返回但回包阶段失败”。
- AI 服务与算法测试详细说明见：
  - [ai-service.md](/D:/workProject/maas/maas-box/docs/ai-service.md)
  - [algorithm-test.md](/D:/workProject/maas/maas-box/docs/algorithm-test.md)

## 6. 事件处理流水线

1. AI 回调到达 `/ai/events`。
2. 后端校验回调 Token。
3. 解析 YOLO 检测结果与 LLM JSON。
4. 与任务算法进行映射：
  - 小模型/混合模式：按 YOLO label 映射
  - 大模型/混合模式：按 `task_results[].task_code -> algorithm.code` 映射
  - `objects[]` 仅用于补充 `boxes_json`（不决定是否创建事件）
5. 落库事件并保存归一化框坐标。
6. 根据算法告警周期判定是否通过 WebSocket 广播告警消息（抑制时仅入库，不弹窗）。
7. 事件 `alarm_level_id` 取自任务中的设备-算法配置（`mb_video_task_device_algorithms.alarm_level_id`）。
8. WebSocket 告警消息新增区域与报警等级字段（`area_id/area_name`、`alarm_level_id/alarm_level_name`），用于大屏实时展示。
9. 前端弹出告警通知并支持跳转到聚焦画面。
10. 若任务设备配置启用报警片段录制，按 `pre/post` 窗口异步归档片段并回写事件 `clip_path`（`alarm_clip` 默认尝试合并单文件，失败回退多段；会话目录命名为 `session_<id>_<ts>`，不产出 `manifest.json`）。

补充链路：
- `camera2` 任务巡查不走实时任务编排，而是走单独的内存 patrol job
- patrol job 对每路设备只抓取当前一帧，经 `/api/analyze_image` 做一次单图 LLM 分析
- 仅命中时才写事件，事件会标记 `event_source=patrol`
- patrol 事件不会触发报警片段归档，也不会参与 `dashboard/overview`、`dashboard/camera2/overview` 与设备 `alarming_60s` 统计

## 7. 前端架构（Vue 3）

技术栈：
- Vue 3 + Composition API
- Pinia
- Vue Router 4
- Ant Design Vue
- Axios（JWT 拦截）

功能模块：
- Dashboard（中控大屏入口）：
  - `/dashboard` 作为统一的数据看板入口页，当前提供“摄像头监控大屏”和“鸿眸多模态视频监控预警中心”两个大屏入口
  - 两套大屏默认都通过固定覆盖层以应用内伪全屏展示，不再额外提供前端内置的“进入/退出浏览器全屏”按钮
  - 摄像头监控大屏：
    - 左侧：算法识别统计、事件分级、区域报警统计（全历史口径）
    - 中部：1/4/9 分屏，设备过滤（在线/全部/告警），设备卡片展示算法标签、区域、今日/累计告警
    - 右侧：实时告警懒加载列表，支持按状态/区域/算法/时间/等级筛选
    - 底部：版本、后端运行时长、CPU/内存/GPU/网络/磁盘指标
  - 实时联动：复用 `/ws/alerts`，新告警即时插入列表并显示底部提示文案
  - 鸿眸多模态视频监控预警中心：
    - 通过独立页面 `camera2.html` + 同源 iframe 承载，与主应用做文档级样式隔离
    - 页内通过 `postMessage({ type: 'camera2-exit' })` 通知父层关闭大屏
    - 顶部时间使用本地秒级时钟，登录人来自 `/api/v1/auth/me`
    - 左侧、趋势分析区、资源区统一使用 `GET /api/v1/dashboard/camera2/overview`
      - 支持 `today`、`7days`、`custom`
      - 处理率、误报率、算法准确率按混合口径计算
      - `camera2` 展示口径中报警等级按 `severity=1/2/3 -> 高/中/低` 归并
      - 设备统计里的“报警设备”与 `camera` 大屏一致，按当前 `alarming_channels` 展示
      - 资源区 CPU/磁盘仪表盘只接收单个百分比数值，前端会兜底收敛异常数组输入
    - 中部监控画面继续复用 `GET /api/v1/dashboard/overview.channels`
      - 前端本地完成区域、算法、设备、设备名过滤和 `1/4/9` 宫格分页
      - 设备右上角 `video-warning` 依据 `alarming_60s` 展示
    - 右侧预警事件复用 `GET /api/v1/events`、`GET /api/v1/events/:id`、`PUT /api/v1/events/:id/review`
      - 实时报警固定展示最新 10 条，查询口径固定为 `source=runtime`
      - 巡查报警页签展示 `source=patrol` 的命中结果，历史弹窗按同口径分页查询
      - 列表与详情优先展示 `display_name`；巡查详情额外展示 `prompt_text`
      - 巡查报警默认不生成报警片段，详情页只保留截图、实时画面和单次 LLM 结论
    - 顶部“任务巡查”和底部自然语言输入都归一到 patrol job
      - “任务巡查”支持按区域/设备树多选，可选算法或直接输入提示词，二者互斥
      - 底部输入框默认对当前 `camera2` 全部视频设备发起一次单帧检查，输入内容直接作为 LLM 提示词
      - “实时巡查 / 近两小时 / 自定义”时间选项仅保留禁用态，不再创建实时任务
    - `camera2` 自己在 iframe 文档内订阅 `/ws/alerts`
      - 收到告警后派发 `maas-alarm` 自定义事件，驱动告警列表、监控画面、报警统计、智能算法、设备统计和趋势分析刷新
      - patrol job 完成后派发 `maas-patrol-refresh`，用于刷新巡查报警列表
    - 资源统计中的 token 配额依赖 `[Server.AI].TotalTokenLimit`
      - 资源区按 `camera` 大屏同口径独立轮询，挂载后立即拉取一次，随后每 10 秒刷新一次，不依赖告警事件触发
    - 左侧统计区与趋势分析区的时间筛选保持面板级独立
      - 点击“自定义”只展开时间框，不立即发起查询
      - 用户选完整时间并确认后，当前面板才按 `range=custom` 重新查询
- 设备/区域/算法/任务/事件/系统管理页面
- 应用壳层 WebSocket 告警通知

播放器：
- Jessibuca v3（`public/assets/js`）
- 播放地址以 `mb_media_sources` 的 `play_*` 字段为准，`output_config` 仅保留为调试信息
- 前端默认最小播放集：`WebRTC + WS-FLV`（其余 Web 协议按配置决定是否暴露）
- ZLM 默认协议输出为 `RTSP + RTMP`，其他协议需在 `zlm.ini` 显式开启

## 8. 部署架构

容器化：
- 后端：单二进制运行（Go + Web dist 嵌入）
- AI：`Dockerfile.ai`
- 编排：`docker-compose.ai.yml` + `docker-compose.zlm.yml`
- 运行环境需具备 `ffmpeg`，用于报警片段合并

反向代理：
- Web Nginx 将 `/api`、`/ai`、`/ws` 转发到后端
- 透传 `X-Forwarded-Host`
- 支持 WebSocket 协议升级

## 9. 资源控制策略（RK3588 / 4GB / 16GB）

内存：
- 默认仅 4 个核心容器
- Compose 设置容器级内存上限
- AI 默认采用保守抽帧策略

存储：
- 默认禁止 24 小时连续录像
- 采用报警片段优先策略
- 录像模式与默认值配置化（`Server.Recording.Modes/DefaultMode`）
- 报警片段策略配置化（`Server.Recording.AlarmClip.*`）
- 视频任务页面默认值配置化（`Server.TaskDefaults.Video.*`）
- 必须启用快照/录像/测试图保留与清理

运行保障：
- AI 回调必须携带鉴权 Token
- 核心 API 参数严格校验
- 提供任务状态同步接口，保证任务与 AI 实际状态一致
