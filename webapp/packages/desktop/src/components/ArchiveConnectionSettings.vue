<script setup lang="ts">
import { computed, onUnmounted, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { ApiError } from '@ct/shared'
import { client } from '../api'

const props=defineProps<{siteId:string}>()
type Connection={host:string;port:number;database:string;username:string;tls:boolean;source_hash:string;version:number;tested_at:string;password_set:boolean}
const defaults=():Connection=>({host:'',port:3306,database:'',username:'',tls:true,source_hash:'',version:0,tested_at:'',password_set:false})
const form=reactive(defaults()),password=ref(''),configured=ref(false),ready=ref(false),busy=ref(false),error=ref(''),tested=ref(''),confirmed=ref(false)
let sequence=0
const signature=computed(()=>JSON.stringify([props.siteId,form.host,form.port,form.database,form.username,form.tls,form.source_hash,form.version,password.value]))
const validTest=computed(()=>tested.value!==''&&tested.value===signature.value)
const errors:Record<string,string>={archive_tls_failed:'TLS 握手或证书校验失败，请检查 RDS SSL 配置与 Server 信任证书。',archive_auth_failed:'数据库账号认证或访问权限失败，请检查账号、密码与授权来源。',archive_network_failed:'Server 无法连接归档库，请检查地址、端口和网络白名单。',archive_database_missing:'目标数据库不存在，请检查数据库名。',archive_connection_unavailable:'连接配置读取失败，请确认 Server 已完成数据库迁移。',archive_invalid_connection:'请检查地址、端口、数据库名和账号。',archive_password_required:'首次配置需要填写只读账号密码。',secret_key_not_configured:'Server 尚未配置 CT_SECRET_KEY，无法安全保存密码。',archive_readonly_permissions_required:'账号仅可拥有目标归档库或归档表的 SELECT 权限，不能有写入、跨库或授权权限。',archive_identity_or_schema_mismatch:'归档身份或结构不匹配，已阻止连接。请核对站点和数据库。',archive_connection_test_failed:'测试失败，请检查 Server 到归档库的网络、账号、TLS、表结构及读取权限。',archive_connection_conflict:'配置已被其他人更新，请重新加载后编辑。',archive_test_required:'请先测试连接并确认归档身份。',archive_connection_save_failed:'保存失败，原配置保持不变。',site_not_found:'当前站点不存在。'}
function message(e:unknown){return e instanceof ApiError?(errors[e.code]||(e.status===404?'当前 Server 尚未提供归档库连接接口，请升级 Server 并完成 CT 库 097 迁移。':`请求失败（${e.status} · ${e.code}）`)):'连接服务失败，请稍后重试。'}
async function load(){
 const ticket=++sequence,site=props.siteId
 Object.assign(form,defaults());password.value='';configured.value=false;ready.value=false;tested.value='';confirmed.value=false;error.value='';busy.value=false
 if(!site)return
 busy.value=true
 try{const res=await client.request<{configured:boolean;connection:Connection}>(`/api/dashboard/log-archive-connection?site_id=${encodeURIComponent(site)}`)
 if(ticket!==sequence)return
 if(res.configured)Object.assign(form,res.connection)
 configured.value=res.configured;ready.value=true
 }catch(e){if(ticket===sequence)error.value=message(e)}finally{if(ticket===sequence)busy.value=false}
}
async function submit(save:boolean){
 if(!ready.value||busy.value||!props.siteId)return
 if(save&&(!validTest.value||!confirmed.value))return
 const ticket=sequence,site=props.siteId,before=signature.value
 busy.value=true;error.value=''
 try{
 const res=await client.request<{connection:Connection;saved:boolean}>(`/api/dashboard/log-archive-connection${save?'':'/test'}?site_id=${encodeURIComponent(site)}`,{method:save?'PUT':'POST',body:JSON.stringify({...form,password:password.value})})
 if(ticket!==sequence||site!==props.siteId||before!==signature.value)return
 Object.assign(form,res.connection)
 if(save){password.value='';configured.value=true;tested.value='';confirmed.value=false;ElMessage.success('归档库连接已保存，读取接口将使用此连接')}
 else{tested.value=signature.value;confirmed.value=false;ElMessage.success('连接、只读权限与新版归档表检查通过')}
 }catch(e){if(ticket===sequence){error.value=message(e);tested.value='';confirmed.value=false}}finally{if(ticket===sequence)busy.value=false}
}
watch(()=>props.siteId,load,{immediate:true})
watch(signature,()=>{confirmed.value=false})
onUnmounted(()=>{sequence++;password.value=''})
</script>

<template>
 <section class="archive-connection">
  <header><div><h3>归档库连接</h3><p>CT Server 只读查询归档统计与日志；填写归档库地址，不是 new-api 源库。</p></div><el-tag :type="ready&&configured?'success':'info'">{{!ready?'连接状态未获取':configured?'已保存连接':'尚未配置页面连接'}}</el-tag></header>
  <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon/>
  <p v-if="!ready" class="muted">{{busy?'正在读取连接配置…':'连接配置尚未就绪'}} <el-button text :disabled="busy||!siteId" @click="load">重新加载</el-button></p>
  <el-form label-position="top" :disabled="busy||!ready" @submit.prevent>
   <div class="connection-grid">
    <el-form-item label="数据库地址"><el-input v-model="form.host" placeholder="Server 可访问的归档库地址" maxlength="253" autocomplete="off"/></el-form-item>
    <el-form-item label="端口"><el-input-number v-model="form.port" :min="1" :max="65535" :precision="0"/></el-form-item>
    <el-form-item label="数据库名"><el-input v-model="form.database" placeholder="例如 pinducloud_logs_archive" maxlength="64"/></el-form-item>
    <el-form-item label="只读账号"><el-input v-model="form.username" placeholder="专用 SELECT 账号" maxlength="128" autocomplete="off"/></el-form-item>
    <el-form-item label="密码"><el-input v-model="password" type="password" show-password autocomplete="new-password" :placeholder="form.password_set?'留空沿用已保存密码':'输入只读账号密码'" maxlength="2048"/></el-form-item>
    <el-form-item label="连接加密"><el-switch v-model="form.tls" active-text="TLS（校验证书与主机名）"/></el-form-item>
   </div>
   <div v-if="form.source_hash" class="identity"><span>归档身份</span><code>{{form.source_hash}}</code><span v-if="form.tested_at">最近成功测试：{{new Date(form.tested_at).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai'})}}</span></div>
   <p class="muted">测试会检查连通性、只读权限和新版归档表。已有绑定不自动切换归档身份。文件配置仍可兼容；保存此处配置后优先使用页面连接。</p>
   <el-checkbox v-if="validTest" v-model="confirmed">已核对数据库及归档身份，确认属于当前站点</el-checkbox>
   <p v-if="!ready" class="muted">当前尚未读取到服务器配置，填写、测试与保存暂不可用；升级 Server 并完成迁移后点击“重新加载”。</p><footer><el-button :disabled="!ready" :loading="busy" @click="submit(false)">测试连接</el-button><el-button type="primary" :disabled="!ready||!validTest||!confirmed||busy" @click="submit(true)">保存连接</el-button><el-button text :disabled="busy" @click="load">重新加载</el-button><span v-if="validTest" class="test-success">本次测试通过，保存时将再次校验</span></footer>
  </el-form>
 </section>
</template>

<style scoped>
.archive-connection{padding:20px;background:var(--el-bg-color);border:1px solid var(--el-border-color-light);border-radius:10px}.archive-connection header{display:flex;justify-content:space-between;gap:16px;align-items:flex-start;margin-bottom:16px}.archive-connection h3{margin:0;font-size:16px}.archive-connection p{font-size:13px;line-height:1.7;color:var(--el-text-color-secondary);margin:8px 0}.connection-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:0 20px}.connection-grid .el-input-number{width:100%}.identity{display:flex;flex-wrap:wrap;gap:8px 16px;font-size:12px;padding:12px;background:var(--el-fill-color-light);border-radius:6px}.identity code{overflow-wrap:anywhere}.archive-connection footer{display:flex;align-items:center;flex-wrap:wrap;gap:10px;margin-top:16px}.test-success{font-size:12px;color:var(--el-color-success)}@media(max-width:900px){.connection-grid{grid-template-columns:repeat(2,minmax(0,1fr))}}@media(max-width:600px){.connection-grid{grid-template-columns:1fr}.archive-connection{padding:14px}.archive-connection header{flex-direction:column}.archive-connection :deep(.el-checkbox){height:auto;white-space:normal}.archive-connection :deep(.el-checkbox__label){white-space:normal}}
</style>
