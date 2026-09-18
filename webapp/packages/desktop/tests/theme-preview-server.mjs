// Local, synthetic, read-only fixtures for theme QA. Never proxies to a real service.
import { createServer } from 'node:http'
const metric = { dimension_type: 'instance', dimension_key: 'demo', instance_id: 'demo', tpm: 285600, request_count: 1240, success_rate: .992, error_rate: .008, p95_use_time: 2.45, avg_use_time: .86 }
const instance = { instance_id: 'demo', site_id: '主题验收 · 模拟数据', name: '主题验收 · 模拟数据', enabled: true, logs_readonly_configured: true }
const history = Array.from({length:30}, (_,i)=>({...metric, bucket_time:new Date(Date.now()-(30-i)*60000).toISOString(), tpm: 180000 + Math.sin(i*.6)*55000+i*1500, success_rate:.987+Math.sin(i*.8)*.01}))
createServer((req,res)=>{
 const path = new URL(req.url,'http://localhost').pathname
 res.setHeader('Content-Type','application/json')
 if(req.method!=='GET'){res.statusCode=405;res.end('{"error":"read_only_theme_fixture"}');return}
 let result={items:[],total:0,configured:true}
 if(path==='/api/auth/me') result={username:'主题预览',role:'admin',permissions:['*'],scope_user_ids:[],enabled:true}
 else if(path.endsWith('/instances')) result={items:[instance]}
 else if(path.endsWith('/overview')) result={recent_1m:metric,runtime:{health:{up_count:6,down_count:0},docker:{running_count:12,stopped_count:1}}}
 else if(path.endsWith('/metric-history'))result={items:history}
 else if(path.endsWith('/metrics'))result={items:[{...metric,bucket_time:new Date().toISOString()}]}
 else if(path.endsWith('/alerts'))result={items:[{id:1,instance_id:'demo',instance_name:'模拟实例',severity:'warning',title:'响应延迟接近阈值',summary:'P95 2.45 秒，请关注服务响应情况。',dimension_key:'demo',dimension_type:'instance'},{id:2,instance_id:'demo',instance_name:'模拟实例',severity:'critical',title:'渠道请求失败',summary:'示例告警：用于检查错误状态的文字与背景对比。'}]}
 else if(path.endsWith('/settings'))result={items:{}}
 else if(path.endsWith('/passthrough/logs'))result={configured:true,total:3,items:[2,5,6].map((type,i)=>({id:i+1,user_id:100+i,created_at:new Date().toISOString(),type,username:['Alex','Morgan','Chris'][i],model_name:'claude-sonnet-4',channel_id:12,channel_name:'生产渠道 · 示例',token_name:'workspace-api',prompt_tokens:4500,completion_tokens:920,quota:24680,use_time:3.2,request_id:'theme-demo-'+i,upstream_request_id:'',content_summary:type===5?'上游服务响应超时':'请求已完成',group:'default',ip:'',is_stream:true,other:JSON.stringify({first_response_time:1.2})}))}
 else if(path.endsWith('/passthrough/logs/count'))result={configured:true,total:3}
 else if(path.endsWith('/passthrough/logs/stat'))result={configured:true,summary:{quota:74040,rpm:120,tpm:285600}}
 else if(path.includes('currency'))result={quota_per_unit:500000,price_multiplier:1,symbol:'$'}
 console.log(path)
 res.end(JSON.stringify(result))
}).listen(18090,'127.0.0.1',()=>console.log('Theme fixtures: http://127.0.0.1:18090'))
