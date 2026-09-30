<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue';
import { ElMessage, ElMessageBox } from 'element-plus';
import type { BillingDiscountRule, ReadonlyUser } from '@ct/shared';
import AppShell from '../components/AppShell.vue';
import { dashboard, passthrough } from '../api';
import { useFiltersStore } from '../stores/filters';
const filters = useFiltersStore();
const users = ref<ReadonlyUser[]>([]), active = ref<ReadonlyUser>();
const keyword = ref(''), total = ref(0), modelSearch = ref('');
const userOffset = ref(0), hasMoreUsers = ref(false);
let loadedKeyword = '';
const userList = ref<HTMLElement>();
const rules = ref<BillingDiscountRule[]>([]), catalog = ref<string[]>([]);
const current = ref<{model_name:string;discount:string}[]>([]);
const userLoading = ref(false), rulesLoading = ref(false), currentLoading = ref(false);
const userError = ref(''), rulesError = ref(''), currentError = ref(''), catalogError = ref('');
const catalogLoading = ref(false);
const updatedAt = ref(''), saving = ref(false), dialog = ref(false), saveError = ref('');
const target = ref<{site:string;user:ReadonlyUser}>();
const now = ref(Date.now());
let siteRevision = 0, userRevision = 0, currentRevision = 0, ruleRevision = 0;
let timer:ReturnType<typeof setTimeout>|undefined, refresh:ReturnType<typeof setInterval>|undefined;
let disposed = false;
const form = reactive({id:0,model_name:'',discount:0.8,effective_from:'',effective_to:'',remark:''});
const rows = computed(() => rules.value.filter(r => r.subject_id === active.value?.id));
const names = computed(() => [...new Set([...current.value.map(r=>r.model_name), ...rows.value.map(r=>r.model_name)])].sort());
const options = computed(() => [...new Set([...catalog.value,...names.value])].sort());
const percent = (v:string|number) => Number(v) === 1 ? '原价' : `${Number((Number(v)*10).toFixed(6))} 折`;
const date = (v?:string) => v ? new Date(v).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai',hour12:false,year:'numeric',month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit'}) : '长期有效';
const range = (r:BillingDiscountRule) => `${date(r.effective_from)} 至 ${date(r.effective_to)}`;
function state(r:BillingDiscountRule) { return new Date(r.effective_from).getTime()>now.value ? '待生效' : r.effective_to && new Date(r.effective_to).getTime()<=now.value ? '已过期' : '生效中'; }
const models = computed(() => names.value.filter(m=>m.toLowerCase().includes(modelSearch.value.toLowerCase())).map(model=>{
  const history = rows.value.filter(r=>r.model_name===model).sort((a,b)=>Date.parse(b.effective_from)-Date.parse(a.effective_from));
  return {model,discount:current.value.find(r=>r.model_name===model)?.discount,history};
}));
const conflicts = computed(() => !form.effective_from ? [] : rules.value.filter(r=>r.subject_id===target.value?.user.id && r.model_name===form.model_name.trim() && r.id!==form.id && Date.parse(r.effective_from)<(form.effective_to?Date.parse(form.effective_to):Infinity) && Date.parse(form.effective_from)<(r.effective_to?Date.parse(r.effective_to):Infinity)));
const needsHistoricalRange = computed(() => {
  const rate=current.value.find(r=>r.model_name===form.model_name.trim())?.discount;
  return !!currentError.value || currentLoading.value || (rate!==undefined && Number(rate)!==1);
});
const historyError = computed(() => !needsHistoricalRange.value ? '' : !form.effective_to ? '站点有折扣或尚未确认折扣，请设置补录结束时间' : Date.parse(form.effective_to)>now.value ? '补录结束时间不能晚于当前时间' : '');
const disabledFutureDate = (value:Date) => needsHistoricalRange.value && value.getTime()>Date.now();
const invalid = computed(() => !!historyError.value || !form.model_name.trim() || !Number.isFinite(form.discount) || form.discount<0 || form.discount>1 || !Number.isFinite(Date.parse(form.effective_from)) || (!!form.effective_to && (!Number.isFinite(Date.parse(form.effective_to)) || Date.parse(form.effective_to)<=Date.parse(form.effective_from))));
async function loadUsers(append = false) {
  if(append && (userLoading.value || !hasMoreUsers.value || loadedKeyword !== keyword.value))return;
  const n=++userRevision, site=filters.site_id, search=keyword.value, offset=append?userOffset.value:0;
  if(!site)return;
  userLoading.value=true; userError.value='';
  try { const result=await passthrough.users({site,keyword:search,limit:50,offset});
    if(disposed || n!==userRevision || site!==filters.site_id)return;
    users.value=append?[...users.value,...result.items.filter(u=>!users.value.some(old=>old.id===u.id))]:result.items;
    total.value=result.total; userOffset.value=offset+result.items.length;
    hasMoreUsers.value=result.items.length>0 && userOffset.value<result.total;
    loadedKeyword=search;
    if(!append && userList.value)userList.value.scrollTop=0;
    if(!active.value)active.value=result.items[0];
  } catch { if(n===userRevision)userError.value='用户加载失败，请重试'; }
  finally { if(n===userRevision)userLoading.value=false; }
}
function onUserScroll(event:Event) {
  const list=event.target as HTMLElement;
  if(list.scrollHeight-list.scrollTop-list.clientHeight<100 && !userError.value)void loadUsers(true);
}
async function loadRules() {
  const n=++ruleRevision,site=filters.site_id;
  rulesLoading.value=true; rulesError.value='';
  try { const result=await dashboard.billingDiscounts(site,'user_model');
    if(!disposed && n===ruleRevision && site===filters.site_id)rules.value=result.items;
  } catch { if(n===ruleRevision)rulesError.value='补录记录加载失败，请重试'; }
  finally {if(n===ruleRevision)rulesLoading.value=false;}
}
async function loadCurrent() {
  const site=filters.site_id,id=active.value?.id,n=++currentRevision;
  if(!site || !id)return;
  currentLoading.value=true;
  try { const result=await dashboard.billingCurrentDiscounts(site,id);
    if(disposed || n!==currentRevision || site!==filters.site_id || id!==active.value?.id)return;
    current.value=result.items; currentError.value=''; updatedAt.value=new Date().toISOString();
  } catch {if(n===currentRevision)currentError.value=updatedAt.value?'站点折扣刷新失败，当前显示上次读取结果':'站点折扣读取失败，请检查站点只读数据库连接及表权限';}
  finally {if(n===currentRevision)currentLoading.value=false;}
}
async function loadCatalog(n:number,site:string) {
  catalogLoading.value=true; catalogError.value='';
  try {
    const result=await dashboard.billingUpstreams(site);
    if(disposed || n!==siteRevision)return;
    catalog.value=[...new Set(result.channels.flatMap(c=>(c.models||'').split(',').map(m=>m.trim()).filter(Boolean)))].sort();
    if(!catalog.value.length)catalogError.value='站点渠道尚未配置模型，可直接输入模型名称';
  } catch {
    if(n===siteRevision)catalogError.value='站点模型读取失败，可重试或直接输入模型名称';
  } finally {if(n===siteRevision)catalogLoading.value=false;}
}
async function loadSite() {
  const n=++siteRevision; ++userRevision; ++ruleRevision; ++currentRevision;
  active.value=undefined; users.value=[]; rules.value=[]; catalog.value=[]; total.value=0; userOffset.value=0; hasMoreUsers.value=false; loadedKeyword='';
  current.value=[]; updatedAt.value=''; currentError.value=''; catalogError.value=''; dialog.value=false;
  try {await filters.loadInstances();} catch {if(n===siteRevision)userError.value='站点加载失败，请刷新页面重试';return;}
  if(disposed || n!==siteRevision || !filters.site_id)return;
  await Promise.all([loadUsers(),loadRules(),loadCatalog(n,filters.site_id)]);
}
function edit(r?:BillingDiscountRule,model='') {
  if(!active.value)return;
  now.value=Date.now();
  target.value={site:filters.site_id,user:{...active.value}};
  Object.assign(form,{id:r?.id||0,model_name:r?.model_name||model,discount:Number(r?.discount??current.value.find(v=>v.model_name===model)?.discount??0.8),effective_from:r?.effective_from||'',effective_to:r?.effective_to||'',remark:r?.remark||''});
  saveError.value='';dialog.value=true;
}
async function save() {
  now.value=Date.now();
  if(!target.value || invalid.value || conflicts.value.length || rulesError.value || rulesLoading.value || saving.value)return;
  const {site,user}=target.value; saving.value=true; saveError.value='';
  try {await dashboard.saveBillingDiscount({id:form.id,instance_id:site,discount_type:'user_model',subject_id:user.id,channel_id:0,model_name:form.model_name.trim(),discount:String(form.discount),effective_from:new Date(form.effective_from).toISOString(),effective_to:form.effective_to?new Date(form.effective_to).toISOString():undefined,remark:form.remark});
    if(site!==filters.site_id || disposed)return;
    dialog.value=false;await loadRules();ElMessage.success('补录已保存');
  } catch(e) {if(site===filters.site_id && !disposed){saveError.value=String(e).includes('billing_discount_overlap')?'该时间段与已有补录重叠，请调整时间或编辑已有记录。':String(e).includes('user_discount_history_only')?'站点当前有折扣，补录结束时间不能晚于当前时间':String(e).includes('user_discount_current_unavailable')?'站点折扣暂不可确认，请设置不晚于当前时间的结束时间，或恢复读取后重试':'保存失败，请稍后重试';if(String(e).includes('billing_discount_overlap'))await loadRules();}}
  finally {saving.value=false;}
}
async function remove(r:BillingDiscountRule) {
  const site=filters.site_id;
  try {await ElMessageBox.confirm(`删除 ${r.model_name} 的 ${percent(r.discount)} 补录？已有账单不变。`,'删除补录');}catch{return;}
  if(site!==filters.site_id || disposed)return;
  try {await dashboard.deleteBillingDiscount(site,r.id);if(site===filters.site_id){await loadRules();ElMessage.success('补录已删除');}}
  catch {ElMessage.error('删除失败，请稍后重试');}
}
function visibleRefresh() {now.value=Date.now();if(document.visibilityState==='visible' && !currentLoading.value)void loadCurrent();}
watch(()=>filters.site_id,()=>void loadSite(),{immediate:true});
watch(()=>active.value?.id,()=>{++currentRevision;current.value=[];updatedAt.value='';currentError.value='';currentLoading.value=false;dialog.value=false;void loadCurrent();});
watch(keyword,()=>{++userRevision;clearTimeout(timer);timer=setTimeout(()=>{void loadUsers();},250);});
onMounted(()=>{refresh=setInterval(visibleRefresh,60_000);document.addEventListener('visibilitychange',visibleRefresh);});
onBeforeUnmount(()=>{disposed=true;++siteRevision;++userRevision;++currentRevision;++ruleRevision;clearTimeout(timer);clearInterval(refresh);document.removeEventListener('visibilitychange',visibleRefresh);});
</script>
<template>
<AppShell title="用户折扣补录">
  <template #tools><el-button :loading="currentLoading" :disabled="!active" @click="loadCurrent">刷新站点折扣</el-button><el-button type="primary" :disabled="!active || !!rulesError || rulesLoading" @click="edit()">新增补录</el-button></template>
  <div class="discount-workspace">
    <aside><el-input v-model="keyword" clearable placeholder="搜索用户名称 / ID"/>
      <div ref="userList" class="user-list" @scroll="onUserScroll" :aria-busy="userLoading"><el-alert v-if="userError" :title="userError" type="error" :closable="false"><el-button link @click="loadUsers(users.length>0 && loadedKeyword===keyword)">重试</el-button></el-alert>
        <button v-for="u in users" :key="u.id" :class="{active:active?.id===u.id}" @click="active=u"><b>{{u.display_name||u.username}}</b><small>#{{u.id}} · {{u.username}}</small></button>
        <el-empty v-if="!users.length && !userLoading && !userError" description="没有匹配的用户" :image-size="48"/>
      <div v-if="userLoading" class="user-load-status">正在加载…</div>
        <el-button v-else-if="hasMoreUsers && !userError" class="load-more" link type="primary" @click="loadUsers(true)">加载更多</el-button>
      </div><small>共 {{total}} 位用户</small>
    </aside>
    <section><header><div><h3>{{active?.display_name||active?.username||'选择用户'}}</h3><small v-if="active">#{{active.id}} · {{names.length}} 个模型 · {{rows.length}} 条补录</small></div><small v-if="updatedAt">{{currentError?'上次成功':'更新于'}} {{date(updatedAt)}}</small></header>
      <el-alert v-if="currentError" :title="currentError" type="warning" :closable="false"><el-button link type="primary" :loading="currentLoading" @click="loadCurrent">重试</el-button></el-alert>
      <el-alert v-if="rulesError" :title="rulesError" type="error" :closable="false"><el-button link type="primary" @click="loadRules">重试</el-button></el-alert>
      <el-input v-model="modelSearch" class="model-search" clearable placeholder="搜索模型"/>
      <el-table :data="models" row-key="model" size="small" v-loading="rulesLoading" empty-text="暂无折扣记录，可新增补录">
        <el-table-column type="expand" width="36"><template #default="s">
          <el-table :data="s.row.history" size="small" class="history" empty-text="暂无补录记录">
            <el-table-column label="补录折扣" width="100"><template #default="h">{{percent(h.row.discount)}}</template></el-table-column>
            <el-table-column label="有效期" min-width="300"><template #default="h">{{range(h.row)}}</template></el-table-column>
            <el-table-column label="状态" width="90"><template #default="h"><el-tag size="small" :type="state(h.row)==='生效中'?'success':state(h.row)==='待生效'?'warning':'info'">{{state(h.row)}}</el-tag></template></el-table-column>
            <el-table-column prop="remark" label="备注" min-width="140" show-overflow-tooltip/>
            <el-table-column label="操作" width="115"><template #default="h"><el-button link type="primary" @click="edit(h.row)">编辑</el-button><el-button link type="danger" @click="remove(h.row)">删除</el-button></template></el-table-column>
          </el-table>
        </template></el-table-column>
        <el-table-column prop="model" label="模型" min-width="180"/>
        <el-table-column label="站点当前折扣" width="165"><template #default="s">{{s.row.discount!==undefined?percent(s.row.discount):currentError?'读取失败':currentLoading?'读取中':'未配置'}}<small v-if="currentError && s.row.discount!==undefined">（未刷新）</small></template></el-table-column>
        <el-table-column label="补录折扣 / 有效期" min-width="420"><template #default="s">
          <div v-for="r in s.row.history" :key="r.id" class="rule-summary"><b>{{percent(r.discount)}}</b><span>{{range(r)}}</span><el-tag size="small" :type="state(r)==='生效中'?'success':state(r)==='待生效'?'warning':'info'">{{state(r)}}</el-tag></div>
          <span v-if="!s.row.history.length">—</span>
        </template></el-table-column>
        <el-table-column label="补录记录" width="95"><template #default="s">{{s.row.history.length}} 条</template></el-table-column>
        <el-table-column label="操作" width="95"><template #default="s"><el-button link type="primary" :disabled="!!rulesError" @click="edit(undefined,s.row.model)">新增补录</el-button></template></el-table-column>
      </el-table>
    </section>
  </div>
  <el-dialog v-model="dialog" :title="form.id?'编辑补录':'新增折扣补录'" width="min(620px,95vw)" :close-on-click-modal="!saving" :show-close="!saving" :close-on-press-escape="!saving">
    <el-form label-position="top"><el-form-item label="用户">{{target?.user.display_name||target?.user.username}} #{{target?.user.id}}</el-form-item>
      <el-form-item label="模型" required><el-select v-model="form.model_name" :loading="catalogLoading" loading-text="正在加载模型" no-data-text="暂无模型，可直接输入名称" no-match-text="没有匹配模型，可直接输入名称" filterable allow-create default-first-option placeholder="选择或输入模型名称" :disabled="!!form.id || saving" style="width:100%"><el-option v-for="m in options" :key="m" :value="m" :label="m"/></el-select><small v-if="catalogError">{{catalogError}} <el-button link type="primary" :loading="catalogLoading" @click="loadCatalog(siteRevision,filters.site_id)">重新加载</el-button></small></el-form-item>
      <el-form-item label="折扣倍率" required><el-input-number v-model="form.discount" :disabled="saving" :min="0" :max="1" :step="0.01" :precision="6"/><span class="rate">{{percent(form.discount)}}</span></el-form-item>
      <div class="dates"><el-form-item label="生效时间" required><el-date-picker v-model="form.effective_from" :disabled-date="disabledFutureDate" :disabled="saving" type="datetime" format="YYYY-MM-DD HH:mm" value-format="YYYY-MM-DDTHH:mm:ssZ"/></el-form-item><el-form-item label="结束时间" :required="needsHistoricalRange"><el-date-picker v-model="form.effective_to" :disabled-date="disabledFutureDate" :disabled="saving" type="datetime" format="YYYY-MM-DD HH:mm" value-format="YYYY-MM-DDTHH:mm:ssZ" :placeholder="needsHistoricalRange?'最晚为当前时间':'长期有效'"/></el-form-item></div>
      <el-form-item label="备注"><el-input v-model="form.remark" :disabled="saving" maxlength="500"/></el-form-item>
    </el-form>
    <el-alert v-if="historyError" :title="historyError" type="warning" :closable="false"/>
    <el-alert v-if="conflicts.length" title="补录有效期重叠" type="warning" :closable="false"><div v-for="r in conflicts" :key="r.id">{{percent(r.discount)}} · {{range(r)}} <el-button link type="primary" @click="edit(r)">编辑已有记录</el-button></div></el-alert>
    <el-alert v-if="form.effective_to && Date.parse(form.effective_to)<=Date.parse(form.effective_from)" title="结束时间必须晚于生效时间" type="warning" :closable="false"/>
    <el-alert v-if="saveError" :title="saveError" type="error" :closable="false"/>
    <template #footer><el-button :disabled="saving" @click="dialog=false">取消</el-button><el-button type="primary" :loading="saving" :disabled="invalid || !!conflicts.length || !!rulesError || rulesLoading" @click="save">保存</el-button></template>
  </el-dialog>
