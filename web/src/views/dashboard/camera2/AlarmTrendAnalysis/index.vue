<template>
  <div class="panel alarm-trend-analysis">
    <div class="panel-header">报警趋势占比分析</div>
    <div class="panel-content">
      <div class="float-right">
        <div class="panel-tools">
          <Tabs v-model="activeTab" :tabs="tabs" @change="handleTabChange" />
          <div class="range-picker-slot" :class="{ active: activeTab === 'custom' }">
            <a-range-picker
              v-model:value="customRange"
              value-format="x"
              class="range-picker"
              show-time
              @ok="handleCustomRangeConfirm"
            />
          </div>
        </div>
      </div>
      <div class="summary-row">
        <section class="summary-card">
          <div class="summary-title">
            <span class="summary-icon"></span>
            <span>区域报警分布</span>
          </div>

          <div class="summary-body">
            <v-chart class="pie-chart" :option="regionPieOption" :autoresize="true" />
          </div>
        </section>

        <section class="summary-card">
          <div class="summary-title">
            <span class="summary-icon"></span>
            <span>报警类型分布</span>
          </div>

          <div class="summary-body">
            <v-chart class="pie-chart" :option="typePieOption" :autoresize="true" />

          </div>
        </section>
      </div>

      <div class="line-chart-wrap">
        <v-chart class="line-chart" :option="trendOption" :autoresize="true" />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { use } from 'echarts/core'
import { PieChart, LineChart } from 'echarts/charts'
import { CanvasRenderer } from 'echarts/renderers'
import { GridComponent, TooltipComponent, MarkLineComponent, TitleComponent, LegendComponent } from 'echarts/components'
import VChart from 'vue-echarts'
import Tabs from '../components/Tabs/index.vue'
import circle from '../assets/circle.png'
import { fetchCamera2Overview } from '../api'
import { useCamera2Range } from '../hooks/useCamera2Range'

use([CanvasRenderer, PieChart, LineChart, GridComponent, TooltipComponent, MarkLineComponent, TitleComponent, LegendComponent])

const { activeTab, customRange, tabs, query, handleTabChange, handleCustomRangeConfirm } = useCamera2Range()
const analysis = ref({
  area_distribution: [] as Array<{ id: string; name: string; count: number }>,
  type_distribution: [] as Array<{ id: string; name: string; count: number }>,
  trend: [] as Array<{ label: string; bucket_at: number; alarm_count: number }>,
  trend_unit: 'hour' as 'hour' | 'day',
})

const piePalette = ['#8EA5FF', '#5CF776', '#50D1FF', '#F7E05C', '#FF7F50', '#A1FF8E', '#24d7ff']

function buildPieOption(items: Array<{ name: string; count: number }>) {
  const list = items.length > 0
    ? items.map((item, index) => ({
      name: item.name || '未命名',
      value: Number(item.count || 0),
      color: piePalette[index % piePalette.length],
    }))
    : [{ name: '暂无数据', value: 0, color: '#3e5e8c' }]
  const richStyles: Record<string, any> = {}
  list.forEach((item, index) => {
    richStyles[`count${index}`] = {
      color: item.color,
      fontSize: 16,
      fontWeight: 'bold',
    }
  })
  const total = list.reduce((sum, item) => sum + item.value, 0)
  return {
    tooltip: {
      trigger: 'item',
      show: true,
    },
    legend: {
      type: 'scroll',
      right: '20%',
      top: 'center',
      icon: 'rect',
      itemWidth: 12,
      itemHeight: 12,
      formatter: (name: string) => {
        const index = list.findIndex((item) => item.name === name)
        const item = list[index]
        return `{name|${item?.name || ''}}{count${index}|${item?.value || 0}}`
      },
      width: 140,
      height: '100%',
      orient: 'vertical',
      textStyle: {
        color: '#92a0b4',
        fontSize: 12,
        fontWeight: 400,
        rich: {
          name: {
            color: '#92a0b4',
            fontSize: 12,
            fontWeight: 400,
            width: 100,
            align: 'left',
          },
          ...richStyles,
        },
      },
    },
    graphic: {
      elements: [
        {
          type: 'image',
          id: 'bg-image',
          left: '3%',
          top: 'center',
          style: {
            image: circle,
            width: 65,
            height: 65,
          },
        },
      ],
    },
    title: {
      text: String(total),
      subtext: '总数',
      left: '6%',
      top: '20%',
      itemGap: 1,
      textStyle: {
        color: '#ffffff',
        fontSize: 24,
        fontWeight: 700,
        fontFamily: '"D-DIN-PRO", "DIN Alternate", sans-serif',
      },
      subtextStyle: {
        color: '#9DD4FF',
        fontSize: 14,
        fontWeight: 400,
      },
    },
    series: [
      {
        type: 'pie',
        radius: ['78%', '94%'],
        center: ['10%', '50%'],
        startAngle: 110,
        clockwise: true,
        silent: false,
        label: { show: false },
        labelLine: { show: false },
        tooltip: {
          confine: true,
          trigger: 'item',
          show: true,
          backgroundColor: 'rgba(21, 42, 81, 0)',
          borderColor: 'rgba(68, 170, 255, 0)',
          textStyle: {
            color: '#ffffff',
            fontSize: 14,
          },
          formatter: (params: any) => `<div style="display:flex;flex-direction:column;align-items:center;">
            <span>${params.value}</span>
            <div style="display:flex;align-items:center;justify-content:center;gap:4px;">
              <div style="border-width:5px 0 5px 8px;border-style:solid;border-color:transparent transparent transparent ${params.color};"></div>
              <span>${params.name}</span>
            </div>
          </div>`,
        },
        itemStyle: {
          borderColor: '#022455',
          borderWidth: 2,
          borderRadius: 6,
          borderCap: 'round',
        },
        data: list.map((item) => ({
          value: item.value,
          name: item.name,
          itemStyle: {
            color: item.color,
          },
        })),
      },
    ],
  }
}

