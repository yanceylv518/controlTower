export type RequestMinute = {
 time: number; host: string; count: number; small: number; medium: number; large: number; huge: number; unknown: number
 request_bytes: number; response_bytes: number; response_unknown: number; latest: number
 from_20_to_30?: number; from_30_to_40?: number; at_least_40?: number
}
export type RequestSnapshot = {
 range_supported?: boolean
 status: string; code?: string; queried_at: number; from: number; to: number; latest: number; settled_before: number; rows: RequestMinute[]
}
// datetime-local intentionally represents Beijing wall time regardless of the
// browser's timezone. Round-trip validation rejects impossible calendar dates.
export function beijingInput(epoch: number) { return new Date(epoch*1000+8*3600000).toISOString().slice(0,16) }
export function historyRange(fromText: string, toText: string, now=Date.now()/1000) {
 const parse=(s:string)=>{
  if(!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(s))return NaN
  const epoch=Date.parse(s+':00+08:00')/1000
  return Number.isFinite(epoch)&&beijingInput(epoch)===s?epoch:NaN
 }
 const from=parse(fromText),to=parse(toText)
 return from>0 && to>from && to-from<=86400 && to<=now ? {from,to} : null
}
export type ChannelRules = {version:number;site_id:string;window_minutes:number;min_requests:number;min_samples:number;ttft_seconds:number;duration_seconds:number;error_percent:number}
export type ChannelMeasure = {value:number|null;samples:number;minutes:number;lower_bound:boolean;trend:Array<number|null>}
export type SlowChannel = {instance_id:string;instance_name:string;key:string;name:string;count:number;latest:number;ttft:ChannelMeasure;duration:ChannelMeasure;errors:ChannelMeasure;reasons:string[];unknown:string[];partial:boolean;score:number}
export type ChannelSnapshot = {from:number;to:number;queried_at:number;latest:number;rules:ChannelRules;site:string;pending_count:number;pending:SlowChannel[];items:SlowChannel[]}
export const channelReason:Record<string,string>={ttft:'首响应慢',duration:'总耗时长',error_rate:'错误率高'}
export function measureText(m:ChannelMeasure|undefined,unit='秒'){return m?.value==null?'—':(m.lower_bound?'≥':'')+m.value.toFixed(1)+unit}
export function channelSeries(channel:SlowChannel,from:number,errors=false){
 const keys=errors?['errors'] as const:['ttft','duration'] as const
 return keys.map(key=>({name:({ttft:'首响应 P95',duration:'总耗时 P95',errors:'错误率'})[key],color:({ttft:'#ef4444',duration:'#f59e0b',errors:'#8b5cf6'})[key],unit:errors?'%':' 秒',smooth:false,data:channel[key].trend.map((v,i)=>[new Date((from+i*60)*1000).toISOString(),v===null?null:Number(v.toFixed(1))] as [string,number|null])}))
}
export const legacyBins = [
 { key: 'small', name: '<5 MiB', color: '#94a3b8' },
 { key: 'medium', name: '5–10 MiB', color: '#3b82f6' },
 { key: 'large', name: '10–20 MiB', color: '#f59e0b' },
 { key: 'huge', name: '≥20 MiB', color: '#ef4444' },
] as const
export const bins = [
 { key: 'small', name: '<5 MiB', color: '#94a3b8' },
 { key: 'medium', name: '5–10 MiB', color: '#3b82f6' },
 { key: 'large', name: '10–20 MiB', color: '#f59e0b' },
 { key: 'from_20_to_30', name: '20–30 MiB', color: '#ef4444' },
 { key: 'from_30_to_40', name: '30–40 MiB', color: '#a855f7' },
 { key: 'at_least_40', name: '≥40 MiB', color: '#db2777' },
] as const
const detailedKeys = ['from_20_to_30','from_30_to_40','at_least_40'] as const
export function hasDetailedSizes(row: RequestMinute) {
 return detailedKeys.every(key=>Number.isSafeInteger(row[key]) && row[key]!>=0)
  && row.from_20_to_30! + row.from_30_to_40! + row.at_least_40! === row.huge
}
const additive = ['count','small','medium','large','huge','unknown','request_bytes','response_bytes','response_unknown'] as const
function empty(time: number, host: string): RequestMinute {
 return { time, host, count:0,small:0,medium:0,large:0,huge:0,unknown:0,request_bytes:0,response_bytes:0,response_unknown:0,latest:0,
  from_20_to_30:0,from_30_to_40:0,at_least_40:0 }
}
export function addMinute(target: RequestMinute, row: RequestMinute) {
 const detailed = hasDetailedSizes(target) && hasDetailedSizes(row)
 for (const key of detailedKeys) target[key] = detailed ? target[key]! + row[key]! : undefined
 for (const key of additive) target[key] += row[key]
 target.latest = Math.max(target.latest, row.latest)
 return target
}
export function monitorWindow(snapshot: RequestSnapshot | undefined, minutes: number, host: string | null) {
 if (!snapshot?.from || !snapshot.to) return { points: [] as Array<{ time: number; value: RequestMinute | null }>, hosts: [] as RequestMinute[] }
 const end = Math.floor(snapshot.to / 60) * 60
 const from = Math.max(snapshot.from, end - minutes * 60)
 const buckets = new Map<number, RequestMinute>(), hosts = new Map<string, RequestMinute>()
 for (const row of snapshot.rows) {
  if (row.time < from || row.time >= snapshot.settled_before || row.time >= end) continue
  hosts.set(row.host, addMinute(hosts.get(row.host) || empty(from, row.host), row))
  if (host !== null && row.host !== host) continue
  buckets.set(row.time, addMinute(buckets.get(row.time) || empty(row.time, host ?? ''), row))
 }
 const points: Array<{time:number;value:RequestMinute|null}> = []
 for (let time=from;time<=end;time+=60) points.push({time,value:buckets.get(time) || null})
 return { points, hosts:[...hosts.values()].sort((a,b)=>b.count-a.count || a.host.localeCompare(b.host)) }
}
export function largePercent(row: RequestMinute | null | undefined) {
 const known = row ? row.count - row.unknown : 0
 return row && known>0 ? (row.medium+row.large+row.huge)*100/known : null
}
export function monitorError(code?: string) {
 const messages: Record<string,string> = {
  alb_auth_failed:'SLS 授权失败，请检查接入配置、RAM 权限及服务器时间。',
  alb_query_failed:'SLS 统计失败，请检查 app_lb_id、host、request_length、body_bytes_sent 的字段索引与统计开关。',
  alb_secret_unavailable:'无法解密凭证，请检查 CT_SECRET_KEY 或重新保存凭证。',
  alb_invalid_config:'ALB 接入配置无效，请重新检查。',
  alb_invalid_range:'请选择有效的起止时间，结束时间不能晚于现在，单次最多查询 24 小时。',
  alb_config_unavailable:'读取 ALB 配置失败，请检查 Server 与数据库迁移。',
  alb_source_not_found:'未找到 SLS 日志库，请核对地域、Project 和 Logstore。',
  alb_query_incomplete:'SLS 查询尚未完成，本次不展示部分统计。',
  alb_too_many_hosts:'分钟与 Host 组合超过查询上限，本次不展示截断统计。',
  alb_rate_limited:'SLS 查询被限流，请稍后重试。',
  alb_timeout:'SLS 查询超时，请检查网络或稍后重试。',
  alb_network_failed:'CT 无法连接 SLS，请检查公网网络与 Endpoint。',
  alb_invalid_response:'SLS 返回结果未通过完整性校验，请检查数据格式。',
  alb_service_failed:'SLS 服务暂不可用，请稍后重试。',
 }
 return messages[code || ''] || '请求失败，请稍后重试。'
}
