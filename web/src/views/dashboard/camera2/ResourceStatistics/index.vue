<template>
  <div class="panel resource-statistics">
    <div class="panel-header">资源统计</div>

    <div class="panel-content">
      <div class="resource-gauges">
        <div class="gauge-item" v-for="item in gauges" :key="item.label">
          <v-chart class="gauge-chart" :option="createGaugeOption(item)" auto-resize />
          <span class="gauge-label">{{ item.label }}</span>
        </div>
      </div>

      <div class="network-status">
        <span class="label">网络状态: {{ resource.network_status || '正常' }}</span>
        <span class="speed-up">↑ {{ formatCamera2BPS(resource.network_tx_bps) }}</span>
        <span class="speed-down">↓ {{ formatCamera2BPS(resource.network_rx_bps) }}</span>
      </div>

      <div class="token-usage">
        <div class="token-header">
          <img class="token-icon" :src="tokenIcon" alt="Token" />
          <span class="token-title">Token消耗</span>
        </div>

        <div class="token-summary">
          <span>总数: {{ tokenTotalText }}</span>
          <span>已用: {{ formatCamera2Number(resource.token_used) }} ({{ formatCamera2Rate(resource.token_usage_rate) }})</span>
        </div>

        <div class="token-bar">
          <div class="token-progress" :style="{ width: `${Math.min(resource.token_usage_rate, 100)}%` }"></div>
          <div class="token-marker" :style="{ left: `${Math.max(Math.min(resource.token_usage_rate - 1, 99), 0)}%` }"></div>
        </div>

        <div class="token-remaining">
          <span>剩余: {{ tokenRemainingText }}</span>
          <span>预计可用: {{ estimatedDaysText }}</span>
        </div>
      </div>

      <button class="renew-btn"></button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { use } from 'echarts/core'
import { GaugeChart } from 'echarts/charts'
import { CanvasRenderer } from 'echarts/renderers'
import { GraphicComponent } from 'echarts/components'
import VChart from 'vue-echarts'
import tokenIcon from '../assets/images/icon-title1.png'
import { fetchCamera2Overview, formatCamera2BPS, formatCamera2Number, formatCamera2Rate } from '../api'

use([GaugeChart, CanvasRenderer, GraphicComponent])

interface GaugeItem {
  label: string
  progress: number
  colors: string[]
}

type Camera2ResourceState = {
  cpu_percent: number
  memory_percent: number
  disk_percent: number
  network_status: string
  network_tx_bps: number
  network_rx_bps: number
  token_total_limit: number
  token_used: number
  token_remaining: number
  token_usage_rate: number
  estimated_remaining_days: number | null
}

// 运行时指标偶发会混入数组或字符串，这里统一收敛成单个百分比，避免仪表盘中心直接渲染原始值。
const normalizeCamera2PercentValue = (value: unknown): number => {
  if (Array.isArray(value)) {
    for (const item of value) {
      const normalized = normalizeCamera2PercentValue(item)
      if (normalized > 0 || Number(item) === 0) {
        return normalized
      }
    }
    return 0
  }
  const numeric = Number(value ?? 0)
  if (!Number.isFinite(numeric)) {
    return 0
  }
  return Math.min(Math.max(numeric, 0), 100)
}

// 进度环保留原始精度，中心文案按业务要求取整显示，避免长小数撑坏大屏可读性。
const formatCamera2GaugePercent = (value: unknown): string => {
  const normalized = normalizeCamera2PercentValue(value)
  return `${Math.round(normalized)}%`
}

const resource = ref<Camera2ResourceState>({
  cpu_percent: 0,
  memory_percent: 0,
  disk_percent: 0,
  network_status: '正常',
  network_tx_bps: 0,
  network_rx_bps: 0,
  token_total_limit: 0,
  token_used: 0,
  token_remaining: 0,
  token_usage_rate: 0,
  estimated_remaining_days: null as number | null,
})

const gauges = computed<GaugeItem[]>(() => [
  {
    label: 'CPU使用率',
    progress: Number(resource.value.cpu_percent || 0),
    colors: ['#0AA7F2', '#B0F911'],
  },
  {
    label: '内存使用率',
    progress: Number(resource.value.memory_percent || 0),
    colors: ['#00FFD1', '#FFA800'],
  },
  {
    label: '磁盘使用率',
    progress: Number(resource.value.disk_percent || 0),
    colors: ['#00C1FE', '#FE013D'],
  },
])

const tokenTotalText = computed(() => (
  resource.value.token_total_limit > 0
    ? formatCamera2Number(resource.value.token_total_limit)
    : '--'
))

const tokenRemainingText = computed(() => (
  resource.value.token_total_limit > 0
    ? formatCamera2Number(resource.value.token_remaining)
    : '--'
))

const estimatedDaysText = computed(() => (
  resource.value.token_total_limit > 0 && resource.value.estimated_remaining_days !== null
    ? `${resource.value.estimated_remaining_days.toFixed(1)}天`
    : '--'
))

let intervalId: ReturnType<typeof setInterval> | null = null

