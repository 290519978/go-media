# maas-box 开发计划（RK3588 / 4GB / 16GB）

## 1. 范围与目标
在 `maas-box` 中构建轻量边缘视频 + AI 平台，参考 `owl` 重新实现，技术路线如下：
- Go 后端（`Gin + GORM + SQLite`）
- Vue 3 前端（`Vite + Pinia + Ant Design Vue + Jessibuca`）
- Python AI 服务（`ONNX Runtime + OpenCV`，支持 HTTP/gRPC）
- 集成 ZLMediaKit 作为流媒体服务

核心目标：
- 形成从设备接入 -> 任务执行 -> AI 回调 -> 事件审核 -> 实时告警的稳定闭环。

## 2. 硬件与工程约束
- 架构：RK3588（ARM64）
- 内存：4GB
- 存储：16GB eMMC

必须遵守：
- 容器数量最小化，不引入 Redis/Kafka 等重型依赖。
- 默认禁止连续 24x7 录像。
- 报警片段录制采用预录缓冲，建议优先使用内存盘目录。
- 严格控制磁盘写入，并启用自动清理策略。

协作开发补充约束：
- Windows 环境下修改含中文文件时，必须使用明确的 UTF-8 编码方式，避免源码和文档乱码。
- 新增或调整复杂逻辑时，需要补充必要的中文注释。
- 功能、接口、配置或流程调整后，必须同步更新 `docs/`。
- 当前项目处于新项目阶段，默认不以兼容旧实现为目标，应优先删除无调用方旧代码并收敛重复逻辑。

## 3. 当前已交付基线
- 设备主模型已重构为 `mb_media_sources`（`source_type + row_kind`），并拆分扩展表：
  - `mb_stream_proxies`（RTSP 拉流扩展）
  - `mb_stream_pushes`（RTMP 推流扩展）
- GB28181 已改为“设备行 + 通道行”双写主表，页面统一读取 `/api/v1/devices`
- GB28181 通道 `stream_id` 规则已统一为 `<设备ID>_<通道ID>`（字符清洗后）
- RTMP 已支持 `on_publish` 驱动自动接入（未知流需通过全局 Token 鉴权）
- 删除后接入阻断已落地：GB 删除写 `device_id` 阻断、RTMP 删除写 `app+stream_id` 阻断，避免被设备立即自动“回灌”
- ZLM 前端播放输出已支持配置开关（`Server.ZLM.Output`），默认最小化为 `WebRTC + WS-FLV`
- RTSP、RTMP 地址固定开启，不纳入输出开关控制（AI 输入链路与 RTMP 推流链路依赖）
- 后端基础骨架：认证、RBAC、区域/设备/算法/任务/事件/系统 API。
- GB28181 最小闭环：自动注册、SIP 注册、心跳保活、目录同步与离线巡检。
- SQLite 自动迁移，表前缀统一为 `mb_*`。
- AI 集成链路：
  - 任务启动/停止调用 AI
  - AI 状态同步
  - 回调接口：`/ai/events`、`/ai/started`、`/ai/stopped`、`/ai/keepalive`
  - 回调 Token 校验
- 事件中心：
  - 回调解析
  - 事件与快照落库
  - WebSocket 推送（`/ws/alerts`）
  - 人工审核流程
- 存储保护：
  - 定时清理器（录像/事件图/测试图）
  - 按保留期清理
  - 超阈值按时间淘汰录像文件
- 前端基线：
  - 全模块路由页面
  - Dashboard 分屏与 Jessibuca 预览
  - 实时告警通知与事件聚焦跳转
  - 中控大屏改造：
    - `/dashboard` 直接升级为三栏大屏
    - 新增 `/api/v1/dashboard/overview` 聚合接口
    - 右侧告警列表支持状态/区域/算法/等级/时间筛选与懒加载
    - 中间“告警”筛选按近 60 秒 `pending` 事件计算
    - 大屏底部新增版本/运行时长/CPU/内存/GPU/网络指标与实时告警播报
- 容器化资产：
  - Go/Web 单二进制发布链路（`embed_web`）
  - `Dockerfile.ai`（AI）
  - `docker-compose.ai.yml`
  - `docker-compose.zlm.yml`
- `configs/prod/config.toml`（本地对应 `configs/local/config.toml`）
- 验证脚本：
  - `scripts/smoke-e2e.ps1` 覆盖登录、录像接口、任务启停、回调入库、事件审核

