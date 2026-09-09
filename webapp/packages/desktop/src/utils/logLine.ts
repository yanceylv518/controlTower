// Split presentation only; joining these parts always reproduces the raw log.
export function splitLogLine(line: string): { prefix: string; body: string } {
  const header = /^[^\r\n]*?\[(?:INFO|ERR|ERROR|WARN|WARNING|DEBUG|TRACE|SYS|FATAL)\][ \t]+\d{4}\/\d{2}\/\d{2}[ \t]+-[ \t]+\d{2}:\d{2}:\d{2}[ \t]*\|[^|\r\n]*\|[ \t]*/.exec(line)
  const prefix = header?.[0] || ''
  return { prefix, body: line.slice(prefix.length) }
}
