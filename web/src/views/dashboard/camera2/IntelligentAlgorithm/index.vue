<template>
  <div class="panel intelligent-algorithm">
    <div class="panel-header">智能算法</div>

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
      <div class="algorithm-stats">
        <div class="algo-stat" v-for="item in statCards" :key="item.label">
          <div class="algo-icon-wrap">
            <img class="algo-icon" :src="item.icon" :alt="item.label" />
          </div>
          <div class="algo-info">
            <span class="algo-value">{{ item.value }}</span>
            <span class="algo-label">{{ item.label }}</span>
          </div>
        </div>
      </div>

      <div class="algorithm-list">
        <div class="list-header">
          <span>算法名称</span>
          <span>报警数</span>
          <span>准确率</span>
        </div>
        <div class="list-item" v-for="item in algorithmList" :key="item.algorithm_id">
          <span class="name">{{ item.algorithm_name }}</span>
          <span class="count">{{ formatCamera2Number(item.alarm_count) }}</span>
          <div class="accuracy-bar">
            <div class="bar-bg">
              <div class="bar-fill" :style="{ width: `${item.accuracy}%` }"></div>
            </div>
            <span class="accuracy-value">{{ formatCamera2Rate(item.accuracy) }}</span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import iconDeploy from '../assets/images/icon-deploy.png'
import iconInProgress from '../assets/images/icon-in-progress.png'
import iconAccuracy from '../assets/images/icon-accuracy.png'
import iconTodayCall from '../assets/images/icon-today-call.png'

import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import Tabs from '../components/Tabs/index.vue'
import { fetchCamera2Overview, formatCamera2Number, formatCamera2Rate } from '../api'
import { useCamera2Range } from '../hooks/useCamera2Range'

const { activeTab, customRange, tabs, query, handleTabChange, handleCustomRangeConfirm } = useCamera2Range()
const statistics = ref({
  deploy_total: 0,
  running_total: 0,
  average_accuracy: 0,
  today_call_count: 0,
  items: [] as Array<{
    algorithm_id: string
    algorithm_name: string
    alarm_count: number
    accuracy: number
  }>,
})

const loadStatistics = async () => {
  const response = await fetchCamera2Overview(query.value)
  statistics.value = response.algorithm_statistics
}

const statCards = computed(() => ([
  { value: formatCamera2Number(statistics.value.deploy_total), label: '部署总数', icon: iconDeploy },
  { value: formatCamera2Number(statistics.value.running_total), label: '进行中', icon: iconInProgress },
  { value: formatCamera2Rate(statistics.value.average_accuracy), label: '平均准确率', icon: iconAccuracy },
  { value: formatCamera2Number(statistics.value.today_call_count), label: '今日调用', icon: iconTodayCall },
]))

const algorithmList = computed(() => statistics.value.items)

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
.intelligent-algorithm {
  width: 100%;
  height: 318px;
  overflow: hidden;
  background: url('../assets/images/bg-section-algorithm.png') no-repeat center center / cover;
  display: flex;
  flex-direction: column;
  .panel-header {
    font-size: 21px;
    font-weight: 800;
    color: #f1f8ff;
    padding: 6px 0 0 30px;
    margin-bottom: 5px;
  }


  .panel-content {
    width: 100%;
    height: 0;
    flex-grow: 1;
    padding: 14px 10px 14px;
    display: flex;
    flex-direction: column;
    align-items: flex-end;
  }

  .panel-tools {
    width: 100%;
    display: flex;
    align-items: center;
    justify-content: space-between;
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

  .algorithm-stats {
    display: grid;
    width: 100%;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 10px;
    margin-bottom: 10px;
    margin-top: 5px;
  }

  .algo-stat {
    display: flex;
    align-items: center;
    gap: 10px;
    min-width: 0;
    height: 44px;
    padding: 8px 12px;
    background: linear-gradient(180deg, rgba(44, 115, 214, 0.24) 0%, rgba(16, 48, 98, 0.52) 100%);
    border: 1px solid rgba(101, 178, 255, 0.32);
    box-shadow: inset 0 0 18px rgba(93, 169, 255, 0.08);
    border-radius: 4px;
  }

  .algo-icon-wrap {
    width: 40px;
    height: 40px;
    flex: 0 0 auto;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .algo-icon {
    max-width: 38px;
    max-height: 33px;
    object-fit: contain;
    filter: drop-shadow(0 0 8px rgba(84, 216, 255, 0.18));
  }

  .algo-info {
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 1px;
  }

  .algo-value {
    font-size: 18px;
    font-weight: 800;
    line-height: 1.1;
    color: #f5fbff;
  }

  .algo-label {
    font-size: 13px;
    line-height: 1.1;
    color: #9bc2ea;
  }

  .algorithm-list {
    width: 100%;
    padding-top: 6px;
    border-top: 1px solid rgba(58, 145, 232, 0.22);
    height: 128px;
    overflow-y: auto;

    &::-webkit-scrollbar {
      display: none;
    }
  }

  .list-header,
  .list-item {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 56px minmax(0, 164px);
    gap: 12px;
    align-items: center;
  }

  .list-header {
    padding-bottom: 8px;
    font-size: 14px;
    color: #84a8ce;
  }

  .list-item {
    padding: 8px 0;
    border-top: 1px solid rgba(255, 255, 255, 0.05);
    font-size: 13px;
  }

  .name {
    color: #e9f4ff;
  }

  .count {
    color: #ffb534;
    font-weight: 800;
  }

  .accuracy-bar {
    display: flex;
    align-items: center;
    gap: 10px;
  }

  .bar-bg {
    flex: 1;
    height: 8px;
    background: rgba(20, 76, 145, 0.5);
    border-radius: 0;
    overflow: hidden;
  }

  .bar-fill {
    height: 100%;
    background: linear-gradient(90deg, #14588d 0%, #29d0ff 100%);
    box-shadow: 0 0 10px rgba(41, 208, 255, 0.45);
  }

  .accuracy-value {
    width: 42px;
    text-align: right;
    color: #dff3ff;
  }
}
</style>
