// Actual Vue pages with a local synthetic API; no proxy to any real service.
import {createServer} from 'node:http'
import {fileURLToPath} from 'node:url'
import {createServer as createViteServer} from 'vite'
const people=[{id:'a'.repeat(32),name:'刘敏',phone:'13800000000',enabled:true,revision:1},{id:'b'.repeat(32),name:'陈宇',phone:'13900000000',enabled:true,revision:1}]
const rule=()=>({enabled:true,use_percent:true,percent:20,delta:10000000})
const config={enabled:true,service_enabled:true,tts_code:'TTS_tpm',trial_tts_code:'TTS_trial',trial_template_ready:true,called_show_number:'057100000000',rise:rule(),fall:rule(),percent:20,use_percent:true,delta:10000000,recipients:[{phone:'13800000000',targets:[]}]}
const zero='0001-01-01T00:00:00Z'
let watches=[{id:'c'.repeat(32),site:'qa-site',user_id:7,token_id:9,label:'星河科技',rule:'first',gap_minutes:30,include_failed:true,phone:true,message:true,person_ids:people.map(p=>p.id),enabled:true,revision:1,round:1,started_at:new Date().toISOString(),last_at:zero,last_id:100,fired:false}],displayName='华东测试站'
const events=[{id:'d'.repeat(32),watch_id:watches[0].id,site_name:'华东测试站',customer:'星河科技',followed_by:'',log:{id:101,user_id:7,token_id:9,type:5,created_at:new Date().toISOString(),model:'gpt-4.1'},detected_at:new Date().toISOString(),deliveries:people.map((p,i)=>({id:p.id,name:p.name,phone:p.phone,kind:'phone',status:i?'unknown':'accepted',code:''}))}]
const server=createServer(async(req,res)=>{res.setHeader('Content-Type','application/json');const u=new URL(req.url,'http://localhost');let body={};if(req.method==='PUT'){let raw='';for await(const part of req)raw+=part;body=JSON.parse(raw)}const send=x=>res.end(JSON.stringify(x));
 if(u.pathname==='/api/auth/me')return send({username:'本地验收',role:'admin',permissions:['*'],enabled:true})
 if(u.pathname==='/api/dashboard/instances')return send({items:[{instance_id:'qa-node',site_id:'qa-site',name:'华东 API',enabled:true,logs_readonly_configured:true}]})
 if(u.pathname.endsWith('/voice-alerts')){if(req.method==='PUT')Object.assign(config,body);return send({site_id:'qa-site',site_scoped:true,direction_rules:true,operations_supported:true,config,credentials_ready:true,worker_enabled:true,calls:[],targets:[],customers:[{site:'qa-site',user_id:7,label:'星河科技'}],people,unavailable_sites:[]})}
 if(u.pathname.endsWith('/operations-people')){if(req.method==='PUT'){if(body.id){Object.assign(people.find(p=>p.id===body.id),body)}else{body.id='e'.repeat(32);people.push(body)}return send(body)}return send({items:people})}
 if(u.pathname.endsWith('/trial-followup/identities'))return send({items:u.searchParams.get('user_id')?[{id:9,name:'客户端联调'}]:[{id:7,name:'test_xinghe'}]})
 if(u.pathname.endsWith('/trial-followup')){if(req.method==='PUT'){if(body.action==='display_name')displayName=body.display_name;if(body.action==='follow')events[0].followed_by='本地验收';if(body.action==='watch'){const v=body.watch;if(!v.id){v.id='f'.repeat(32);watches.push(v)}else Object.assign(watches.find(w=>w.id===v.id),v);return send(v)}return send({ok:true})}return send({site_id:'qa-site',site_name:'华东 API',phone_ready:true,display_name:displayName,watches,people,events,state:'healthy',checked_at:new Date().toISOString(),worker_enabled:true})}
 if(req.method!=='GET'){res.statusCode=405;return send({error:'fixture_only'})}return send({items:{}})
})
await new Promise(resolve=>server.listen(18093,'127.0.0.1',resolve))
const vite=await createViteServer({root:fileURLToPath(new URL('..',import.meta.url)),configFile:fileURLToPath(new URL('../vite.config.ts',import.meta.url)),server:{host:'127.0.0.1',port:5199,strictPort:true,proxy:{'/api':{target:'http://127.0.0.1:18093',changeOrigin:true}}}})
await vite.listen();console.log('Trial UI QA: http://127.0.0.1:5199/trial-followup (synthetic API only)')
async function close(){await vite.close();server.close();process.exit(0)}process.on('SIGINT',close);process.on('SIGTERM',close)
