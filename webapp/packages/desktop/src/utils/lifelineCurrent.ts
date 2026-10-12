import type { LifelineChannel } from '@ct/shared';

export const lifelineStates:Record<string,string>={all:'全部渠道',routine:'常态探测',recovery:'恢复探测中',waiting:'等待恢复',stopped:'未探测',stopping:'正在停止探测',unknown:'状态待确认'};
export const lifelineSwitches:Record<string,string>={enabled:'已启用',disabled:'已关闭',manual_disabled:'手动关闭',auto_disabled:'自动关闭',manual_hold:'手动接管 · 关闭待确认',unknown:'状态未知'};
export function currentScope(rows:LifelineChannel[],search:string,model:string,status:string){
 const q=search.trim().toLowerCase();return rows.filter(r=>(!model||r.models.includes(model))&&(!status||r.switch===status)&&(!q||`${r.channel_id} ${r.channel_name} ${r.models.join(' ')}`.toLowerCase().includes(q)));
}
export function currentOrder(rows:LifelineChannel[],state:string,sort:string){
 const rank=(r:LifelineChannel)=>r.state==='unknown'?0:r.state==='stopping'?1:r.state==='recovery'?2:r.state==='waiting'?3:r.accelerated>0?4:r.state==='routine'?5:6;
 return rows.filter(r=>state==='all'||r.state===state).sort((a,b)=>(sort==='attention'?rank(a)-rank(b):0)||a.channel_id-b.channel_id);
}
// Staleness belongs to the snapshot, not the channel's last reported state.
export function isCurrentSnapshotStale(snapshot:string|undefined,now:number){
 return !!snapshot&&now-Date.parse(snapshot)>60000;
}
