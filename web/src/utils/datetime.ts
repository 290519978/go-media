function pad2(value: number): string {
  return String(value).padStart(2, '0')
}

function formatLocalDate(date: Date): string {
  return [
    date.getFullYear(),
    pad2(date.getMonth() + 1),
    pad2(date.getDate()),
  ].join('-') + ` ${pad2(date.getHours())}:${pad2(date.getMinutes())}:${pad2(date.getSeconds())}`
}

function parseNumericTimestamp(raw: string): Date | null {
  if (!/^-?\d+$/.test(raw)) return null
  const num = Number(raw)
  if (!Number.isFinite(num)) return null
  if (raw.length >= 13) return new Date(num)
  if (raw.length >= 10) return new Date(num * 1000)
  return null
}

function parseDateTimeString(raw: string): Date | null {
  const normalized = raw.trim()
  if (!normalized) return null

  const numeric = parseNumericTimestamp(normalized)
  if (numeric && !Number.isNaN(numeric.getTime())) {
    return numeric
  }

  const parsed = new Date(normalized)
  if (!Number.isNaN(parsed.getTime())) {
    return parsed
  }

  const localLike = normalized.match(
    /^(\d{4})-(\d{2})-(\d{2})[ T](\d{2}):(\d{2})(?::(\d{2}))?(?:\.\d+)?$/,
  )
  if (!localLike) return null

  const [, year, month, day, hour, minute, second] = localLike
  const localDate = new Date(
    Number(year),
    Number(month) - 1,
    Number(day),
    Number(hour),
    Number(minute),
    Number(second || '0'),
  )
  if (Number.isNaN(localDate.getTime())) return null
  return localDate
}

export function formatDateTime(value: unknown, fallback = '-'): string {
  if (value === null || value === undefined) return fallback

  const raw = String(value).trim()
  if (!raw) return fallback

  const parsed = parseDateTimeString(raw)
  if (!parsed) return raw
  return formatLocalDate(parsed)
}