## 4. 里程碑

## M1（已完成）：核心闭环
- 认证、RBAC、核心领域模型。
- 设备/算法/任务/事件基础 CRUD。
- 任务与 AI 的启动/停止/状态同步。
- 事件回调入库与 WebSocket 推送。

验收标准：
- 完成 `设备 -> 算法 -> 任务 -> 启动 -> 回调 -> 事件 -> 审核` 全链路。

## M2（进行中）：设备与流媒体能力强化
- 设备接入体验完善（RTSP/RTMP/GB28181/ONVIF，含局域网发现）。
- GB28181 详情页与 SIP 参数校验接口/UI。
- ZLM 输出协议配置管理。
- 录像策略完善：
- 任务设备级两选一：`recording_policy=none/alarm_clip`
  - 录制窗口参数：`recording_pre_seconds + recording_post_seconds`
  - 报警片段重叠复用：同批告警只写一套文件，跨回调窗口重叠优先复用既有片段
  - 预录缓冲目录配置：`Server.Recording.AlarmClip.BufferDir/ZLMBufferDir`
  - 保留期清理与磁盘阈值控制
  - 录像文件分页查询/下载/删除

验收标准：
- 设备状态、快照信息、录制状态可在 API/UI 查询。
- 默认不出现不可控连续写盘。
- 设备页展示精简：保留“协议”列，`row_kind` 仅用于后端逻辑约束。

## M3（进行中）：算法中心强化
- 算法元数据完整 CRUD。
- 提示词版本管理与激活切换。
- 提示词版本约束与治理：算法内版本唯一、仅非激活版本可删除。
- 大模型提供方管理 + YOLO 内置标签文件（`configs/yolo-label.json`）驱动（后端启动加载缓存，替换后重启生效）。
- 图片测试结果持久化。

验收标准：
- 小模型/大模型/混合模式均通过规则校验。
- 提示词版本切换可影响运行时启动计划。

## M4（进行中）：任务编排质量
- 阻止同一设备被多个任务重复绑定。
- 运行中任务禁止编辑/删除。
- 多算法大模型提示词合并策略。
- 提供提示词预览接口便于校验。
- 报警等级与任务/事件联动增强：
  - 报警等级固定 3 级内置（只允许编辑名称/颜色/描述）
  - 每设备-每算法独立配置 `alarm_level_id + alert_cycle_seconds`
  - 事件列表返回算法对应报警等级与设备区域信息

验收标准：
- 合并提示词输出结构稳定，且可追溯到具体算法。
- 同一设备不同算法触发时，事件报警等级可按算法配置正确区分。

## M5（进行中）：事件质量与运维可控性
- YOLO/LLM 结果映射到算法事件。
- 前端框选所需归一化框坐标落库。
- 审核状态、审核人、审核时间完整记录。
- 实时告警通知闭环。

验收标准：
- 单条报警可完整审计，且支持页面复核。

## M6（进行中）：部署与运行优化
- 后端/前端/AI/ZLM 支持拆分部署。
- Nginx 反向代理支持 WebSocket 与 `X-Forwarded-Host`。
- ARM64 构建与低内存默认配置。

验收标准：
- `docker compose -f docker-compose.ai.yml up -d && docker compose -f docker-compose.zlm.yml up -d` 后，单二进制 `maas-box-linux-arm64` 可在 ARM64 设备稳定运行。

## 5. 剩余工作清单
- 完善生产环境反向代理与 NAT 场景运维文档。
- 增加关键集成测试：
  - GB28181 参数校验边界分支（主机名、本地/远端可达性）
  - 任务启停与部分失败状态迁移
- 持续执行并维护开发规范文档：
  - `AGENTS.md`
  - `docs/development-guidelines.md`

## 6. 风险与应对
- AI 占用内存风险（4GB 受限）：
  - AI 单进程/单工作线程
  - 保守抽帧默认值
- Flash 磨损风险：
  - 默认关闭连续录像
  - 强制保留期与自动清理
- 回调链路不一致风险：
  - 固定回调 Token 校验
  - 提供系统设置接口支持 Token 轮换

## 7. 后续推荐迭代顺序
1. 完成 ARM64 生产部署运行手册。
2. 完成 NAT 与反向代理排障附录。
3. 扩展任务部分失败和 GB28181 网络分支的集成测试覆盖。