</AppShell>
</template>
<style scoped>
.discount-workspace{display:grid;grid-template-columns:240px minmax(0,1fr);align-items:start;gap:12px}
aside,section{border:1px solid var(--el-border-color-light);border-radius:8px;background:var(--el-bg-color);padding:14px;min-width:0}
.user-load-status{padding:10px;text-align:center;color:var(--el-text-color-secondary);font-size:12px}.load-more{width:100%}.user-list{max-height:calc(100vh - 215px);overflow:auto;margin:10px 0;min-height:100px}
aside button{display:flex;flex-direction:column;gap:4px;width:100%;border:0;text-align:left;background:transparent;padding:10px;border-radius:6px;color:var(--el-text-color-primary);cursor:pointer;overflow-wrap:anywhere}
aside button.active{background:var(--el-color-primary-light-9);color:var(--el-color-primary)}
small{color:var(--el-text-color-secondary);font-size:12px}h3{margin:0 0 5px;font-size:16px}header{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:14px}
.rule-summary{display:flex;align-items:center;gap:12px;padding:3px 0}.rule-summary b{min-width:44px}.rule-summary span{white-space:nowrap}
.model-search{max-width:260px;margin:12px 0}.history{padding:8px 16px}.rate{margin-left:12px}.dates{display:grid;grid-template-columns:1fr 1fr;gap:16px}.dates :deep(.el-date-editor){width:100%}.el-alert{margin-bottom:10px}
@media(max-width:760px){.discount-workspace{grid-template-columns:1fr}.user-list{max-height:160px}.dates{grid-template-columns:1fr;gap:0}header{align-items:flex-start;flex-direction:column}}
</style>
