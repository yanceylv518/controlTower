export type ErrorCodeResult={notice?:string;configured:boolean;total:number;items:{code:string;count:number}[];buckets:{time:string;counts:Record<string,number>}[];bucket_seconds:number;start_time:string;end_time:string};
const colors=['#2f5fe0','#d98416','#8054db','#ce3b44','#1391a5'];
export function errorTrend(result:ErrorCodeResult,selected:string|null=null){
 const chosen=selected===null?result.items.slice(0,5).map(i=>i.code):[selected];
 const chosenSet=new Set(chosen);const hasOther=selected===null&&result.items.length>5;
 const byTime=new Map(result.buckets.map(b=>[Date.parse(b.time),b.counts]));
 const step=result.bucket_seconds*1000;const start=Date.parse(result.start_time);const end=Date.parse(result.end_time);
 if(!Number.isFinite(step)||step<=0||!Number.isFinite(start)||!Number.isFinite(end)||end<=start||Math.ceil((end-start)/step)>2000)return [];
 const times:number[]=[];for(let t=Math.floor(start/step)*step;t<end;t+=step)times.push(t);
 const series=chosen.map((code,index)=>({name:code==='unknown'?'未知':code==='other'?'其他':code.replace('http:','HTTP ').replace('business:','业务码 ')||'未知',color:colors[index%colors.length],unit:' 次',smooth:false,data:times.map(t=>[new Date(t).toISOString(),byTime.get(t)?.[code]??0] as [string,number])}));
 if(hasOther)series.push({name:'其他',color:'#8792a2',unit:' 次',smooth:false,data:times.map(t=>[new Date(t).toISOString(),Object.entries(byTime.get(t)||{}).reduce((sum,[code,count])=>sum+(chosenSet.has(code)?0:count),0)] as [string,number])});
 if(!series.length)series.push({name:'错误数',color:'#ce3b44',unit:' 次',smooth:false,data:times.map(t=>[new Date(t).toISOString(),0] as [string,number])});
 return series;
}
