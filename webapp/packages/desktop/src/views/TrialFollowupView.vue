<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { client } from '../api'
import AppShell from '../components/AppShell.vue'
import { useFiltersStore } from '../stores/filters'
import { useAuthStore } from '../stores/auth'
import { can } from '../permissions'
type Person={id:string;name:string;phone:string;enabled:boolean}
type Identity={id:number;name:string}
type Watch={id:string;site:string;user_id:number;token_id:number;label:string;model:string;rule:'first'|'resume';gap_minutes:number;include_failed:boolean;phone:boolean;message:boolean;person_ids:string[];enabled:boolean;revision:number;round:number;started_at:string;last_at:string;last_id:number;fired:boolean;reset?:boolean}
type Delivery={id:string;name:string;phone:string;kind:string;status:string;code:string}
type Event={id:string;site_name:string;customer:string;watch_id:string;log:{id:number;user_id:number;token_id:number;type:number;created_at:string;model:string};detected_at:string;followed_by:string;deliveries:Delivery[]}
type Response={site_id:string;site_name:string;phone_ready:boolean;display_name:string;watches:Watch[];people:Person[];events:Event[];state:string;checked_at:string;worker_enabled:boolean}
const filters=useFiltersStore(),auth=useAuthStore(),router=useRouter()
const data=ref<Response|null>(null),loading=ref(false),saving=ref(false),error=ref(''),tab=ref('watches'),editor=ref(false),alias=ref(''),detail=ref<Event|null>(null),users=ref<Identity[]>([]),keys=ref<Identity[]>([]),identityError=ref(''),identityLoading=ref(false),mode=ref('account')
const preview=ref(false)
let version=0,keyVersion=0
const blank=():Watch=>({id:'',site:filters.site_id,user_id:0,token_id:0,label:'',model:'',rule:'first',gap_minutes:30,include_failed:true,phone:true,message:true,person_ids:[],enabled:false,revision:0,round:0,started_at:'0001-01-01T00:00:00Z',last_at:'0001-01-01T00:00:00Z',last_id:0,fired:false})
const draft=ref<Watch>(blank())
const selectedUser=computed({get:()=>draft.value.user_id||undefined,set:(id:number|undefined)=>{draft.value.user_id=id??0}})
const selectedKey=computed({get:()=>draft.value.token_id||undefined,set:(id:number|undefined)=>{draft.value.token_id=id??0}})
function previewData(site:string):Response {
 const people=[{id:'preview-person',name:'示例运营人员',phone:'示例号码',enabled:true}]
 const watch:Watch={...blank(),id:'preview-watch',site,user_id:101,token_id:201,label:'示例客户',model:'示例模型',enabled:true,person_ids:[people[0].id]}
 const at='2026-09-20T02:00:00Z'
 return {site_id:site,site_name:'示例站点',phone_ready:true,display_name:'示例测试站',watches:[watch],people,state:'preview',checked_at:'',worker_enabled:false,events:[{id:'preview-event',site_name:'示例测试站',customer:'示例客户',watch_id:watch.id,log:{id:1,user_id:101,token_id:201,type:2,created_at:at,model:'示例模型'},detected_at:at,followed_by:'',deliveries:[{id:'preview-delivery',name:people[0].name,phone:people[0].phone,kind:'phone',status:'accepted',code:'示例结果，未拨号'}]}]}
}
const displayName=computed(()=>alias.value.trim()||data.value?.site_name||filters.site_id)
const statuses:Record<string,string>={watch_changed:'测试配置或轮次已变更，本次跳过',pending:'等待通知',accepted:'呼叫已受理（未确认接听）',unknown:'结果未知，不自动重拨',limited:'号码频控受限',person_disabled:'人员已停用',recipient_changed:'号码已变更，本次跳过',template_unavailable:'模板未就绪',service_disabled:'电话服务已关闭',credentials_missing:'服务端凭据未配置',expired:'提醒已过期',sent:'消息已送达',partial:'部分渠道已送达',no_channel:'未配置匹配的群通知渠道',site_disabled:'站点已停用',notifications_disabled:'通知总开关已关闭',rejected:'拨号被拒绝'}
const stateText=computed(()=>preview.value?'界面预览 · 示例数据':!data.value?.worker_enabled?'检测后台未运行':data.value.state==='healthy'?'检测正常':data.value.state==='catching_up'?'正在追赶积压日志':data.value.state==='source_unavailable'?'日志源不可用，请检查只读连接':'等待检测')
const endpoint=()=>`/api/dashboard/trial-followup?site_id=${encodeURIComponent(filters.site_id)}`
async function load(){const site=filters.site_id,seq=++version;if(!site)return;loading.value=true;error.value='';try{const result=await client.request<Response>(endpoint());if(seq!==version||site!==filters.site_id)return;preview.value=false;editor.value=false;detail.value=null;data.value=result;alias.value=result.display_name}catch(e){if(seq===version&&site===filters.site_id){if(e instanceof Error&&e.message==='http_404'){preview.value=true;data.value=previewData(site);alias.value=data.value.display_name}else{preview.value=false;data.value=null;error.value=e instanceof Error?e.message:'加载失败'}}}finally{if(seq===version)loading.value=false}}
async function loadUsers(){if(preview.value){users.value=[{id:101,name:'示例测试账户'}];return}const site=filters.site_id;identityLoading.value=true;identityError.value='';try{const result=await client.request<{items:Identity[]}>(`/api/dashboard/trial-followup/identities?site_id=${encodeURIComponent(site)}`);if(site===filters.site_id)users.value=result.items}catch(e){if(site===filters.site_id)identityError.value=e instanceof Error?e.message:'账户目录读取失败'}finally{if(site===filters.site_id)identityLoading.value=false}}
async function loadKeys(clear=true){if(preview.value){keys.value=[{id:201,name:'示例测试 Key'}];if(clear){draft.value.token_id=0;draft.value.label='示例客户'}return}const site=filters.site_id,user=draft.value.user_id,seq=++keyVersion;keys.value=[];if(clear){draft.value.token_id=0;draft.value.label=users.value.find(u=>u.id===user)?.name||''}if(!user)return;identityError.value='';try{const result=await client.request<{items:Identity[]}>(`/api/dashboard/trial-followup/identities?site_id=${encodeURIComponent(site)}&user_id=${user}`);if(seq===keyVersion&&site===filters.site_id)keys.value=result.items}catch(e){if(seq===keyVersion)identityError.value=e instanceof Error?e.message:'Key目录读取失败'}}
async function edit(w?:Watch){draft.value=w?{...w,model:w.model||'',person_ids:[...w.person_ids]}:blank();mode.value=w?.token_id?'key':'account';editor.value=true;identityError.value='';await loadUsers();if(draft.value.user_id)await loadKeys(false)}
async function saveWatch(enabled:boolean){
 if(preview.value)return
 if(!draft.value.user_id||mode.value==='key'&&!draft.value.token_id){ElMessage.error('请选择测试账户和Key');return}
 if(enabled&&!draft.value.model.trim()){ElMessage.error('请填写测试模型');return}
 if(enabled&&draft.value.phone&&(!draft.value.person_ids.length||draft.value.person_ids.some(id=>!data.value?.people.find(p=>p.id===id)?.enabled))){ElMessage.error('请选择已启用的通知人员，并移除停用人员');return}
 if(enabled&&draft.value.phone&&!data.value?.phone_ready){ElMessage.error('电话服务或测试模板未就绪，请先配置，或关闭电话后使用群消息');return}
 if(enabled&&!draft.value.phone&&!draft.value.message){ElMessage.error('请至少选择一种通知方式');return}
 const site=filters.site_id,url=endpoint(),body=JSON.stringify({action:'watch',watch:{...draft.value,model:draft.value.model.trim(),enabled,token_id:mode.value==='account'?0:draft.value.token_id}});saving.value=true
 try {await client.request(url,{method:'PUT',body:JSON.stringify({action:'display_name',display_name:alias.value})});await client.request(url,{method:'PUT',body});if(site!==filters.site_id)return;editor.value=false;await load();ElMessage.success(enabled?'已保存并开启检测':'已保存，检测暂停')}
 catch(e){ElMessage.error(e instanceof Error?e.message:'保存失败')}finally{saving.value=false}
}
async function toggle(w:Watch,reset=false){if(preview.value)return;const site=filters.site_id,url=endpoint();if(reset){try{await ElMessageBox.confirm('开启新一轮后，只观察后续调用；历史提醒记录保留。','开启新一轮',{type:'warning'})}catch{return}}if(site!==filters.site_id)return
 saving.value=true;try{await client.request(url,{method:'PUT',body:JSON.stringify({action:'watch',watch:{...w,enabled:reset?true:!w.enabled,reset}})});if(site===filters.site_id)await load()}catch(e){ElMessage.error(e instanceof Error?e.message:'操作失败')}finally{saving.value=false}}
