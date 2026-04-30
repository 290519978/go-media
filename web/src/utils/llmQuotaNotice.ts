export type LLMQuotaNotice = {
  notice_id: string
  issued_at: string
  token_total_limit: number
  used_tokens: number
  title?: string
  message?: string
}

export const llmQuotaNoticeTargetPath = '/algorithms/llm-usage'

const llmQuotaNoticeSessionPrefix = 'mb_llm_quota_notice_shown_v1'
const llmQuotaNoticeFallbackTitle = 'LLM 配额提醒'
const llmQuotaNoticeFallbackMessage = 'LLM 总 token 已达到限制，AI 识别已禁用。请前往 LLM 用量统计调整限额配置，解除限制后手动重启任务。'

export function normalizeLLMQuotaNotice(payload: unknown): LLMQuotaNotice | null {
  if (!payload || typeof payload !== 'object') {
    return null
  }
  const raw = payload as Record<string, unknown>
  const noticeID = String(raw.notice_id || '').trim()
  if (!noticeID) {
    return null
  }
  return {
    notice_id: noticeID,
    issued_at: String(raw.issued_at || '').trim(),
    token_total_limit: Number(raw.token_total_limit || 0),
    used_tokens: Number(raw.used_tokens || 0),
    title: String(raw.title || '').trim(),
    message: String(raw.message || '').trim(),
  }
}

export function resolveLLMQuotaNoticeContent(notice: LLMQuotaNotice) {
  return {
    title: String(notice.title || '').trim() || llmQuotaNoticeFallbackTitle,
    description: String(notice.message || '').trim() || llmQuotaNoticeFallbackMessage,
  }
}

export function markLLMQuotaNoticeShown(surface: 'app' | 'camera2', notice: LLMQuotaNotice): boolean {
  const noticeID = String(notice.notice_id || '').trim()
  if (!noticeID) {
    return false
  }
  // 同一登录会话内刷新页面不重复弹，但用户重新登录拿到新 token 后仍然可以再次提醒一次。
  const token = String(localStorage.getItem('mb_token') || '').trim()
  const key = `${llmQuotaNoticeSessionPrefix}|${surface}|${token}|${noticeID}`
  if (sessionStorage.getItem(key) === '1') {
    return false
  }
  sessionStorage.setItem(key, '1')
  return true
}

export function buildAppRouteURL(path: string): string {
  const normalizedPath = String(path || '').trim()
  if (!normalizedPath) {
    return String(import.meta.env.BASE_URL || '/')
  }
  const base = String(import.meta.env.BASE_URL || '/').trim()
  const normalizedBase = base === '/' ? '' : base.replace(/\/$/, '')
  return `${normalizedBase}${normalizedPath}`
}
