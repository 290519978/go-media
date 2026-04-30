<template>
  <div class="panel alarm-statistics">
    <div class="panel-header">报警统计</div>

    <div class="panel-content">
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
      <div class="stats-grid">
        <div class="stat-card red">
          <img src="../assets/images/img-alarm-total.png" alt="报警总数" />

          <div class="stat-info">
            <span class="stat-label">总报警数</span>
            <span class="stat-value">{{ formatCamera2Number(statistics.total_alarm_count) }}</span>
          </div>
        </div>

        <div class="stat-card yellow">
          <img src="../assets/images/img-alarm-unhandled.png" alt="未处理报警" />

          <div class="stat-info">
            <span class="stat-label">未处理</span>
            <span class="stat-value">{{ formatCamera2Number(statistics.pending_count) }}</span>
          </div>
        </div>

        <div class="stat-card green">
          <img src="../assets/images/icon-alarm-handled-rate.png" alt="处理率" />

          <div class="stat-info">
            <span class="stat-label">处理率</span>
            <span class="stat-value">{{ formatCamera2Rate(statistics.handling_rate) }}</span>
          </div>
        </div>

        <div class="stat-card cyan">
          <img src="../assets/images/icon-alarm-mistake-rate.png" alt="误报率" />

          <div class="stat-info">
            <span class="stat-label">误报率</span>
            <span class="stat-value">{{ formatCamera2Rate(statistics.false_alarm_rate) }}</span>
          </div>
        </div>
      </div>

      <div class="alarm-levels">
        <div class="level-item">
          <span class="level-dot high"></span>
          <span class="level-label">高等级</span>
          <span class="level-value">{{ formatCamera2Number(statistics.high_count) }}</span>
        </div>
        <div class="line"></div>
        <div class="level-item">
          <span class="level-dot medium"></span>
          <span class="level-label">中等级</span>
          <span class="level-value">{{ formatCamera2Number(statistics.medium_count) }}</span>
        </div>
        <div class="line"></div>
        <div class="level-item">
          <span class="level-dot low"></span>
          <span class="level-label">低等级</span>
          <span class="level-value">{{ formatCamera2Number(statistics.low_count) }}</span>
        </div>
      </div>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { fetchCamera2Overview, formatCamera2Number, formatCamera2Rate } from '../api'
import Tabs from '../components/Tabs/index.vue'
import { useCamera2Range } from '../hooks/useCamera2Range'

const { activeTab, customRange, tabs, query, handleTabChange, handleCustomRangeConfirm } = useCamera2Range()
const statistics = ref({
  total_alarm_count: 0,
  pending_count: 0,
  handling_rate: 0,
  false_alarm_rate: 0,
  high_count: 0,
  medium_count: 0,
  low_count: 0,
})

const loadStatistics = async () => {
  const response = await fetchCamera2Overview(query.value)
  statistics.value = response.alarm_statistics
}

const handleAlarmRefresh = () => {
  void loadStatistics()
}

watch(query, () => {
  void loadStatistics()
}, { deep: true, immediate: true })

onMounted(() => {
  window.addEventListener('maas-alarm', handleAlarmRefresh)
})

onUnmounted(() => {
  window.removeEventListener('maas-alarm', handleAlarmRefresh)
})
</script>

<style scoped lang="less">
.alarm-statistics {
  width: 100%;
  height: 268px;
  background: url('../assets/images/bg-section-alarm-stat.png') no-repeat center center / cover;
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  .panel-header {
    font-size: 21px;
    font-weight: 800;
    color: #f1f8ff;
    padding: 6px 0 0 30px;
    width: 100%;
    margin-bottom: 10px;
  }


  .panel-content {
    width: 100%;
    height: 0;
    flex-grow: 1;
    padding: 10px 0px 4px 10px;
    display: flex;
    flex-direction: column;
    align-items: flex-end;
  }

  .panel-tools {
    width: 100%;
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding-bottom: 10px;
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

  .stats-grid {
    width: 100%;
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 18px 20px;
    padding: 0 20px;
  }

  .stat-card {
    display: flex;
    align-items: center;
    gap: 14px;
    min-width: 0;
  }

  .diamond {
    width: 58px;
    height: 58px;
    transform: rotate(45deg);
    border: 3px solid currentColor;
    display: flex;
    align-items: center;
    justify-content: center;
    flex: 0 0 auto;
    box-shadow: inset 0 0 0 1px rgba(255, 255, 255, 0.18);

    .diamond-inner {
      width: 26px;
      height: 26px;
      border: 3px solid currentColor;
      border-radius: 6px;
      opacity: 0.9;
      position: relative;
    }

    .warning {
      border-radius: 50%;
    }

    .ok::before,
    .ok::after {
      content: '';
      position: absolute;
      background: currentColor;
      border-radius: 2px;
    }

    .ok::before {
      width: 6px;
      height: 16px;
      left: 10px;
      top: 2px;
      transform: rotate(26deg);
    }

    .ok::after {
      width: 14px;
      height: 6px;
      left: 6px;
      top: 14px;
      transform: rotate(-26deg);
    }

    .shield::before {
      content: '';
      position: absolute;
      inset: 4px;
      border: 2px solid currentColor;
      clip-path: polygon(50% 0, 100% 20%, 84% 100%, 16% 100%, 0 20%);
    }
  }

  .stat-info {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .stat-label {
    font-size: 13px;
    color: #d1dfff;
    opacity: 0.92;
  }

  .stat-value {
    font-size: 20px;
    font-weight: 900;
    font-style: italic;
  }

  .red {
    color: #ff4b57;
  }

  .yellow {
    color: #ffe11b;
  }

  .green {
    color: #1cff88;
  }

  .cyan {
    color: #26bfff;
  }

  .alarm-levels {
    display: flex;
    align-items: center;
    justify-content: center;
    padding-top: 14px;
    width: 100%;
    .line {
      width: 1px;
      height: 20px;
      background-image: linear-gradient(to bottom, transparent,#9cc0e8, transparent);
      margin: 0 20px;
    }
  }

  .level-item {
    flex: 1;
    display: flex;
    justify-content: center;
    align-items: center;
    gap: 6px;
    font-size: 13px;
  }

  .level-dot {
    width: 10px;
    height: 10px;
    border-radius: 50%;
    box-shadow: 0 0 8px currentColor;

    &.high {
      color: #ff4d59;
      background: #ff4d59;
    }

    &.medium {
      color: #ffe11b;
      background: #ffe11b;
    }

    &.low {
      color: #16ff86;
      background: #16ff86;
    }
  }

  .level-label {
    color: #d7e6ff;
  }

  .level-value {
    font-size: 16px;
    font-weight: 800;
  }
}
</style>
