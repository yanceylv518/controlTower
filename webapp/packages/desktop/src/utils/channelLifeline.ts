import type { ChannelLifeEvent, ProbeChannelStats, ProbeOverview } from '@ct/shared'

export const channelProbeFilters = {all:'全部渠道',abnormal:'有异常',error:'有渠道错误',slow:'有慢请求',skipped:'有漏测',unconfirmed:'有待确认结果',unknown:'有控制异常',pending:'有执行中探针',empty:'无探针记录'}
export const channelProbeSorts = {channel:'渠道 ID',errors:'错误最多',slow:'慢请求最多',skipped:'漏测最多',success_rate:'成功率最低',mean_seconds:'平均耗时最长',latest:'最近探测优先'}
export type ChannelProbeFilter = keyof typeof channelProbeFilters
export type ChannelProbeSort = keyof typeof channelProbeSorts
export function filterProbeChannels(rows: ProbeChannelStats[], filter: ChannelProbeFilter, sort: ChannelProbeSort): ProbeChannelStats[] {
  const matches = (row: ProbeChannelStats) => {
    switch(filter) {
      case 'abnormal': return [row.errors,row.slow,row.skipped,row.unconfirmed,row.unknown].some(n=>n>0)
      case 'error': return row.errors>0
      case 'slow': return row.slow>0
      case 'skipped': return row.skipped>0
      case 'unconfirmed': return row.unconfirmed>0
      case 'unknown': return row.unknown>0
      case 'pending': return row.pending>0
      case 'empty': return row.due===0
      default: return true
    }
  }
  const value = (row: ProbeChannelStats): number|null => {
    if(sort==='channel')return row.channel_id
    if(sort==='latest')return row.last_started_at ? Date.parse(row.last_started_at) : null
    return row[sort] ?? null
  }
  return rows.filter(matches).sort((a,b)=>{
    const av=value(a),bv=value(b), aMissing=av==null||!Number.isFinite(av),bMissing=bv==null||!Number.isFinite(bv)
    if(aMissing!==bMissing)return aMissing?1:-1
    const difference=aMissing||bMissing ? 0 : (av! - bv!)*(sort==='channel'||sort==='success_rate'?1:-1)
    return difference || a.channel_id-b.channel_id || a.model_name.localeCompare(b.model_name)
  })
}

export const probeKinds: Record<string,string> = {scheduled:'定时探针',accelerated:'错误后加速',recovery:'CT 恢复探针'}
export const probeOutcomes: Record<string,string> = {success:'成功',slow:'慢请求',error:'渠道错误',unknown:'控制异常',canceled:'已取消',pending:'执行中',unconfirmed:'结果待确认',skipped:'漏测',succeeded:'执行成功',failed:'执行失败',expired:'已过期',delivered:'已下发',recorded:'已记录',auto_executed:'已关联执行',observed:'观察记录',submitted:'已提交'}
export const probeReasons: Record<string,string> = {global_concurrency:'全局并发占满',channel_concurrency:'单渠道并发占满',pending_evidence:'等待结果容量占满',slow_threshold:'达到慢请求阈值，后续返回不改变判定',channel_error:'上游明确返回渠道错误',control_unavailable:'CT 到控制接口的请求异常，未归因为渠道错误',scheduler_canceled:'调度停止或渠道状态变化，请求被取消',upstream_request_failed:'调用失败，上游是否已生效需核对',upstream_timeout:'调用超时，上游是否已生效需核对',request_canceled:'调用取消，上游是否已生效需核对',channel_not_found:'上游未找到该渠道',command_reported_error:'命令回执包含错误，详见关联操作审计',upstream_rejected:'上游明确拒绝写入',status_confirmed:'已回读确认目标状态'}
export const ratio = (v?: number|null) => v == null ? '—' : `${(v*100).toFixed(1)}%`
export const seconds = (v?: number|null) => v == null ? '—' : `${v.toFixed(2)}s`
export const outcomeTone = (v: string) => ['success','succeeded'].includes(v)?'good':['slow','error','failed'].includes(v)?'bad':['skipped','unknown','unconfirmed','expired'].includes(v)?'warn':'neutral'
const object = (v: unknown): Record<string,unknown> => typeof v === 'object' && v !== null ? v as Record<string,unknown> : {}
export function lifeTitle(v: ChannelLifeEvent): string {
  if(v.source==='probe') return probeKinds[v.operation.replace('probe.','')] || '探针'
  const target=object(v.details.target)
  if(v.source==='decision') return ({routine_probe_circuit:'探针触发熔断',circuit_opened:'进入熔断',circuit_disabled:'禁用渠道决策',circuit_recovered:'恢复渠道决策',probe_started:'启动恢复探测',probe_failed:'恢复探测未通过',weight_write:'调权决策',weight_observed:'观察权重',capacity_reduce:'容量保护降权',manual_takeover:'检测到外部接管',write_abandoned:'停止重试写入'} as Record<string,string>)[v.operation] || v.operation
  if(v.operation==='channel.schedule_open')return '定时开启渠道'
  if(v.operation==='channel.schedule_close')return '定时关闭渠道'
  if(v.operation==='channel.probe')return '恢复检测批次'
  if(v.operation==='channel.verify')return '控制链路验证'
  if(v.operation==='channel.manual_disable')return '手动关闭渠道'
  if(v.operation==='channel.manual_enable')return '手动开启渠道'
  if(target.status===2)return '禁用渠道'
  if(target.status===1)return '启用渠道'
  if(target.group!=null)return '修改渠道分组'
  if(target.priority!=null)return '修改渠道优先级'
  if(target.weight!=null)return '调整渠道权重'
  return v.operation
}
export function lifeChanges(v: ChannelLifeEvent): string[] {
  const before=object(v.details.before), target=object(v.details.target)
  const labels:Record<string,string>={weight:'权重',priority:'优先级',status:'状态',group:'分组',model:'模型',count:'探测次数'}
  const result=Object.keys(labels).filter(k=>target[k]!=null).map(k=>`${labels[k]}：${before[k]!=null?`${before[k]} → `:''}${target[k]}`)
  if(v.details.duration_seconds!=null)result.push(`耗时：${seconds(Number(v.details.duration_seconds))}`)
  if(v.details.model)result.push(`模型：${v.details.model}`)
  if(v.details.rule)result.push(`规则：${v.details.rule}`)
  if(v.details.mode)result.push(`模式：${v.details.mode}`)
  if(v.details.reason)result.push(probeReasons[String(v.details.reason)]||String(v.details.reason))
  return result
}
export function probeTrend(data?: ProbeOverview) {
  if(!data)return []
  const step=data.bucket_seconds*1000, map=new Map(data.trend.map(p=>[Date.parse(p.at),p]))
  const points=[]
  for(let at=Math.floor(Date.parse(data.from)/step)*step;at<Date.parse(data.to);at+=step)points.push({at:new Date(at).toISOString(),value:map.get(at)})
  return points
}
