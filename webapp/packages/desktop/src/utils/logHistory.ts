interface HistoryTask {
  id: string; actor: string; instance_id: string; agent_id: string; created_at: string
  query: { kind?: string; host?: string; path?: string; level?: string; min_duration_ms?: number; batch_id?: string; from: string; to: string; keyword?: string; request_id?: string; error_code?: string; source_id: string; container: string }
  result: { status: string }
}
export function groupLogHistory<T extends HistoryTask>(tasks: T[]) {
  const groups: { id: string; created_at: string; tasks: T[]; signature: string }[] = []
  for (const task of [...tasks].sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at))) {
    const q = task.query
    const signature = JSON.stringify([task.actor, q.from, q.to, q.keyword || '', q.request_id || '', q.error_code || '', q.kind || 'app', q.host || '', q.path || '', q.level || '', q.min_duration_ms || 0])
    const source = (t: T) => JSON.stringify([t.instance_id, t.agent_id, t.query.source_id, t.query.container])
    const group = groups.find(g => g.signature === signature && (q.batch_id
      ? g.tasks[0].query.batch_id === q.batch_id
      : !g.tasks[0].query.batch_id && Math.abs(Date.parse(g.created_at) - Date.parse(task.created_at)) <= 10000 && !g.tasks.some(t => source(t) === source(task))))
    if (group) group.tasks.push(task)
    else groups.push({ id: task.id, created_at: task.created_at, tasks: [task], signature })
  }
  return groups.map(g => ({ ...g, status: g.tasks.some(t => ['pending', 'running'].includes(t.result.status)) ? '查询中'
    : g.tasks.every(t => t.result.status === 'succeeded') ? '已完成'
    : g.tasks.some(t => t.result.status === 'succeeded') ? '部分失败' : '失败' }))
}
