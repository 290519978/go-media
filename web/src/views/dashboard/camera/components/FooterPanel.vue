<template>
  <div class="footer-panel">
    <div class="left-info">
      <div class="info-item">
        <span class="icon">🔑</span>
        <span>版本 v{{ systemInfoData.version }}</span>
      </div>
      <div class="info-item">
        <span class="icon">🕵</span>
        <span>运行时间 {{ uptimeText }}</span>
      </div>
    </div>

    <div class="center-marquee">
      <div class="marquee-content" :style="{ animationDuration: marqueeDuration }">
        <span v-for="(item, index) in marqueeItems" :key="`${index}-${item}`" class="marquee-item">{{ item }}</span>
      </div>
    </div>

    <div class="right-stats">
      <div class="stat-item">
        <span class="icon">🖥️</span>
        <span class="label">CPU</span>
        <span class="value yellow">{{ systemInfoData.cpuUsage.toFixed(1) }}%</span>
        <div class="bar-bg">
          <div class="bar-fill yellow" :style="{ width: `${clampPercent(systemInfoData.cpuUsage)}%` }"></div>
        </div>
      </div>

      <div class="stat-item">
        <span class="icon">🔑</span>
        <span class="label">MEM</span>
        <span class="value green">{{ systemInfoData.memUsage.toFixed(1) }}%</span>
        <div class="bar-bg">
          <div class="bar-fill green" :style="{ width: `${clampPercent(systemInfoData.memUsage)}%` }"></div>
        </div>
      </div>

      <div class="stat-item">
        <span class="icon">🎃</span>
        <span class="label">DISK</span>
        <span class="value red">{{ systemInfoData.diskUsage.toFixed(1) }}%</span>
        <div class="bar-bg">
          <div class="bar-fill red" :style="{ width: `${clampPercent(systemInfoData.diskUsage)}%` }"></div>
        </div>
      </div>

      <div class="stat-item">
        <span class="icon">📱</span>
        <span class="label">NET</span>
        <span class="value blue">↑{{ netTxText }} / ↓{{ netRxText }}</span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { systemInfoApi, type SystemInfoData } from '../Camera.api'

const systemInfoData = ref<SystemInfoData>({
  version: '-',
  uptimeSeconds: 0,
  cpuUsage: 0,
  memUsage: 0,
  diskUsage: 0,
  netTxBps: 0,
  netRxBps: 0,
})

const notices = ref<string[]>([])
let intervalId: ReturnType<typeof setInterval> | null = null

const marqueeItems = computed(() => {
  if (notices.value.length === 0) {
    return ['等待实时告警...']
  }
  return [...notices.value, ...notices.value]
})

const marqueeDuration = computed(() => {
  const base = Math.max(16, notices.value.length * 5)
  return `${base}s`
})

const uptimeText = computed(() => {
  const total = Math.max(0, Math.floor(systemInfoData.value.uptimeSeconds))
  const day = Math.floor(total / 86400)
  const hour = Math.floor((total % 86400) / 3600)
  const minute = Math.floor((total % 3600) / 60)
  const second = total % 60
  return `${day}天${hour}小时${minute}分${second}秒`
})

const netTxText = computed(() => formatSpeed(systemInfoData.value.netTxBps))
const netRxText = computed(() => formatSpeed(systemInfoData.value.netRxBps))

function clampPercent(value: number): number {
  if (!Number.isFinite(value)) return 0
  if (value < 0) return 0
  if (value > 100) return 100
  return value
}

function formatSpeed(value: number): string {
  const num = Number(value || 0)
  if (num <= 0) {
    return '0 B/s'
  }
  if (num < 1024) {
    return `${num.toFixed(0)} B/s`
  }
  if (num < 1024 * 1024) {
    return `${(num / 1024).toFixed(2)} KB/s`
  }
  return `${(num / 1024 / 1024).toFixed(2)} MB/s`
}

function formatAlarmNotice(payload: Record<string, unknown>): string {
  const raw = Number(payload.occurred_at || 0)
  const time = raw > 0 ? new Date(raw) : new Date()
  const hh = String(time.getHours()).padStart(2, '0')
  const mm = String(time.getMinutes()).padStart(2, '0')
  const ss = String(time.getSeconds()).padStart(2, '0')
  const area = String(payload.area_name || payload.area_id || '未知区域')
  const algorithm = String(payload.algorithm_name || payload.algorithm_id || '未知算法')
  return `[${hh}:${mm}:${ss}]${area} 检测到 ${algorithm}`
}

async function getData() {
  const res = await systemInfoApi()
  systemInfoData.value = res
}

function pushNotice(text: string) {
  const normalized = text.trim()
  if (!normalized) {
    return
  }
  const idx = notices.value.findIndex((item) => item === normalized)
  if (idx >= 0) {
    notices.value.splice(idx, 1)
  }
  notices.value.unshift(normalized)
  notices.value = notices.value.slice(0, 20)
}

function onRealtimeAlarm(evt: Event) {
  const payload = ((evt as CustomEvent<Record<string, unknown>>).detail || {}) as Record<string, unknown>
  pushNotice(formatAlarmNotice(payload))
}

onMounted(() => {
  void getData()
  intervalId = setInterval(() => {
    void getData()
  }, 10000)
  window.addEventListener('maas-alarm', onRealtimeAlarm)
})

onUnmounted(() => {
  if (intervalId !== null) {
    clearInterval(intervalId)
  }
  window.removeEventListener('maas-alarm', onRealtimeAlarm)
})
</script>

<style lang="less" scoped>
  .footer-panel {
    border-top: 1px solid #00356d;
    width: 100%;
    height: 100%;
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 200px;
    padding: 0 20px;
    color: #b9d5ed;
    font-size: 12px;
    box-sizing: border-box;

    .left-info {
      display: flex;
      gap: 40px;
      align-items: center;

      .info-item {
        display: flex;
        align-items: center;
        gap: 5px;

        .icon {
          opacity: 0.7;
        }
      }
    }

    .center-marquee {
      flex: 1;
      margin: 0 40px;
      height: 24px;
      background: rgba(0, 0, 0, 0.8);
      border-radius: 12px;
      overflow: hidden;
      display: flex;
      align-items: center;

      .marquee-content {
        display: flex;
        white-space: nowrap;
        animation: marquee linear infinite;

        .marquee-item {
          margin-right: 80px;
          color: #ffccc7;
        }
      }
    }

    .right-stats {
      display: flex;
      gap: 40px;
      align-items: center;

      .stat-item {
        display: flex;
        align-items: center;
        gap: 8px;

        .label {
          color: #fff;
          font-weight: bold;
        }

        .value {
          width: 40px;
          text-align: right;

          &.yellow {
            color: #faad14;
          }
          &.green {
            color: #52c41a;
          }
          &.red {
            color: #ff4d4f;
          }
          &.blue {
            color: #1890ff;
            width: auto;
          }
        }

        .bar-bg {
          width: 50px;
          height: 6px;
          background: rgba(255, 255, 255, 0.1);
          border-radius: 3px;
          overflow: hidden;

          .bar-fill {
            height: 100%;
            border-radius: 3px;

            &.yellow {
              background: #faad14;
            }
            &.green {
              background: #52c41a;
            }
            &.red {
              background: #ff4d4f;
            }
          }
        }
      }
    }
  }

  @keyframes marquee {
    0% {
      transform: translateX(100%);
    }
    100% {
      transform: translateX(-100%);
    }
  }
</style>