const loadResource = async () => {
  const response = await fetchCamera2Overview({ range: 'today' })
  const next = response.resource_statistics as Record<string, unknown>
  resource.value = {
    cpu_percent: normalizeCamera2PercentValue(next.cpu_percent),
    memory_percent: normalizeCamera2PercentValue(next.memory_percent),
    disk_percent: normalizeCamera2PercentValue(next.disk_percent),
    network_status: String(next.network_status || '正常'),
    network_tx_bps: Number(next.network_tx_bps || 0),
    network_rx_bps: Number(next.network_rx_bps || 0),
    token_total_limit: Number(next.token_total_limit || 0),
    token_used: Number(next.token_used || 0),
    token_remaining: Number(next.token_remaining || 0),
    token_usage_rate: normalizeCamera2PercentValue(next.token_usage_rate),
    estimated_remaining_days: next.estimated_remaining_days === null || next.estimated_remaining_days === undefined
      ? null
      : Number(next.estimated_remaining_days || 0),
  }
}

onMounted(() => {
  void loadResource()
  intervalId = setInterval(() => {
    void loadResource()
  }, 10000)
})

onUnmounted(() => {
  if (intervalId !== null) {
    clearInterval(intervalId)
  }
})

const createGaugeOption = (item: GaugeItem) => ({
  animation: false,
  series: [
    {
      type: 'gauge',
      min: 0,
      max: 100,
      startAngle: 220,
      endAngle: -40,
      radius: '100%',
      center: ['50%', '50%'],
      pointer: {
        show: false,
      },
      progress: {
        show: true,
        width: 10,
        itemStyle: {
          color: {
            type: 'linear',
            x: 0,
            y: 0,
            x2: 1,
            y2: 0,
            colorStops: [
              { offset: 0, color: item.colors[0] },
              { offset: 1, color: item.colors[1] },
            ],
          },
        },
      },
      axisLine: {
        lineStyle: {
          width: 10,
          color: [[1, 'rgba(103, 128, 170, 0.38)']],
        },
      },
      splitLine: {
        show: false,
      },
      axisTick: {
        show: false,
      },
      axisLabel: {
        show: false,
      },
      anchor: {
        show: false,
      },
      title: {
        show: false,
      },
      detail: {
        valueAnimation: false,
        offsetCenter: [0, '-2%'],
        fontSize: 20,
        fontWeight: 400,
        color: '#f7fbff',
        formatter: (value: unknown) => formatCamera2GaugePercent(value),
      },
      data: [{ value: item.progress }],
    },
    {
      type: 'gauge',
      min: 0,
      max: 100,
      startAngle: 220,
      endAngle: -40,
      radius: '80%',
      center: ['50%', '50%'],
      pointer: {
        show: false,
      },
      progress: {
        show: false,
      },
      axisLine: {
        lineStyle: {
          color: [[1, '#1D345B']]
        },
      },
      splitLine: {
        show: false,
      },
      axisTick: {
        show: false,
      },
      axisLabel: {
        show: false,
      },
      detail: {
        show: false,
      },
      title: {
        show: false,
      },
      data: [{ value: 100 }],
      z: 0,
    },
  ],
})
</script>

<style scoped lang="less">
.resource-statistics {
  width: 100%;
  height: 359px;
  overflow: hidden;
  background: url('../assets/images/bg-section-resource.png') no-repeat center center / cover;

  .panel-header {
    font-size: 21px;
    font-weight: 800;
    color: #f1f8ff;
    padding: 6px 0 0 30px;
  }

  .panel-content {
    padding: 14px 14px 18px;
  }

  .resource-gauges {
    display: flex;
    justify-content: space-between;
    gap: 4px;
    margin-bottom: 12px;
  }

  .gauge-item {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 4px;
    min-width: 0;
    flex: 1;
  }

  .gauge-chart {
    width: 100px;
    height: 80px;
  }

  .gauge-label {
    font-size: 14px;
    line-height: 1;
    color: #f1f8ff;
    margin-top: -16px;
  }

  .network-status {
    height: 39px;
    display: grid;
    grid-template-columns: 1fr 1fr 1fr;
    
    background: url('../assets/images/bg-text.png') no-repeat center center / cover;
    padding-top: 6px;
    text-align: center;

    .label {
      color: #dcecff;
      font-size: 14px;
    }
    .speed-up {
      color: #31ff82;
    }

    .speed-down {
      color: #ff5c62;
    }
  }

  .token-usage {
    margin-bottom: 20px;
  }

  .token-header {
    display: flex;
    align-items: center;
    gap: 2px;
    font-weight: bold;

    .token-icon {
      width: 26px;
      height: 27px;
      object-fit: contain;
      margin-top: 8px;
    }

    .token-title {
      font-size: 14px;
      font-weight: bold;
      color: #dcecff;
      font-style: italic;
    }
  }


  .token-summary,
  .token-remaining {
    display: flex;
    justify-content: space-between;
    font-size: 13px;
    color: #d9ebff;
  }

  .token-summary {
    margin-bottom: 6px;
  }

  .token-bar {
    position: relative;
    height: 14px;
    margin-bottom: 6px;
    overflow: hidden;
    background: linear-gradient(180deg, #142E59 0%, #0B2243 100%);
    box-shadow: inset 0px 1.5px 4.5px rgba(116, 129, 163, 0.5);
    padding: 2px;
  }

  .token-progress {
    height: 10px;
    background: linear-gradient(90deg, #022452 0%, #4487C4 44%, #4DCAF1 100%);
  }

  .token-marker {
    position: absolute;
    top: -3px;
    width: 6px;
    height: 20px;
    background: rgba(255, 255, 255, 0.9);
  }

  .renew-btn {
    width: 100%;
    height: 40px;
    background: url('../assets/images/bg-btn3.png') no-repeat center center / cover;
  }
}
</style>