const regionPieOption = computed(() => buildPieOption(
  analysis.value.area_distribution.map((item) => ({ name: item.name, count: item.count })),
))

const typePieOption = computed(() => buildPieOption(
  analysis.value.type_distribution.map((item) => ({ name: item.name, count: item.count })),
))

const trendOption = computed(() => {
  const points = analysis.value.trend
  const values = points.map((item) => Number(item.alarm_count || 0))
  const maxValue = Math.max(...values, 0)
  const interval = maxValue > 0 ? Math.max(1, Math.ceil(maxValue / 4)) : 20
  return {
  // animation: false,
  tooltip: {
    trigger: 'axis',
    backgroundColor: 'rgba(21, 42, 81, 0.2)',
    borderColor: 'rgba(68, 170, 255, 0.32)',
    borderWidth: 1,
    textStyle: {
      color: '#ffffff',
      fontSize: 14
    },
    extraCssText: 'box-shadow: inset 0 0 10px #0586FF8F;',
    formatter: (params: Array<{ name: string; value: number | string }>) => {
          const {name, value} = params[0]
          return `<div style="display: flex; gap: 20px; align-items: center;">
          <span style="font-size: 14px;">${name}</span>
          <span style="font-size: 14px;color: #2E95FF;font-weight: 700;">${value}</span>
          </div>`
        },
  },
  grid: {
    left: 40,
    right: 4,
    top: 20,
    bottom: 20,
  },
  xAxis: {
    type: 'category',
    boundaryGap: false,
    data: points.map((item) => item.label),
    axisLine: {
      lineStyle: {
        color: 'rgba(255, 255, 255, 0.06)',
      },
    },
    axisTick: {
      show: true,
      length: 6,
      lineStyle: {
        color: 'rgba(255, 255, 255, 0.18)',
      },
    },
    axisLabel: {
      color: 'rgba(255, 255, 255, 0.86)',
      fontSize: 12,
      margin: 12,
    },
  },
  yAxis: {
    type: 'value',
    min: 0,
    max: Math.max(interval * 4, maxValue),
    interval,
    axisLine: { show: false },
    axisTick: { show: false },
    axisLabel: {
      color: 'rgba(255, 255, 255, 0.86)',
      fontSize: 12,
      margin: 14,
    },
    splitLine: {
      lineStyle: {
        color: 'rgba(255, 255, 255, 0.08)',
      },
    },
  },
  series: [
    {
      type: 'line',
      smooth: true,
      data: values,
      symbol: 'circle',
      symbolSize: 8,
      showSymbol: true,
      lineStyle: {
        width: 2,
        color: '#2E95FF',
        shadowBlur: 8,
        shadowColor: 'rgba(46, 149, 255, 0.4)',
      },
      
      itemStyle: {
        color: '#FFFFFF',
        borderColor: '#2E95FF',
        borderWidth: 2,
        
      },
      areaStyle: {
        color: {
          type: 'linear',
          x: 0,
          y: 0,
          x2: 0,
          y2: 1,
          colorStops: [
            { offset: 0, color: 'rgba(59, 150, 255, 0.62)' },
            { offset: 0.56, color: 'rgba(59, 150, 255, 0.22)' },
            { offset: 1, color: 'rgba(59, 150, 255, 0.02)' },
          ],
        },
      },
      markLine: {
        symbol: 'none',
        silent: true,
        lineStyle: {
          color: 'rgba(42, 117, 255, 0.98)',
          width: 1,
        },
        label: {
          show: true,
          position: 'insideStartTop',
          distance: 10,
          color: '#FFFFFF',
          backgroundColor: '#2A71FF',
          borderRadius: 4,
          padding: [7, 10],
          fontSize: 12,
        },
      },
    },
  ],
  }
})

