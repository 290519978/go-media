import { onMounted, onUnmounted } from 'vue'
import { normalizeLLMQuotaNotice, type LLMQuotaNotice } from '@/utils/llmQuotaNotice'

type AlarmFeedOptions = {
  onLLMQuotaNotice?: (notice: LLMQuotaNotice) => void
}

export function useAlarmFeed(options: AlarmFeedOptions = {}) {
  let ws: WebSocket | null = null
  let reconnectTimer: number | null = null
  let stopped = false

  function clearReconnectTimer() {
    if (reconnectTimer !== null) {
      window.clearTimeout(reconnectTimer)
      reconnectTimer = null
    }
  }

  function scheduleReconnect() {
    if (stopped || reconnectTimer !== null) {
      return
    }
    reconnectTimer = window.setTimeout(() => {
      reconnectTimer = null
      connect()
    }, 3000)
  }

  function connect() {
    if (stopped) {
      return
    }
    const protocol = window.location.protocol === 'https:' ? 'wss' : 'ws'
    ws = new WebSocket(`${protocol}://${window.location.host}/ws/alerts`)
    ws.onmessage = (event) => {
      try {
        const payload = JSON.parse(event.data)
        if (payload?.type === 'alarm') {
          // camera2 在独立 iframe 内自行分发告警事件，避免依赖主应用壳层的 websocket。
          window.dispatchEvent(new CustomEvent('maas-alarm', { detail: payload }))
        } else if (payload?.type === 'llm_quota_notice') {
          const notice = normalizeLLMQuotaNotice(payload)
          if (notice) {
            options.onLLMQuotaNotice?.(notice)
          }
        }
      } catch {
        // 忽略无效消息，避免单条异常阻断后续告警刷新。
      }
    }
    ws.onclose = () => {
      ws = null
      scheduleReconnect()
    }
    ws.onerror = () => {
      ws?.close()
    }
  }

  onMounted(() => {
    stopped = false
    connect()
  })

  onUnmounted(() => {
    stopped = true
    clearReconnectTimer()
    ws?.close()
    ws = null
  })
}
