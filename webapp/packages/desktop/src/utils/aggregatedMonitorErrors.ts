import type { ErrorCodeResult } from './monitorErrorTrend';
export type AggregatedErrors = {
 since: string; until: string; total_errors: number; started_at: string | null;
 last_loss_at?: string | null; observed_at: string | null; covered_until: string | null; dropped: number;
 codes: {key:string;count:number}[]; buckets: ErrorCodeResult['buckets']; truncated: boolean;
};
// One bounded query against CT statistics; never fall back to source log scans.
export async function loadAggregatedMonitorErrors(query: URLSearchParams, signal: AbortSignal,
 request: (url:string,options:{signal:AbortSignal})=>Promise<AggregatedErrors>): Promise<ErrorCodeResult> {
 const params=new URLSearchParams(query);
 const markers:Record<string,string>={instance_user:'user',instance_channel:'channel',instance_model:'model'};
 const marker=markers[params.get('dimension_type')!];
 params.set('dimension_key',`${params.get('instance_id')}:${marker}:${params.get('value')}`);
 params.delete('value'); params.set('timeline','true');
 const result=await request(`/api/dashboard/error-statistics?${params}`,{signal}); signal.throwIfAborted();
 let notice='';
 if(!result.started_at)notice='尚未收到 Agent 错误统计，请升级并启用采集 Agent。';
 else if(result.dropped>0 && !result.last_loss_at)notice='采集缓冲曾丢失统计，当前数据不完整，暂不展示错误码总量。';
 else if(!result.covered_until || Date.parse(result.since)>=Date.parse(result.until))notice='所选时段尚无完整采集覆盖，历史数据未回填或采集正在追赶。';
 else {
  const parts:string[]=[];
  if(result.last_loss_at)parts.push('已排除曾丢失或跳过统计的历史时段');
  if(Date.parse(result.since)>Date.parse(query.get('start_time')!))parts.push('历史未覆盖部分不计入');
  if(Date.parse(result.until)<Date.parse(query.get('end_time')!))parts.push(`统计截至 ${new Date(result.until).toLocaleString()}，后续分钟尚未完整采集`);
  if(result.truncated)parts.push('错误码列表仅展示前 500 项，其余合并为其他');
  notice=parts.join('；');
 }
 const ready=!!result.started_at && !!result.covered_until && (!result.dropped || !!result.last_loss_at) && Date.parse(result.since)<Date.parse(result.until);
 return {configured:ready,notice,total:ready?result.total_errors:0,
 items:ready?result.codes.filter(c=>c.key!=='zero_output').map(c=>({code:c.key,count:c.count})):[],
 buckets:ready?result.buckets:[],bucket_seconds:query.get('bucket')==='5m'?300:60,
 start_time:result.since,end_time:result.until};
}