const loadAnalysis = async () => {
  const response = await fetchCamera2Overview(query.value)
  analysis.value = response.analysis
}

const handleAlarmRefresh = () => {
  void loadAnalysis()
}

watch(query, () => {
  void loadAnalysis()
}, { deep: true, immediate: true })

onMounted(() => {
  window.addEventListener('maas-alarm', handleAlarmRefresh)
})

onUnmounted(() => {
  window.removeEventListener('maas-alarm', handleAlarmRefresh)
})
</script>

<style scoped lang="less">
.alarm-trend-analysis {
  width: 100%;
  height: 360px;
  overflow: hidden;
  background: url('../assets/images/bg-section-alarm-trend.png') no-repeat center center / cover;
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  
  .panel-header {
    width: 100%;
    font-size: 21px;
    font-weight: 800;
    color: #f1f8ff;
    padding: 6px 0 10px 30px;
  }

  .panel-icon::before,
  .panel-icon::after,
  .summary-icon::before,
  .summary-icon::after {
    content: '';
    position: absolute;
    border-radius: 50%;
  }

  .panel-icon::before,
  .summary-icon::before {
    inset: 0;
    background:
      radial-gradient(circle at 50% 50%, #94e2ff 0 16%, transparent 18% 100%),
      radial-gradient(circle at 50% 50%, rgba(74, 169, 255, 0) 38%, rgba(74, 169, 255, 0.92) 39% 56%, transparent 57%),
      radial-gradient(circle at 50% 50%, rgba(42, 134, 255, 0) 62%, rgba(42, 134, 255, 0.68) 63% 100%);
  }

  .panel-icon::after {
    inset: 5px;
    border: 1px solid rgba(104, 214, 255, 0.9);
  }

  .panel-title {
    font-size: 21px;
    line-height: 20px;
    font-weight: 800;
    color: #f1f8ff;
  }

  

  .panel-content {
    height: 0;
    flex-grow: 1;
    width: 100%;
    padding: 6px 16px 0px;
    display: flex;
    flex-direction: column;

    .float-right {
      width: 100%;
      display: flex;
      justify-content: flex-start;
      margin-bottom: 2px;
    }

    .panel-tools {
      display: flex;
      align-items: center;
      gap: 8px;
    }
    .range-picker {
      width: 260px;
    }
    .range-picker-slot {
      width: 260px;
      height: 28px;
      display: flex;
      align-items: center;
      justify-content: flex-end;
      flex: 0 0 260px;
      visibility: hidden;
      pointer-events: none;

      &.active {
        visibility: visible;
        pointer-events: auto;
      }

      :deep(.ant-picker) {
        width: 100%;
        height: 28px;
        padding: 0 10px;
        display: flex;
        align-items: center;
      }

      :deep(.ant-picker-input > input) {
        height: 26px;
        line-height: 26px;
      }

      :deep(.ant-picker-range-separator),
      :deep(.ant-picker-suffix) {
        display: flex;
        align-items: center;
      }
    }
  }

  .summary-row {
    height: 108px;
    width: 100%;
    display: flex;
    align-items: center;
    gap: 18px;
  }

  .summary-card {
    width: 0;
    flex-grow: 1;
    height: 100%;
    display: flex;
    flex-direction: column;
  }

  .summary-title {
    height: 24px;
    margin-bottom: 8px;
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 18px;
    font-family: 'YouSheBiaoTiHei', 'Microsoft YaHei', sans-serif;
    color: transparent;
    background: linear-gradient(180deg, #ffffff 16%, #308fff 84%);
    -webkit-background-clip: text;
    background-clip: text;
  }

  .summary-icon {
    position: relative;
    width: 14px;
    height: 14px;
    transform: rotate(-45deg);
  }

  .summary-icon::after {
    inset: 4px;
    border: 1px solid rgba(104, 214, 255, 0.9);
  }

  .summary-body {
    width: 100%;
    height: 0;
    flex-grow: 1;
  }

  .pie-chart {
    width: 100%;
    height: 100%;
  }

  .legend-list {
    display: flex;
    flex-direction: column;
    gap: 5px;
  }

  .type-legend {
    padding-left: 8px;
  }

  .legend-item {
    display: grid;
    grid-template-columns: 8px 1fr auto;
    align-items: center;
    column-gap: 10px;
  }

  .legend-dot {
    width: 8px;
    height: 8px;
  }

  .legend-name {
    font-size: 14px;
    color: rgba(255, 255, 255, 0.68);
    white-space: nowrap;
  }

  .legend-value {
    font-size: 16px;
    line-height: 20px;
    font-weight: 600;
  }

  .line-chart-wrap {
    height: 0;
    flex-grow: 1;
    width: 100%;
    padding: 2px 0 0;
  }

  .line-chart {
    width: 100%;
    height: 100%;
    min-height: 172px;
  }
}
</style>