async function follow(e:Event){if(preview.value)return;saving.value=true;try{await client.request(endpoint(),{method:'PUT',body:JSON.stringify({action:'follow',event_id:e.id})});detail.value=null;await load()}catch(e){ElMessage.error(e instanceof Error?e.message:'关注失败')}finally{saving.value=false}}
const stamp=(s:string)=>!s||s.startsWith('0001')?'—':new Date(s).toLocaleString()
watch(()=>filters.site_id,()=>{++version;++keyVersion;preview.value=false;data.value=null;users.value=[];keys.value=[];editor.value=false;detail.value=null;alias.value='';draft.value=blank();void load()},{immediate:true,flush:'sync'})
</script>
<template>
 <AppShell title="测试跟进">
  <template #tools><el-button v-if="can(auth.user,'settings.manage')" @click="router.push('/settings')">电话与人员设置</el-button><el-button :disabled="loading||saving||editor" @click="load">刷新</el-button><el-button type="primary" :disabled="!data||saving" @click="edit()">添加测试对象</el-button></template>
  <div v-loading="loading" class="trial-page">
   <el-alert v-if="preview" title="界面预览：当前接口尚不可用，下方均为示例数据。可查看名单、提醒详情和添加表单；不会保存或发送通知。" type="info" :closable="false" />
   <el-alert v-if="error" :title="error" type="error" :closable="false" /><el-empty v-if="!filters.site_id" description="请选择站点" />
   <template v-if="data"><el-alert v-if="data.watches.some(w=>!w.model)" title="部分测试对象尚未配置模型，不会触发提醒。请进入配置填写测试模型并保存开启检测。" type="warning" :closable="false"/><div class="trial-summary"><span>{{ stateText }}</span><span>最近检测：{{ stamp(data.checked_at) }}</span><span>站点通知名称：{{ data.display_name||filters.site_id }}</span></div>
   <template v-if="!editor"><el-tabs v-model="tab"><el-tab-pane label="测试名单" name="watches"/><el-tab-pane label="提醒记录" name="events"/></el-tabs>
   <el-table v-if="tab==='watches'" v-mobile-cards :data="data.watches" empty-text="暂无测试对象，添加后开始观察后续调用">
    <el-table-column prop="label" label="客户"/><el-table-column label="监控对象"><template #default="{row}">账户 #{{ row.user_id }} · {{ row.token_id?'Key #'+row.token_id:'全部 Key' }}</template></el-table-column>
    <el-table-column class="trial-field-wide" label="测试模型"><template #default="{row}">{{ row.model||'未配置，请补充模型' }}</template></el-table-column>
    <el-table-column label="检测状态"><template #default="{row}"><el-tag :type="!row.model||!row.enabled?'info':row.fired?'success':'primary'">{{ !row.model?'待配置模型':!row.enabled?'暂停 / 草稿':row.fired?'已发现调用':'等待调用' }}</el-tag></template></el-table-column>
    <el-table-column label="通知人员"><template #default="{row}">{{ row.person_ids.map((id:string)=>data?.people.find(p=>p.id===id)?.name||'不可用人员').join('、')||'—' }}</template></el-table-column>
    <el-table-column label="操作" min-width="210"><template #default="{row}"><el-button link type="primary" :disabled="saving" @click="edit(row)">配置</el-button><el-button link :disabled="saving||preview" @click="toggle(row)">{{ row.enabled?'暂停':'恢复' }}</el-button><el-button link type="primary" :disabled="saving||preview" @click="toggle(row,true)">新一轮</el-button></template></el-table-column>
   </el-table>
   <el-table v-else v-mobile-cards :data="data.events" empty-text="暂无提醒记录">
    <el-table-column label="调用时间"><template #default="{row}">{{ stamp(row.log.created_at) }}</template></el-table-column><el-table-column prop="customer" label="客户"/><el-table-column prop="site_name" label="站点显示名称"/><el-table-column label="调用结果"><template #default="{row}">{{ row.log.type===2?'成功':'失败调用' }}</template></el-table-column><el-table-column label="跟进状态"><template #default="{row}">{{ row.followed_by?'已关注':'待关注' }}</template></el-table-column><el-table-column label="操作"><template #default="{row}"><el-button link type="primary" @click="detail=row">查看详情</el-button></template></el-table-column>
   </el-table></template>
   <template v-else><div><el-button link @click="editor=false">← 返回测试名单</el-button></div><div class="trial-editor"><el-form class="panel sub-panel trial-form" label-position="left" label-width="136px" :disabled="saving">
    <h2>配置测试跟进</h2><el-alert v-if="identityError" :title="identityError" type="warning" :closable="false"/>
    <el-form-item class="trial-field-wide" label="站点通知显示名称"><template #label><el-tooltip content="本站点测试提醒共用；留空使用站点名称。历史提醒保持原名称。" placement="top"><span class="trial-label-help">站点通知显示名称</span></el-tooltip></template><el-input v-model="alias" maxlength="80" :placeholder="filters.site_id"/></el-form-item>
    <el-form-item label="监控对象"><el-radio-group v-model="mode" :disabled="Boolean(draft.id)"><el-radio-button value="account">整个账户</el-radio-button><el-radio-button value="key">指定 Key</el-radio-button></el-radio-group></el-form-item>
    <el-form-item label="测试账户"><el-select v-model="selectedUser" placeholder="请选择测试账户" no-data-text="暂无可选账户" filterable :loading="identityLoading" :disabled="Boolean(draft.id)" @change="loadKeys()"><el-option v-for="u in users" :key="u.id" :value="u.id" :label="`${u.name} · #${u.id}`"/></el-select></el-form-item>
    <el-form-item v-if="mode==='key'" label="测试 Key"><template #label><el-tooltip content="只关联 Key ID，不读取或保存完整密钥。" placement="top"><span class="trial-label-help">测试 Key</span></el-tooltip></template><el-select v-model="selectedKey" :placeholder="draft.user_id?'请选择测试 Key':'请先选择测试账户'" no-data-text="该账户暂无可选 Key" filterable :disabled="Boolean(draft.id)||!draft.user_id"><el-option v-for="k in keys" :key="k.id" :value="k.id" :label="`${k.name} · #${k.id}`"/></el-select></el-form-item>
    <el-form-item class="trial-field-wide" label="测试模型" :required="true"><template #label><el-tooltip content="仅指定模型的调用触发提醒，模型名称区分大小写。修改模型会开启新一轮，只观察保存后的调用。" placement="top"><span class="trial-label-help">测试模型</span></el-tooltip></template><el-input v-model="draft.model" maxlength="200" placeholder="请输入日志中的完整模型名称，如 gpt-4.1"/></el-form-item>
    <el-form-item label="客户显示名称"><el-input v-model="draft.label" maxlength="80"/></el-form-item>
    <el-form-item label="何时提醒"><el-select v-model="draft.rule"><el-option value="first" label="本轮首次调用，只提醒一次"/><el-option value="resume" label="静默后再次调用时提醒"/></el-select></el-form-item>
    <el-form-item v-if="draft.rule==='resume'" label="静默时间（分钟）"><el-input-number v-model="draft.gap_minutes" :min="1" :max="10080"/></el-form-item>
    <el-checkbox v-model="draft.include_failed">失败调用尝试也提醒</el-checkbox>
    <el-alert v-if="draft.phone&&!data.phone_ready" type="warning" :closable="false" title="电话服务或测试模板未就绪。可保存草稿，或关闭电话后使用群消息。"/>
    <el-form-item class="trial-field-wide" label="通知方式"><el-checkbox v-model="draft.phone">电话提醒</el-checkbox><el-checkbox v-model="draft.message">同步发送消息到本站点运营群</el-checkbox></el-form-item>
    <el-form-item v-if="draft.phone" class="trial-field-wide" label="通知运营人员"><el-select v-model="draft.person_ids" placeholder="请选择通知运营人员（可多选）" no-data-text="暂无运营人员，请先在测试开始提醒中添加" multiple filterable><el-option v-for="p in data.people" :key="p.id" :value="p.id" :label="`${p.name} · ${p.phone}${p.enabled?'':' · 已停用'}`" :disabled="!p.enabled"/></el-select></el-form-item>
    <p class="sub-note">从开启检测后开始观察，历史调用不触发。恢复不重置已触发状态；需要再次首次提醒时使用“新一轮”。</p>
    <div class="trial-actions"><el-button :disabled="preview" :loading="saving" @click="saveWatch(false)">保存并暂停</el-button><el-button type="primary" :disabled="preview" :loading="saving" @click="saveWatch(true)">保存并开启检测</el-button></div>
   </el-form><aside class="panel sub-panel trial-preview"><h2>通知预览</h2><h3>电话播报</h3><p>您好，您关注的<strong>{{ displayName }}</strong>客户<strong>{{ draft.label||'客户显示名称' }}</strong>已开始接口测试，请运营人员及时查看调用情况并跟进。</p><h3>通知人员</h3><p v-for="id in draft.person_ids" :key="id">{{ data.people.find(p=>p.id===id)?.name }} · {{ data.people.find(p=>p.id===id)?.phone }}</p><h3>运营群消息</h3><p>站点：{{ displayName }}<br>客户：{{ draft.label||'—' }}<br>对象：账户 #{{ draft.user_id||'—' }}{{ mode==='key'?' · Key #'+draft.token_id:' · 全部 Key' }}<br>测试模型：{{ draft.model.trim()||'请填写测试模型' }}<br>触发后附调用时间、模型及成功/失败结果。</p><p class="sub-note">多人分别拨打，逐人记录结果。群消息沿用本站点匹配“测试开始提醒”的通知渠道。</p></aside></div></template>
   </template>
  </div>
  <el-dialog :model-value="Boolean(detail)" title="测试提醒详情" width="min(720px, 94vw)" @close="detail=null"><template v-if="detail"><h3>{{ detail.site_name }} · {{ detail.customer }}</h3><p>调用：{{ stamp(detail.log.created_at) }} · {{ detail.log.type===2?'成功':'失败' }} · {{ detail.log.model }}</p><p>检测：{{ stamp(detail.detected_at) }} · 账户 #{{ detail.log.user_id }} · Key #{{ detail.log.token_id }}</p><el-table v-mobile-cards :data="detail.deliveries"><el-table-column label="接收人"><template #default="{row}">{{ row.kind==='message'?'本站点运营群':row.name+' · '+row.phone }}</template></el-table-column><el-table-column label="结果"><template #default="{row}">{{ statuses[row.status]||row.status }} {{ row.code }}</template></el-table-column></el-table><p class="sub-note">账户和 Key 重叠命中时，相同调用的相同接收人合并通知。受理不代表已接听，也不代表已关注。</p><el-button type="primary" :disabled="Boolean(detail.followed_by)||saving||preview" @click="follow(detail)">{{ detail.followed_by?'已关注 · '+detail.followed_by:'标记已关注' }}</el-button></template></el-dialog>
 </AppShell>
