type Task = { id: string; result: { status: string; lines: string[]; error?: string } }
export function mergeLogTaskRefresh<T extends Task>(current: T[], requested: T[], updates: PromiseSettledResult<T>[]): { tasks: T[]; warning: string } {
  const replacements = new Map<string, T>()
  let retry = false
  let missing = false
  updates.forEach((update, index) => {
    const previous = requested[index]
    if (!previous) return
    if (update.status === 'fulfilled') replacements.set(previous.id, update.value)
    else if (update.reason?.status === 404) {
      missing = true
      replacements.set(previous.id, { ...previous, result: { ...previous.result, status: 'failed', error: '任务已过期或不可访问，请重新查询' } })
    } else retry = true
  })
  return {
    tasks: current.map(t => replacements.get(t.id) || t),
    warning: retry ? '部分进度读取失败，正在自动重试；已保留现有结果' : missing ? '部分任务已过期或不可访问，请重新查询' : '',
  }
}
