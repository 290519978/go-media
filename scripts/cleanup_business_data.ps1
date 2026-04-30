param(
  [string]$DbPath = "",
  [switch]$SkipBackup
)

$ErrorActionPreference = "Stop"

function Resolve-DefaultDbPath {
  $candidates = @(
    ".\configs\data.db",
    ".\configs\local\data.db",
    ".\configs\prod\data.db"
  )
  foreach ($item in $candidates) {
    if (Test-Path -LiteralPath $item -PathType Leaf) {
      return $item
    }
  }
  return ".\configs\data.db"
}

if ([string]::IsNullOrWhiteSpace($DbPath)) {
  $DbPath = Resolve-DefaultDbPath
}

if (-not (Test-Path -LiteralPath $DbPath -PathType Leaf)) {
  throw "db file not found: $DbPath"
}

$sqlite = Get-Command sqlite3 -ErrorAction SilentlyContinue
if ($null -eq $sqlite) {
  throw "sqlite3 command not found. Please install sqlite3 first."
}

if (-not $SkipBackup) {
  $timestamp = Get-Date -Format "yyyyMMddHHmmss"
  $backupPath = "$DbPath.bak.$timestamp"
  Copy-Item -LiteralPath $DbPath -Destination $backupPath -Force
  Write-Host "INFO: backup created: $backupPath"
}

Write-Host "INFO: cleaning db: $DbPath"

$cleanupSql = @"
PRAGMA foreign_keys = OFF;
BEGIN IMMEDIATE;

-- alarm records
DELETE FROM mb_alarm_clip_session_events;
DELETE FROM mb_alarm_clip_sessions;
DELETE FROM mb_alarm_events;

-- 算法测试：先删任务明细，再删任务和记录，避免留下孤儿数据
DELETE FROM mb_algorithm_test_job_items;
DELETE FROM mb_algorithm_test_jobs;
DELETE FROM mb_algorithm_test_records;

-- video tasks
DELETE FROM mb_video_task_device_algorithms;
DELETE FROM mb_video_task_device_profiles;
DELETE FROM mb_video_task_algorithms;
DELETE FROM mb_video_task_devices;
DELETE FROM mb_video_tasks;

-- llm usage stats
DELETE FROM mb_llm_usage_daily;
DELETE FROM mb_llm_usage_hourly;
DELETE FROM mb_llm_usage_calls;

-- devices and related rows
DELETE FROM mb_stream_proxies;
DELETE FROM mb_stream_pushes;
DELETE FROM mb_gb_channels;
DELETE FROM mb_gb_devices;
DELETE FROM mb_gb_device_blocks;
DELETE FROM mb_stream_blocks;
DELETE FROM mb_media_sources;

-- areas: keep root
DELETE FROM mb_areas WHERE id <> 'root';
INSERT INTO mb_areas(id, parent_id, name, is_root, sort, created_at, updated_at)
SELECT 'root', '', 'Root', 1, 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
WHERE NOT EXISTS (SELECT 1 FROM mb_areas WHERE id = 'root');
UPDATE mb_areas
SET parent_id = '',
    name = 'Root',
    is_root = 1,
    sort = 0,
    updated_at = CURRENT_TIMESTAMP
WHERE id = 'root';

COMMIT;
PRAGMA foreign_keys = ON;
VACUUM;
"@

& $sqlite.Source $DbPath $cleanupSql

Write-Host "INFO: cleanup completed."
Write-Host "INFO: remaining rows:"
$countSql = @"
SELECT 'mb_media_sources', COUNT(*) FROM mb_media_sources;
SELECT 'mb_areas', COUNT(*) FROM mb_areas;
SELECT 'mb_llm_usage_calls', COUNT(*) FROM mb_llm_usage_calls;
SELECT 'mb_llm_usage_hourly', COUNT(*) FROM mb_llm_usage_hourly;
SELECT 'mb_llm_usage_daily', COUNT(*) FROM mb_llm_usage_daily;
SELECT 'mb_algorithm_test_job_items', COUNT(*) FROM mb_algorithm_test_job_items;
SELECT 'mb_algorithm_test_jobs', COUNT(*) FROM mb_algorithm_test_jobs;
SELECT 'mb_algorithm_test_records', COUNT(*) FROM mb_algorithm_test_records;
SELECT 'mb_video_tasks', COUNT(*) FROM mb_video_tasks;
SELECT 'mb_alarm_events', COUNT(*) FROM mb_alarm_events;
"@
& $sqlite.Source $DbPath $countSql
