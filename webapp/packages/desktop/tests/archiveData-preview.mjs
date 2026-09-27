// Isolated synthetic data for visual QA. No requests go to a remote service.
import {createServer} from 'node:http'
import {createServer as viteServer} from 'vite'
import {fileURLToPath} from 'node:url'
const stamp=()=>new Date().toISOString()
const fixture=createServer((req,res)=>{
 const u=new URL(req.url,'http://127.0.0.1:18102'),p=u.pathname
 res.setHeader('Content-Type','application/json; charset=utf-8')
 const send=data=>res.end(JSON.stringify(data))
 if(p==='/api/auth/me')return send({username:'界面验收（合成数据）',role:'admin',permissions:['*'],enabled:true})
 if(p.endsWith('/instances'))return send({items:[{instance_id:'qa-node',site_id:'qa-site',name:'界面验收 · 合成数据',enabled:true,logs_readonly_configured:true}]})
 if(p.endsWith('/currency'))return send({site_id:'qa-site',type:'CNY',symbol:'¥',raw_quota_per_unit:'500000',exchange_rate:'7',observed_at:stamp()})
 if(p.endsWith('/log-archive-jobs'))return send({protocol:1,items:[{site_id:'qa-site',config:{version:1,instance_id:'qa-node',agent_id:'qa-agent',running:true,batch_size:1000,interval_seconds:2,delay_seconds:300,history_immutable:true,tasks:{collection:true,history:true}},seen_at:stamp(),targets:[],status:{applied_version:1,state:'running',engine:{protocol:1,first_date:'2026-08-01',collection:{step:'collect',after_id:'100',rows:'100',updated_at:stamp()},history:{step:'summarize',date:'2026-08-13',after_id:'100',rows:'100',updated_at:stamp()}}}}],days:[]})
 if(p.endsWith('/overview')){
  const month=u.searchParams.get('date'),dimension=u.searchParams.get('dimension')||'model_name'
  const days=Array.from({length:27},(_,i)=>({date:`${month}-${String(i+1).padStart(2,'0')}`,state:i<13?'sealed':'collecting',ready:i<25,version:'v'+i,error:'',updated_at:stamp()}))
  const rows=days.flatMap((d,i)=>['deepseek-v4-pro','glm-5.1','doubao-seed-2.0-pro'].map((model,j)=>({date:d.date,dimensions:{type:'2',[dimension]:dimension==='model_name'?model:String(j+1)},amounts:{requests:String((i+1)*500+j*120),quota:String((i+1)*500000+j*100),log_rows:String((i+1)*500+j*120),anomaly_rows:String((i+1)*500+j*120),prompt_tokens:String((i+1)*58000),completion_tokens:String((i+1)*18000),cache_tokens:String((i+1)*8000),empty_output:String(i+1),charged_empty_output:String(i),error_logs:'0',missing_output:'0'}})))
  return send({items:[{days,rows,options:{user_id:{4:true,7:true},channel_id:{252:true,198:true},model_name:{'deepseek-v4-pro':true,'glm-5.1':true}},observed_at:stamp()}],has_more:false})
 }
 if(p.endsWith('/logs'))return send({items:[{id:'9007199254740993',created_at:'1790467200',type:'2',user_id:'4',model_name:'deepseek-v4-pro',channel:'252',prompt_tokens:'10274',completion_tokens:'0',quota:'587',content_preview:'合成验收记录'}],has_more:false})
 if(p.endsWith('/menu-visibility')||p.endsWith('/settings'))return send({items:{}})
 return send({items:[],total:0})
})
await new Promise(resolve=>fixture.listen(18102,'127.0.0.1',resolve))
const vite=await viteServer({root:fileURLToPath(new URL('..',import.meta.url)),configFile:fileURLToPath(new URL('../vite.config.ts',import.meta.url)),server:{host:'127.0.0.1',port:5204,strictPort:true,proxy:{'/api':{target:'http://127.0.0.1:18102'}}}})
await vite.listen()
console.log('Synthetic archive preview: http://127.0.0.1:5204/log-archives')
