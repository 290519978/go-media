# 磁盘清理策略说明

## 1. 模块定位

磁盘清理由 Go 后端负责，核心实现位于：

- [storage_maintenance.go](/D:/workProject/maas/maas-box/internal/server/storage_maintenance.go)
- [storage_maintenance_dbsync.go](/D:/workProject/maas/maas-box/internal/server/storage_maintenance_dbsync.go)

该模块负责：

- 周期性保留期清理
- 软/硬/临界水位压缩清理
- 与数据库媒体路径字段同步

## 2. 近期已修改内容

与当前代码一致的调整如下：

- 算法测试媒体目录已从旧概念 `configs/tests` 收口到 `configs/test`
- 清理器当前处理 `configs/test`，不会再主动清理 `configs/tests`
- 清理算法测试媒体后，会同步清空数据库中的 `media_path` 与 `image_path`
- 清理日志已增强为分阶段触发、分类摘要与汇总日志

## 3. 调度方式与并发模型

- 清理器由一个定时 goroutine 周期执行
- 默认按 `Server.Cleanup.Interval` 调度
- 同一时刻只会有一轮清理运行
- 当前不是并发多 worker 清理模型

这意味着：

- 清理过程本身是串行的
- 不会同时启动多轮清理争抢同一批文件

## 4. 四层清理策略

当前清理流程包含四层：

1. `runRoutineCleanup`
2. `runSoftCompaction`
3. `runHardCompaction`
4. `runCriticalCompaction`

### 4.1 `runRoutineCleanup`

用途：

- 按保留期删除过期文件
- 不要求磁盘占用达到阈值才触发

与算法测试媒体相关：

- 当 `Server.AI.RetainDays > 0` 时，会清理 `configs/test` 下超过保留期的文件

### 4.2 `runSoftCompaction`

用途：

- 当磁盘使用率超过软水位时触发
- 优先清低价值或可再生内容

当前会处理：

- `configs/test`
- ZLM 抓拍目录
- 其他临时或低价值媒体目录

### 4.3 `runHardCompaction`

用途：

- 当磁盘使用率超过硬水位时触发
- 比软清理更激进

当前会继续回收：

- 算法测试媒体
- 录制文件
- 其他图片/快照类内容

### 4.4 `runCriticalCompaction`

用途：

- 当磁盘使用率达到临界条件时触发
- 在极端压力下执行最激进清理

如果启用了 `EmergencyBreakGlass`，还可能删除保留期内更高价值的数据。

## 5. 当前会被清理的目录

基于当前代码，清理策略会涉及以下目录：

- `configs/test`
- `configs/zlm-www/snap`
- `configs/recordings`
- `configs/cover`
- `configs/device_snapshots`
- `configs/events`
- `configs/recordings-buffer`
- 报警片段归档目录

当前代码**不会**再主动清理：

- `configs/tests`

原因是算法测试媒体根目录已经切换到 `configs/test`，清理器实现也已跟随收口。

## 6. `configs/test` 的触发条件

`configs/test` 的清理受两类条件影响：

### 6.1 保留期清理

当：

- `Server.AI.RetainDays > 0`

会在例行清理时按文件修改时间删除超过保留天数的测试媒体。

但当前会先加载算法测试 `pending/running` 分项引用的 `media_path`，命中的媒体不会被删。
如果保护集加载失败，本轮会直接跳过 `configs/test` 清理，避免误删正在跑的任务。

### 6.2 磁盘压力清理

当磁盘占用超过以下阈值时，`configs/test` 也可能被提前清理：

- 软水位
- 硬水位
- 临界水位

具体阈值来自：

- `Server.Cleanup.SoftWatermark`
- `Server.Cleanup.HardWatermark`
- `Server.Cleanup.CriticalWatermark`

其中软水位清理同样会避开 `pending/running` 算法测试分项引用的媒体；保护集加载失败时会跳过本轮测试媒体压缩清理。

## 7. 清理后的数据库同步

算法测试媒体文件被清理后，后端会同步修正测试记录：

- 清空 `mb_algorithm_test_records.media_path`
- 清空 `mb_algorithm_test_records.image_path`
- 清空 `mb_algorithm_test_job_items.media_path`

对应实现：

- `clearAlgorithmTestMediaPaths(...)`

这意味着：

- 历史测试记录本身仍在
- 但媒体预览路径会失效
- 前端应表现为“媒体不可用”，而不是继续引用脏路径

## 8. 对 AI 服务的影响边界

### 8.1 会不会影响 AI 服务

会，但当前实现已经对活跃算法测试任务做了基础保护。

原因是：

- AI 服务读取算法测试媒体时，直接访问 `configs/test`
- 例行清理与软压缩会先读取 `pending/running` 的算法测试分项并保护对应 `media_path`
- 当前仍然没有文件级引用计数，只做了 job item 级别保护
- 保护集加载失败时，会直接跳过该轮 `configs/test` 清理

因此当前主链路下，正在执行或排队中的算法测试媒体通常不会被清理器打断；历史无引用媒体仍可能被清理。

- 活跃 job item 对应媒体会被保留
- 历史测试媒体会继续按保留期或磁盘压力清理
- 如果媒体路径本身已失效，Go 侧仍会收到“文件不存在”或分析失败类错误

### 8.2 当前代码是否有使用中保护

有，但粒度是“算法测试分项”而不是“文件引用计数”。

当前代码会：

- 在保留期清理时保护 `pending/running` 分项引用的媒体
- 在软水位压缩时保护 `pending/running` 分项引用的媒体
- 在保护集加载失败时跳过本轮测试媒体清理

当前仍不会做更细粒度的文件级引用计数。

## 9. 与算法测试的关系

算法测试媒体当前统一保存在：

- `configs/test/YYYYMMDD/<batch>/...`

影响如下：

- Go 创建测试任务时写入该目录
- AI 服务分析时从该目录读取
- 前端图片/视频预览通过后端 `test-media` 接口读取该目录
- 清空测试记录时也会删除对应媒体文件

因此 `configs/test` 同时被：

- 测试任务写入
- AI 服务读取
- 前端预览访问
- 清理器删除

## 10. 当前结论

基于当前代码，可以确认：

- 会清理 `configs/test`
- 不会再主动清理 `configs/tests`
- 清理后会同步清空数据库媒体路径
- `pending/running` 算法测试分项引用的媒体会在例行清理和软压缩中被保护
- 如果保护集加载失败，会直接跳过本轮测试媒体清理
