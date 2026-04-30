import { computed, ref } from 'vue'
import type { Camera2OverviewQuery, Camera2RangeType } from '../api'

export const camera2RangeTabs = [
  { label: '今日', value: 'today' },
  { label: '7天', value: '7days' },
  { label: '自定义', value: 'custom' },
] as const

export function createDefaultCamera2CustomRange(): [string, string] {
  const now = new Date()
  const startOfDay = new Date(now)
  startOfDay.setHours(0, 0, 0, 0)
  return [String(startOfDay.getTime()), String(now.getTime())]
}

export function useCamera2Range(initialRange: Camera2RangeType = 'today') {
  const activeTab = ref<Camera2RangeType>(initialRange)
  const appliedTab = ref<Camera2RangeType>(initialRange)
  const customRange = ref<[string, string]>(createDefaultCamera2CustomRange())
  const appliedCustomRange = ref<[string, string]>([...customRange.value] as [string, string])

  function normalizeCustomRange(value: unknown): [string, string] | null {
    if (!Array.isArray(value) || value.length !== 2) {
      return null
    }
    const start = String(value[0] || '').trim()
    const end = String(value[1] || '').trim()
    if (!start || !end) {
      return null
    }
    return [start, end]
  }

  function handleTabChange(nextValue: string) {
    activeTab.value = nextValue as Camera2RangeType
    if (activeTab.value === 'custom' && (!customRange.value[0] || !customRange.value[1])) {
      customRange.value = createDefaultCamera2CustomRange()
      return
    }
    if (activeTab.value !== 'custom') {
      appliedTab.value = activeTab.value
    }
  }

  function handleCustomRangeConfirm() {
    const normalized = normalizeCustomRange(customRange.value)
    if (!normalized) {
      return
    }
    customRange.value = normalized
    appliedCustomRange.value = normalized
    appliedTab.value = 'custom'
  }

  const query = computed<Camera2OverviewQuery>(() => {
    if (appliedTab.value === 'custom') {
      return {
        range: 'custom',
        start_at: appliedCustomRange.value[0],
        end_at: appliedCustomRange.value[1],
      }
    }
    return {
      range: appliedTab.value,
    }
  })

  return {
    activeTab,
    customRange,
    tabs: camera2RangeTabs,
    query,
    handleTabChange,
    handleCustomRangeConfirm,
  }
}
