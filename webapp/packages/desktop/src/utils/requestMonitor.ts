export type RequestMinute = {
 time: number; host: string; count: number; small: number; medium: number; large: number; huge: number; unknown: number
 request_bytes: number; response_bytes: number; response_unknown: number; latest: number
}
export type RequestSnapshot = {
 status: string; code?: string; queried_at: number; from: number; to: number; latest: number; settled_before: number; rows: RequestMinute[]
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
export const bins = [
 { key: 'small', name: '<5 MiB', color: '#94a3b8' },
 { key: 'medium', name: '5–10 MiB', color: '#3b82f6' },
 { key: 'large', name: '10–20 MiB', color: '#f59e0b' },
 { key: 'huge', name: '≥20 MiB', color: '#ef4444' },
] as const
const additive = ['count','small','medium','large','huge','unknown','request_bytes','response_bytes','response_unknown'] as const
function empty(time: number, host: string): RequestMinute {
 return { time, host, count:0,small:0,medium:0,large:0,huge:0,unknown:0,request_bytes:0,response_bytes:0,response_unknown:0,latest:0 }
}
export function addMinute(target: RequestMinute, row: RequestMinute) {
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
