// Only explicit code fields are interpreted; arbitrary numbers in messages are not status codes.
export function logErrorCode(row: { type: number; other: string; content?: string; content_summary: string }): string {
  if (row.type !== 5) return ''
  const scalar = (value: unknown) => typeof value === 'number' && Number.isFinite(value)
    ? String(value) : typeof value === 'string' && /^[\w.-]{1,80}$/.test(value.trim()) ? value.trim() : ''
  const read = (value: unknown): string => {
    if (!value || typeof value !== 'object' || Array.isArray(value)) return ''
    const data = value as Record<string, unknown>
    for (const key of ['status_code', 'http_status_code', 'error_code', 'code']) {
      const code = scalar(data[key])
      if (code) return code
    }
    return data.error && typeof data.error === 'object' ? read(data.error) : ''
  }
  for (const raw of [row.other, row.content, row.content_summary]) {
    if (!raw) continue
    try { const code = read(JSON.parse(raw)); if (code) return code } catch { /* Plain text is handled below. */ }
    const match = raw.match(/\b(?:status[_ ]code|http[_ ]status(?:[_ ]code)?|error_code|code)\s*[=:：]\s*["']?([\w.-]{1,80})\b/i)
    if (match) return match[1]
  }
  return ''
}
