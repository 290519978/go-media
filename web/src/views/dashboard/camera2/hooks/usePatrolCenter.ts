import { ref } from 'vue'
import { message } from 'ant-design-vue'

import { createCamera2PatrolJob, fetchCamera2PatrolJob, type Camera2PatrolCreatePayload, type Camera2PatrolJobSnapshot } from '../api'

export type Camera2WarningTab = 'real' | 'patrol'

const currentWarningTab = ref<Camera2WarningTab>('real')
const patrolSubmitting = ref(false)
const activePatrolJobID = ref('')
let patrolPollTimer: number | null = null

function clearPatrolPollTimer() {
  if (patrolPollTimer !== null) {
    window.clearTimeout(patrolPollTimer)
    patrolPollTimer = null
  }
}

function dispatchPatrolRefresh(snapshot?: Camera2PatrolJobSnapshot) {
  window.dispatchEvent(new CustomEvent('maas-patrol-refresh', { detail: snapshot }))
}

function summarizePatrolResult(snapshot: Camera2PatrolJobSnapshot) {
  return `命中 ${Number(snapshot.alarm_count || 0)} 路，失败 ${Number(snapshot.failed_count || 0)} 路`
}

async function pollPatrolJob(jobID: string) {
  try {
    const snapshot = await fetchCamera2PatrolJob(jobID)
    dispatchPatrolRefresh(snapshot)
    if (snapshot.status === 'pending' || snapshot.status === 'running') {
      clearPatrolPollTimer()
      patrolPollTimer = window.setTimeout(() => {
        void pollPatrolJob(jobID)
      }, 1500)
      return
    }
    patrolSubmitting.value = false
    activePatrolJobID.value = ''
    clearPatrolPollTimer()
    if (snapshot.status === 'failed' || snapshot.status === 'partial_failed') {
      message.warning(`任务巡查已结束，${summarizePatrolResult(snapshot)}`)
      return
    }
    message.success(`任务巡查完成，${summarizePatrolResult(snapshot)}`)
  } catch (error) {
    patrolSubmitting.value = false
    activePatrolJobID.value = ''
    clearPatrolPollTimer()
    message.error((error as Error).message || '获取任务巡查结果失败')
  }
}

export function usePatrolCenter() {
  const setWarningTab = (tab: Camera2WarningTab) => {
    currentWarningTab.value = tab
  }

  const startPatrol = async (payload: Camera2PatrolCreatePayload) => {
    if (patrolSubmitting.value) {
      message.warning('任务巡查进行中，请稍候')
      return
    }
    patrolSubmitting.value = true
    try {
      const response = await createCamera2PatrolJob(payload)
      activePatrolJobID.value = String(response.job_id || '')
      currentWarningTab.value = 'patrol'
      dispatchPatrolRefresh()
      message.success('任务巡查已创建')
      if (activePatrolJobID.value) {
        void pollPatrolJob(activePatrolJobID.value)
      } else {
        patrolSubmitting.value = false
      }
    } catch (error) {
      patrolSubmitting.value = false
      activePatrolJobID.value = ''
      clearPatrolPollTimer()
      throw error
    }
  }

  return {
    currentWarningTab,
    patrolSubmitting,
    activePatrolJobID,
    setWarningTab,
    startPatrol,
  }
}
