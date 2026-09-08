// Wall-clock <-> instant conversion for an IANA time zone, independent of the
// browser's own zone. Container logs are stamped in the log source's zone, so
// the query picker and history must speak that zone, not the viewer's.

const parts = (date: Date, tz: string) => {
  const out: Record<string, number> = {}
  for (const p of new Intl.DateTimeFormat('en-US', { timeZone: tz, hour12: false, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' }).formatToParts(date)) {
    if (p.type !== 'literal') out[p.type] = Number(p.value)
  }
  if (out.hour === 24) out.hour = 0
  return out
}

/** Offset of `tz` from UTC at `date`, in minutes (east positive). */
export function zoneOffsetMinutes(date: Date, tz: string): number {
  const p = parts(date, tz)
  return (Date.UTC(p.year, p.month - 1, p.day, p.hour, p.minute, p.second) - Math.floor(date.getTime() / 1000) * 1000) / 60000
}

export function isValidZone(tz: string): boolean {
  try { new Intl.DateTimeFormat('en-US', { timeZone: tz }); return true } catch { return false }
}

export type WallTimeResult = { date: Date; error?: never } | { date?: never; error: 'invalid' | 'nonexistent' | 'ambiguous' }

/** Reject invalid dates, DST gaps and repeated wall times rather than guessing. */
export function parseWallTime(wall: string, tz: string): WallTimeResult {
  const m = /^(\d{4})-(\d{2})-(\d{2})[ T](\d{2}):(\d{2})(?::(\d{2}))?$/.exec(wall.trim())
  if (!m || !isValidZone(tz)) return { error: 'invalid' }
  const asUTC = Date.UTC(+m[1], +m[2] - 1, +m[3], +m[4], +m[5], +(m[6] || 0))
  const normalized = `${m[1]}-${m[2]}-${m[3]} ${m[4]}:${m[5]}:${m[6] || '00'}`
  if (dateToWall(new Date(asUTC), 'UTC') !== normalized) return { error: 'invalid' }
  // Gather offsets on both sides of a nearby transition (including half-hour
  // DST and date-line changes), then verify each candidate by round-trip.
  const offsets = new Set<number>()
  for (let hours = -48; hours <= 48; hours += 6) offsets.add(zoneOffsetMinutes(new Date(asUTC + hours * 3600000), tz))
  const candidates = [...offsets].map(offset => new Date(asUTC - offset * 60000)).filter(date => dateToWall(date, tz) === normalized)
  if (!candidates.length) return { error: 'nonexistent' }
  if (candidates.length > 1) return { error: 'ambiguous' }
  return { date: candidates[0] }
}

export function wallToDate(wall: string, tz: string): Date | null {
  return parseWallTime(wall, tz).date || null
}

/** Change the displayed zone only if both endpoints retain their exact instant. */
export function rezoneRange(range: [string, string], fromZone: string, toZone: string): [string, string] | null {
  const from = wallToDate(range[0], fromZone), to = wallToDate(range[1], fromZone)
  if (!from || !to || !isValidZone(toZone)) return null
  const converted: [string, string] = [dateToWall(from, toZone), dateToWall(to, toZone)]
  if (wallToDate(converted[0], toZone)?.getTime() !== from.getTime() || wallToDate(converted[1], toZone)?.getTime() !== to.getTime()) return null
  return converted
}

const two = (n: number) => String(n).padStart(2, '0')

/** Instant -> `YYYY-MM-DD HH:mm:ss` wall time in `tz`. */
export function dateToWall(date: Date, tz: string): string {
  const p = parts(date, tz)
  return `${p.year}-${two(p.month)}-${two(p.day)} ${two(p.hour)}:${two(p.minute)}:${two(p.second)}`
}

/** Human label such as `Asia/Shanghai (UTC+08:00)`. */
export function zoneLabel(tz: string, at = new Date()): string {
  const off = zoneOffsetMinutes(at, tz)
  const sign = off < 0 ? '-' : '+'
  const abs = Math.abs(off)
  return `${tz} (UTC${sign}${two(Math.floor(abs / 60))}:${two(abs % 60)})`
}
