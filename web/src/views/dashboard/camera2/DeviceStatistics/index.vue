<template>
  <div class="panel device-statistics">
    <div class="panel-header">设备统计</div>

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
      <div class="device-overview">
        <div class="device-item" v-for="item in overviewCards" :key="item.label" :style="item.style">
          <div class="device-meta">
            <span class="device-label">{{ item.label }}</span>
            <span class="device-value">{{ item.value }}</span>
          </div>
        </div>
      </div>

      <div class="device-list-header">
        <img class="header-icon" :src="listHeaderIcon" alt="报警设备" />
        <span>报警设备TOP3</span>
      </div>

      <div class="device-list">
        <div class="list-item" :style="item.rankStyle" v-for="(item, index) in deviceList" :key="item.device_id || item.device_name">
          <div class="item-rank">{{ index < 3 ? '' : (index < 10 ? '0' + (index + 1) : index + 1) }}</div>
          <div class="item-name">{{ item.device_name }}</div>
          <div class="item-location">{{ item.area_name }}</div>
          <div class="item-status">
            <span class="status-dot"></span>
            <span>报警</span>
          </div>
          <div class="item-count">{{ item.alarm_count > 999 ? '999+' : item.alarm_count }}</div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import baojingshebeitop3 from '../assets/baojingshebeitop3.svg'
import bgDeviceTotal from '../assets/images/bg-device-total.png'
import bgDeviceOnline from '../assets/images/bg-device-online.png'
import bgDeviceAlarm from '../assets/images/bg-device-alarm.png'
import bgDeviceOffline from '../assets/images/bg-device-offline.png'
import bgAlarmTop1 from '../assets/images/bg-alarm-top1.png'
import bgAlarmTop2 from '../assets/images/bg-alarm-top2.png'
import bgAlarmTop3 from '../assets/images/bg-alarm-top3.png'
import listHeaderIcon from '../assets/images/icon-title1.png'
import Tabs from '../components/Tabs/index.vue'
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { fetchCamera2Overview, formatCamera2Number, formatCamera2Rate } from '../api'
import { useCamera2Range } from '../hooks/useCamera2Range'

const { activeTab, customRange, tabs, query, handleTabChange, handleCustomRangeConfirm } = useCamera2Range()

const createCardStyle = (image: string) => ({
  backgroundImage: `url(${image})`,
  backgroundRepeat: 'no-repeat',
  backgroundPosition: 'center center',
  backgroundSize: '100% 100%',
})

const createRankStyle = (image: string, color: string) => ({
  backgroundImage: `url(${image})`,
  backgroundRepeat: 'no-repeat',
  backgroundPosition: 'center center',
  backgroundSize: '100% 100%',
  color,
})

const statistics = ref({
  total_devices: 0,
  area_count: 0,
  online_devices: 0,
  online_rate: 0,
  alarm_devices: 0,
  offline_devices: 0,
  top_devices: [] as Array<{
    device_id: string
    device_name: string
    area_id: string
    area_name: string
    alarm_count: number
  }>,
})

const overviewCards = computed(() => ([
  {
    label: '设备总数 / 区域数',
    value: `${formatCamera2Number(statistics.value.total_devices)}/${formatCamera2Number(statistics.value.area_count)}`,
    style: createCardStyle(bgDeviceTotal),
  },
  {
    label: '在线设备',
    value: `${formatCamera2Number(statistics.value.online_devices)} (${formatCamera2Rate(statistics.value.online_rate)})`,
    style: createCardStyle(bgDeviceOnline),
  },
  {
    label: '报警设备',
    value: formatCamera2Number(statistics.value.alarm_devices),
    style: createCardStyle(bgDeviceAlarm),
  },
  {
    label: '离线设备',
    value: formatCamera2Number(statistics.value.offline_devices),
    style: createCardStyle(bgDeviceOffline),
  },
]))

const deviceList = computed(() => statistics.value.top_devices.map((item, index) => ({
  ...item,
  rankStyle: index === 0
    ? createRankStyle(bgAlarmTop1, '#ffe127')
    : index === 1
      ? createRankStyle(bgAlarmTop2, '#24d7ff')
      : index === 2
        ? createRankStyle(bgAlarmTop3, '#5cff7b')
        : null,
})))

const loadStatistics = async () => {
  const response = await fetchCamera2Overview(query.value)
  statistics.value = response.device_statistics
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
.device-statistics {
  width: 100%;
  height: 360px;
  overflow: hidden;
  background: url('../assets/images/bg-section-device.png') no-repeat center center / cover;
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  .panel-header {
    width: 100%;
    font-size: 21px;
    font-weight: 800;
    color: #f1f8ff;
    padding: 6px 0 0 30px;
    margin-bottom: 10px;
  }



  .panel-content {
    width: 100%;
    padding: 8px 16px 16px;
  }

  .panel-tools {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 10px;
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

  .device-overview {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px 14px;
  }

  .device-item {
    height: 47px;
    display: flex;
    align-items: center;
    padding: 0 20px 0 52px;
    min-width: 0;
  }

  .device-meta {
    min-width: 0;
    display: flex;
    align-items: center;
    justify-content: space-between;
    width: 100%;
    gap: 10px;
  }

  .device-label {
    width: 60px;
    font-size: 13px;
    color: #dce9ff;
    white-space: wrap;
  }

  .device-value {
    font-size: 14px;
    font-weight: bold;
    color: #edf7ff;
    white-space: nowrap;
  }

  .device-list-header {
    display: flex;
    align-items: center;
    gap: 6px;
    color: #8fb9e5;
    font-size: 18px;
    line-height: 28px;
    font-weight: bold;
    margin-bottom: 4px;;
  }

  .header-icon {
    width: 20px;
    height: 40px;
    object-fit: contain;
    margin-top: 8px;
  }

  .device-list {
    display: flex;
    flex-direction: column;
    gap: 10px;
    height: 128px;
    overflow-y: auto;

    &::-webkit-scrollbar {
      display: none;
    }
  }

  .list-item {
    display: flex;
    gap: 10px;
    align-items: center;
    height: 36px;
    background: rgba(28, 73, 136, 0.45);
  }

  .item-rank {
    width: 40px;
    height: 36px;
    display: flex;
    align-items: center;
    justify-content: center;
    color: #25EAFF;
    font-size: 16px;
    font-weight: bold;
    font-style: italic;
    text-shadow: 0 0 6px currentColor;
  }

  .item-name {
    width: 100px;
    font-size: 15px;
    color: #f0f7ff;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .item-location {
    flex: 1;
    color: #93b8e2;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .item-status {
    display: flex;
    align-items: center;
    gap: 6px;
    color: #d8e6ff;
  }

  .status-dot {
    width: 9px;
    height: 9px;
    border-radius: 50%;
    background: #ff4a4a;
    box-shadow: 0 0 8px rgba(255, 74, 74, 0.75);
  }

  .item-count {
    width: 50px;
    color: #ff4a4a;
    font-size: 16px;
    font-weight: 900;
  }
}
</style>
