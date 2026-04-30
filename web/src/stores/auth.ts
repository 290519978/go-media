import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { authAPI, type LoginPayload } from '@/api/modules'
import { normalizeLLMQuotaNotice, type LLMQuotaNotice } from '@/utils/llmQuotaNotice'

type MenuItem = {
  id: string
  name: string
  path: string
  menu_type: string
  view_path: string
  icon: string
  parent_id: string
  sort: number
}

export type CleanupNotice = {
  notice_kind?: 'soft_pressure' | 'retention_risk' | string
  notice_id: string
  issued_at: string
  execute_after?: string
  event_snapshot_count: number
  alarm_clip_count: number
  hard_reached?: boolean
  used_percent?: number
  free_gb?: number
  soft_watermark?: number
  title?: string
  message?: string
}

export const useAuthStore = defineStore('auth', () => {
  const token = ref<string>(localStorage.getItem('mb_token') || '')
  const username = ref<string>(localStorage.getItem('mb_username') || '')
  const roles = ref<string[]>(JSON.parse(localStorage.getItem('mb_roles') || '[]'))
  const menus = ref<MenuItem[]>(JSON.parse(localStorage.getItem('mb_menus') || '[]'))
  const developmentMode = ref<boolean>(localStorage.getItem('mb_development_mode') === '1')
  const cleanupNotices = ref<CleanupNotice[]>([])
  const cleanupNotice = ref<CleanupNotice | null>(null)
  const llmQuotaNotice = ref<LLMQuotaNotice | null>(null)

  const isLoggedIn = computed(() => Boolean(token.value))

  function persist() {
    localStorage.setItem('mb_token', token.value)
    localStorage.setItem('mb_username', username.value)
    localStorage.setItem('mb_roles', JSON.stringify(roles.value))
    localStorage.setItem('mb_menus', JSON.stringify(menus.value))
    localStorage.setItem('mb_development_mode', developmentMode.value ? '1' : '0')
  }

  async function login(payload: LoginPayload) {
    const data = await authAPI.login(payload) as { token: string; username: string; roles: string[] }
    token.value = data.token
    username.value = data.username
    roles.value = data.roles || []
    persist()
    await refreshMe()
  }

  async function refreshMe() {
    const data = await authAPI.me() as {
      username: string
      roles: string[]
      menus: MenuItem[]
      development_mode?: boolean
      cleanup_notices?: CleanupNotice[] | null
      cleanup_notice?: CleanupNotice | null
      llm_quota_notice?: LLMQuotaNotice | null
    }
    username.value = data.username || username.value
    roles.value = data.roles || roles.value
    menus.value = data.menus || []
    developmentMode.value = data.development_mode === true
    const notices = Array.isArray(data.cleanup_notices)
      ? data.cleanup_notices.filter((item): item is CleanupNotice => !!item && typeof item === 'object')
      : []
    if (notices.length > 0) {
      cleanupNotices.value = notices
      cleanupNotice.value = notices[0]
    } else {
      cleanupNotice.value = data.cleanup_notice || null
      cleanupNotices.value = cleanupNotice.value ? [cleanupNotice.value] : []
    }
    llmQuotaNotice.value = normalizeLLMQuotaNotice(data.llm_quota_notice)
    persist()
  }

  function logout() {
    token.value = ''
    username.value = ''
    roles.value = []
    menus.value = []
    developmentMode.value = false
    cleanupNotices.value = []
    cleanupNotice.value = null
    llmQuotaNotice.value = null
    persist()
  }

  return {
    token,
    username,
    roles,
    menus,
    developmentMode,
    cleanupNotices,
    cleanupNotice,
    llmQuotaNotice,
    isLoggedIn,
    login,
    refreshMe,
    logout,
  }
})
