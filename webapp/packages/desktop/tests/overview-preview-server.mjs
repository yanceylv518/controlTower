// Actual Vue page with synthetic local-only API. Never forwards to production.
import {createServer} from 'node:http';
import {fileURLToPath} from 'node:url';
import {createServer as viteServer} from 'vite';
let mode='normal';
const stamp=()=>new Date().toISOString();
const instance=(id)=>({instance_id:id+'-1',site_id:id,name:id==='qa-overview'?'总览验收（模拟主站）':'总览验收（模拟空站）',enabled:true});
const fixture=createServer((req,res)=>{
 const url=new URL(req.url,'http://127.0.0.1:18094'),path=url.pathname;
 if(path==='/__fixture'){
  if(url.searchParams.has('mode')) mode=url.searchParams.get('mode');
  res.setHeader('Content-Type','text/html;charset=utf-8');
  res.end(`<h1>总览验收 · 合成数据</h1><p>当前场景 ${mode}</p>${['normal','partial','failure','stale','zero'].map(x=>`<p><a href="/__fixture?mode=${x}">${x}</a></p>`).join('')}`);return;
 }
 res.setHeader('Content-Type','application/json');
 const send=x=>res.end(JSON.stringify(x));
 if(path==='/api/auth/me')return send({username:'总览验收（模拟）',role:'admin',permissions:['*'],enabled:true});
 if(path==='/api/dashboard/instances')return send({items:[instance('qa-overview'),instance('qa-empty')]});
 if(path==='/api/dashboard/overview-snapshot'){
  const site=url.searchParams.get('site'),section=url.searchParams.get('section'),hours=Number(url.searchParams.get('hours')||1);
  if(mode==='failure'&&section==='requests'){res.writeHead(503);return send({error:'synthetic_timeout'});}
  const queried_at=stamp();
  if(section==='requests'){
   const end=Math.floor(Date.now()/60000)*60000,step=hours===24?300000:60000;
   const metrics={request_count:2096,success_count:1836,error_count:260,success_rate:1836/2096,error_rate:260/2096,tpm:82498889,prompt_tokens:80834674,completion_tokens:1664215,p95_use_time:90,p95_lower_bound:true,avg_use_time:25.919};
   if(mode==='zero'){Object.assign(metrics,{request_count:0,success_count:0,error_count:0,success_rate:null,error_rate:null,tpm:0,prompt_tokens:0,completion_tokens:0,p95_use_time:null,p95_lower_bound:false,avg_use_time:null});}
   const point=(from,to,complete=true)=>{
    const totals={...metrics},minutes=(to-from)/60000;
    for(const key of ['request_count','success_count','error_count','tpm','prompt_tokens','completion_tokens']) totals[key]*=minutes;
    return {from:new Date(from).toISOString(),to:new Date(to).toISOString(),complete,received_sources:1,confirmed_at:complete?queried_at:null,metrics:complete?totals:{...totals,success_rate:null,error_rate:null,p95_use_time:null,avg_use_time:null}};
   };
   const recent=point(end-60000,end,mode!=='partial');
   const points=Array.from({length:hours*3600000/step},(_,i)=>point(end-hours*3600000+i*step,end-hours*3600000+(i+1)*step,i%17!==0));
   return send({site,queried_at,sources:site==='qa-empty'?[]:['qa-overview-1'],recent:site==='qa-empty'?{...recent,complete:false,metrics:null}:recent,points:site==='qa-empty'?[]:points,step_seconds:step/1000});
  }
  if(section==='runtime')return send({site,queried_at,stale_after_seconds:120,items:site==='qa-empty'?[]:Array.from({length:5},(_,i)=>({instance_id:`qa-node-${i+1}`,name:`服务节点 ${i+1}`,enabled:true,last_seen_at:queried_at,health:[{target:'status',status:'up',checked_at:queried_at,http_status_code:200}],docker:[]}))});
  return send({site,queried_at,evaluated_at:mode==='stale'?new Date(Date.now()-3600000).toISOString():queried_at,fresh:mode!=='stale',total:site==='qa-empty'?0:1,unconfirmed:50,items:site==='qa-empty'?[]:[{id:'demo',instance_id:'qa-node-1',title:'渠道 #7 错误率偏高',summary:'最近确认窗口错误率 12.4%',first_seen_at:queried_at,confirmed_at:queried_at,dimension_type:'instance_channel',dimension_key:'qa-node-1:channel:7'}]});
 }
 if(path.endsWith('menu-visibility')||path.endsWith('settings'))return send({items:{}});
 return send({items:[],total:0});
});
await new Promise(resolve=>fixture.listen(18094,'127.0.0.1',resolve));
const vite=await viteServer({root:fileURLToPath(new URL('..',import.meta.url)),configFile:fileURLToPath(new URL('../vite.config.ts',import.meta.url)),server:{host:'127.0.0.1',port:5199,strictPort:true,proxy:{'/api':{target:'http://127.0.0.1:18094'}}}});
await vite.listen();console.log('Overview QA http://127.0.0.1:5199/ ; controls http://127.0.0.1:18094/__fixture');
async function close(){await vite.close();fixture.close();process.exit(0)}
process.on('SIGINT',close);process.on('SIGTERM',close);