</template>
<style scoped>
.trial-page{display:grid;gap:12px;min-width:0}
.trial-summary{display:flex;gap:12px;flex-wrap:wrap;font-size:12px;color:var(--ct-ink-3)}
.trial-editor{display:grid;grid-template-columns:minmax(0,1.3fr) minmax(0,1fr);gap:16px;align-items:start}
.trial-editor>.panel{margin:0;min-width:0;padding:20px}
.trial-form{display:grid;grid-template-columns:minmax(0,1fr);gap:16px;align-items:start}
.trial-form>h2,.trial-form>.el-alert,.trial-form>.sub-note,.trial-form>.trial-actions,.trial-form>.el-checkbox,.trial-form>.trial-field-wide{grid-column:1/-1}
.trial-form>h2{margin:0 0 4px}
.trial-label-help{cursor:help;text-decoration:underline dotted var(--ct-line);text-underline-offset:4px}
.trial-form :deep(.el-form-item){margin:0;min-width:0}
.trial-form :deep(.el-form-item__label){height:auto;min-height:36px;margin:0;padding:8px 16px 8px 0;line-height:20px;align-items:flex-start}
.trial-form :deep(.el-form-item__content){min-width:0;line-height:20px;gap:0 12px}
.trial-form :deep(.el-select){width:100%}
.trial-form :deep(.el-input),.trial-form :deep(.el-select){--el-component-size:36px}
.trial-form :deep(.el-select__wrapper){min-height:36px}
.trial-form :deep(.el-radio-button__inner){padding:10px 16px}
.trial-form :deep(.el-form-item__content > .sub-note){flex-basis:100%;margin:4px 0 0;line-height:1.5}
.trial-form>.sub-note{margin:0;line-height:1.5}
.trial-form :deep(.el-checkbox){height:auto;min-height:24px;margin-right:0}
.trial-form :deep(.el-checkbox__label){white-space:normal;line-height:20px}
.trial-preview h3{font-size:13px;margin:12px 0 4px}
.trial-preview p{margin:0 0 6px;line-height:1.6;overflow-wrap:anywhere}
.trial-actions{display:flex;gap:8px;justify-content:flex-end;flex-wrap:wrap;margin-top:4px}
@media(max-width:900px){.trial-editor{grid-template-columns:1fr}}
@media(max-width:600px){.trial-editor>.panel{padding:14px}.trial-form{gap:12px}.trial-form :deep(.el-form-item__label){width:100px!important;font-size:12px}.trial-form :deep(.el-form-item__content){margin-left:0!important}}
</style>
