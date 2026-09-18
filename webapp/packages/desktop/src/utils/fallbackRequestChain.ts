import type { ReadonlyLog, ReadonlyResponse } from '@ct/shared'

export type ChainQuery = { site: string; request_id: string; user_ids: string; start_time: string; end_time: string }
export type ChainStep = { channel: string; row?: ReadonlyLog; position?: number }
const day = 86400000

// Request lookup is independent of the main list filters, including its time boundary.
// Keep a bounded window for the existing read-only API, and expose it in the UI.
export function chainQuery(site: string, row: ReadonlyLog): ChainQuery | undefined {
  const time = Date.parse(row.created_at)
  if (!site || !row.request_id?.trim() || !Number.isFinite(time) || !Number.isSafeInteger(row.user_id) || row.user_id <= 0) return
  const lookback = Math.min(30 * day, Math.max(day, (Number(row.use_time) || 0) * 1000 + day))
  return { site, request_id: row.request_id.trim(), user_ids: String(row.user_id), start_time: new Date(time - lookback).toISOString(), end_time: new Date(time + day).toISOString() }
}

function channels(value: unknown): string[] {
  const values = Array.isArray(value) ? value : typeof value === 'string' ? value.split(/\s*(?:->|→|,|\s)\s*/) : []
  // Repeated channels are separate attempts: never deduplicate the recorded sequence.
  return values.map(v => String(v ?? '').trim()).filter(v => /^\d+$/.test(v))
}
export function attemptChannels(row: ReadonlyLog): string[] {
  let other: Record<string, unknown> = {}
  try { other = JSON.parse(row.other || '{}') || {} } catch { /* old or malformed log */ }
  const admin = other.admin_info && typeof other.admin_info === 'object' ? other.admin_info as Record<string, unknown> : {}
  // use_channel is the original sequence; normalized fallback_channels can lose repetitions.
  for (const value of [admin.use_channel, other.use_channel, admin.fallback_channels, other.fallback_channels, row.fallback_channels]) {
    const result = channels(value)
    if (result.length) return result
  }
  return []
}
const chronological = (a: ReadonlyLog, b: ReadonlyLog) => Date.parse(a.created_at) - Date.parse(b.created_at) || a.id - b.id
const channel = (row: ReadonlyLog) => String(row.channel_id || row.channel || '')
const prefixOf = (prefix: string[], full: string[]) => prefix.every((value, index) => value === full[index])

export function buildRequestChain(rows: ReadonlyLog[], selected: ReadonlyLog) {
  const matched = rows.filter(row => row.request_id === selected.request_id && row.user_id === selected.user_id)
  const attempts = matched.filter(row => row.type === 2 || row.type === 5).sort(chronological)
  const related = matched.filter(row => row.type !== 2 && row.type !== 5).sort(chronological)
  const sequences = attempts.map(attemptChannels).filter(path => path.length)
  const path = sequences.sort((a, b) => b.length - a.length)[0] || []
  const compatible = path.length > 1 && sequences.every(seq => prefixOf(seq, path))
  if (!compatible) return { steps: attempts.map(row => ({ row, channel: channel(row) } as ChainStep)), related, ordered: false, missing: 0 }

  const steps: ChainStep[] = path.map((id, index) => ({ channel: id, position: index + 1 }))
  const unplaced: ChainStep[] = []
  for (const row of attempts) {
    const seq = attemptChannels(row)
    // A sequence that ends at this log's channel records the exact attempt position.
    let index = seq.length && seq[seq.length - 1] === channel(row) ? seq.length - 1 : -1
    if (index < 0) {
      const matches = path.map((id, i) => id === channel(row) ? i : -1).filter(i => i >= 0)
      if (matches.length === 1) index = matches[0]
    }
    if (index < 0 || steps[index].row) unplaced.push({ row, channel: channel(row) })
    else steps[index].row = row
  }
  // Ambiguous logs stay visible, separate from the confirmed sequence.
  return { steps, related, unplaced, ordered: true, missing: steps.filter(step => !step.row).length }
}

export async function loadRequestChain(
  query: ChainQuery,
  fetchPage: (params: ChainQuery & { limit: number; offset: number }, signal: AbortSignal) => Promise<ReadonlyResponse<ReadonlyLog>>,
  signal: AbortSignal,
) {
  const rows = new Map<number, ReadonlyLog>()
  const limit = 100
  for (let offset = 0; offset < 1000; offset += limit) {
    signal.throwIfAborted()
    const result = await fetchPage({ ...query, limit, offset }, signal)
    signal.throwIfAborted()
    if (!result.configured) throw new Error('当前站点尚未配置只读日志数据库')
    for (const row of result.items) {
      if (row.request_id === query.request_id && String(row.user_id) === query.user_ids) rows.set(row.id, row)
    }
    if (result.has_more === false || (result.has_more === undefined && result.items.length < limit)) return { rows: [...rows.values()], truncated: false }
    if (!result.items.length) throw new Error('关联查询返回了不完整的分页结果，请重试')
  }
  return { rows: [...rows.values()], truncated: true }
}

// An explicit false comes from a failed supplemental lookup. Older servers that
// omit the field retain their existing display behavior.
export function retryLookupUnknown(row: ReadonlyLog): boolean {
  return row.fallback_checked === false && row.fallback !== true
    && Boolean(row.request_id) && row.user_id > 0 && attemptChannels(row).length <= 1
}
