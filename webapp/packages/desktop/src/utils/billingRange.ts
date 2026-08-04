// Shared helpers for the billing generation range pickers (user & channel billing).
export function formatDateTime(value: Date) {
  const y = value.getFullYear(), m = String(value.getMonth() + 1).padStart(2, "0"), d = String(value.getDate()).padStart(2, "0")
  const h = String(value.getHours()).padStart(2, "0"), n = String(value.getMinutes()).padStart(2, "0"), s = String(value.getSeconds()).padStart(2, "0")
  return `${y}-${m}-${d} ${h}:${n}:${s}`
}

export function parseLocal(value: string) { return new Date(value.replace(" ", "T")) }

export function defaultGenerationRange(): [string, string] {
  const now = new Date()
  return [formatDateTime(new Date(now.getFullYear(), now.getMonth(), 1)), formatDateTime(now)]
}

export function savedGenerationRange(key: string): [string, string] {
  try {
    const value = JSON.parse(localStorage.getItem(key) || "")
    if (Array.isArray(value) && value.length === 2 && value.every((v) => typeof v === "string" && v.length === 19)) return [value[0], value[1]]
  } catch { /* fall through to default */ }
  return defaultGenerationRange()
}

export const timeRangeShortcuts = [
  { text: "今天", value: () => { const now = new Date(); return [new Date(now.getFullYear(), now.getMonth(), now.getDate()), now] } },
  { text: "近 7 天", value: () => { const now = new Date(), start = new Date(now); start.setDate(start.getDate() - 6); start.setHours(0, 0, 0, 0); return [start, now] } },
  { text: "本周", value: () => { const now = new Date(), start = new Date(now); start.setDate(start.getDate() - ((start.getDay() + 6) % 7)); start.setHours(0, 0, 0, 0); return [start, now] } },
  { text: "近 30 天", value: () => { const now = new Date(), start = new Date(now); start.setDate(start.getDate() - 29); start.setHours(0, 0, 0, 0); return [start, now] } },
  { text: "本月", value: () => { const now = new Date(); return [new Date(now.getFullYear(), now.getMonth(), 1), now] } },
]

// Returns an error message, or "" when the range is valid for bill generation.
export function validateGenerationRange(range: [string, string] | undefined) {
  const [from, to] = range || []
  if (!from || !to) return "请选择生成账单的开始和结束时间"
  const start = parseLocal(from), end = parseLocal(to), now = new Date()
  if (start > now || end > now) return "不能生成未来时间的账单"
  if (end < start) return "结束时间不能早于开始时间"
  if (end.getTime() - start.getTime() >= 60 * 86400000) return "单次最多生成 60 天账单"
  return ""
}
