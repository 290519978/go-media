#!/usr/bin/env bash
set -euo pipefail

# 清理范围：
# - 设备
# - 区域（保留并重建 root）
# - LLM 用量统计
# - 视频任务
# - 报警记录
# - 算法测试记录 / Job / Job 明细
#
# 用法：
#   bash scripts/cleanup_business_data.sh [DB_PATH]
#
# 示例：
#   bash scripts/cleanup_business_data.sh
#   bash scripts/cleanup_business_data.sh ./configs/local/data.db

pick_default_db() {
  local candidates=(
    "./configs/data.db"
    "./configs/local/data.db"
    "./configs/prod/data.db"
  )
  local item
  for item in "${candidates[@]}"; do
    if [[ -f "${item}" ]]; then
      printf "%s" "${item}"
      return 0
    fi
  done
  printf "%s" "./configs/data.db"
}

DB_PATH="${1:-$(pick_default_db)}"

if [[ ! -f "${DB_PATH}" ]]; then
  echo "ERROR: db file not found: ${DB_PATH}" >&2
  exit 1
fi

if ! command -v sqlite3 >/dev/null 2>&1; then
  echo "ERROR: sqlite3 command not found. Please install sqlite3 first." >&2
  exit 1
fi

BACKUP_PATH="${DB_PATH}.bak.$(date +%Y%m%d%H%M%S)"
cp -f "${DB_PATH}" "${BACKUP_PATH}"
echo "INFO: backup created: ${BACKUP_PATH}"
echo "INFO: cleaning db: ${DB_PATH}"

sqlite3 "${DB_PATH}" <<'SQL'
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
SQL

echo "INFO: cleanup completed."
echo "INFO: remaining rows:"
sqlite3 "${DB_PATH}" <<'SQL'
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
SQL
