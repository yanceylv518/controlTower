<script setup lang="ts">
import {computed,ref,reactive,watch,onBeforeUnmount} from 'vue';
import {ElMessage,ElMessageBox} from 'element-plus';
import {ApiError,type BillingDiscountRule} from '@ct/shared';
import {dashboard} from '../api';
import {formatBillingDiscount} from '../utils/billingDiscount';
const props=defineProps<{site:string;upstream:number;channel:number;name:string;label?:string}>();
const emit=defineEmits<{changed:[]}>();
const open=ref(false),saving=ref(false),deleting=ref(0),loading=ref(false),loaded=ref(false),error=ref(''),items=ref<BillingDiscountRule[]>([]);
const busy=computed(()=>saving.value||deleting.value!==0);
const form=reactive({id:0,discount:1,effective_from:'',effective_to:''});
let version=0;
const date=(s?:string)=>s?new Date(s).toLocaleString('sv-SE',{timeZone:'Asia/Shanghai',hour12:false}).slice(0,16):'长期有效';
function status(rule:BillingDiscountRule){const now=Date.now();return Date.parse(rule.effective_from)>now?'待生效':rule.effective_to&&Date.parse(rule.effective_to)<=now?'已结束':'生效中';}
const conflicts=computed(()=>{
 const from=Date.parse(form.effective_from),to=form.effective_to?Date.parse(form.effective_to):Infinity;
 if(!Number.isFinite(from)||!(to>from))return [];
 return items.value.filter(v=>v.id!==form.id&&from<(v.effective_to?Date.parse(v.effective_to):Infinity)&&Date.parse(v.effective_from)<to);
});
async function load(){
 const current=version,site=props.site,upstream=props.upstream,channel=props.channel;
 loading.value=true;loaded.value=false;error.value='';
 try{const result=await dashboard.billingDiscounts(site,'upstream_channel');if(current!==version)return;items.value=result.items.filter(v=>v.subject_id===upstream&&v.channel_id===channel).sort((a,b)=>Date.parse(b.effective_from)-Date.parse(a.effective_from));loaded.value=true;}
 catch{if(current===version)error.value='渠道折扣加载失败，请重试。';}
 finally{if(current===version)loading.value=false;}
}
function edit(v?:BillingDiscountRule){error.value='';Object.assign(form,{id:v?.id||0,discount:Number(v?.discount??1),effective_from:v?.effective_from||'',effective_to:v?.effective_to||''});}
watch(form,()=>{error.value='';});
async function remove(rule:BillingDiscountRule){
 if(busy.value||loading.value||!loaded.value||!open.value)return;
 const current=version,site=props.site,id=rule.id;
 deleting.value=id;
 try{
  try{await ElMessageBox.confirm(`确定删除 ${formatBillingDiscount(rule.discount)}（${date(rule.effective_from)} 至 ${date(rule.effective_to)}）？删除只影响以后创建的账单，已有账单快照不变。`,'删除渠道折扣',{type:'warning',confirmButtonText:'删除折扣',cancelButtonText:'取消'});}catch{return;}
  if(current!==version||!open.value)return;
  await dashboard.deleteBillingDiscount(site,id);
  if(current!==version)return;
  if(form.id===id)edit();
  await load();
  if(current!==version)return;
  emit('changed');ElMessage.success('渠道折扣已删除');
 }catch{if(current===version)error.value='删除失败，请刷新列表确认后重试。';}
 finally{if(current===version)deleting.value=0;}
}
async function save(){
 if(busy.value||loading.value||!loaded.value||!open.value)return;
 error.value='';
 if(form.discount===1){error.value='原价无需设置折扣';return;}
 const from=Date.parse(form.effective_from),to=form.effective_to?Date.parse(form.effective_to):Infinity;
 if(!Number.isFinite(from)||!(to>from)){error.value='请填写生效时间，结束时间必须晚于生效时间。';return;}
 if(conflicts.value.length)return;
 const current=version;
 const payload={id:form.id,instance_id:props.site,discount_type:'upstream_channel' as const,subject_id:props.upstream,channel_id:props.channel,model_name:'',discount:String(form.discount),effective_from:new Date(from).toISOString(),effective_to:form.effective_to?new Date(to).toISOString():undefined,remark:''};
 saving.value=true;
 try{
  await dashboard.saveBillingDiscount(payload);
  if(current!==version)return;
  edit();await load();if(current!==version)return;emit('changed');ElMessage.success('渠道折扣已保存');
 }catch(e){
  if(current!==version)return;
  if(e instanceof ApiError&&e.code==='billing_discount_overlap'){await load();if(current===version)error.value='该时间段与已有折扣重叠，请调整时间或编辑已有折扣。';}
  else if(e instanceof ApiError&&e.code==='channel_discount_full_price')error.value='原价无需设置折扣';
  else if(e instanceof ApiError&&e.code==='invalid_discount')error.value='请检查折扣倍率和有效时间区间。';
  else error.value='渠道折扣保存失败，请稍后重试。';
 }finally{if(current===version)saving.value=false;}
}
watch(open,v=>{version++;saving.value=false;deleting.value=0;loading.value=false;loaded.value=false;if(v){edit();items.value=[];void load();}},{flush:'sync'});
watch(()=>[props.site,props.upstream,props.channel],()=>{version++;open.value=false;items.value=[];loaded.value=false;},{flush:'sync'});
onBeforeUnmount(()=>{version++;});
</script>
<template>
 <el-button link type="primary" @click="open=true">{{label||'设置折扣'}}</el-button>
 <el-dialog v-model="open" width="min(940px, calc(100vw - 32px))" align-center append-to-body :close-on-click-modal="!busy" :close-on-press-escape="!busy" :show-close="!busy">
  <template #header><div class="discount-header"><h3>渠道折扣</h3><p>{{name}} <span>渠道 #{{channel}}</span></p></div></template>
  <el-alert v-if="error" :title="error" type="error" :closable="false" class="discount-error"><el-button v-if="!loaded" link :disabled="busy||loading" @click="load">重新加载</el-button></el-alert>
  <div class="discount-layout">
   <section class="discount-records">
    <div class="section-heading"><h4>已有折扣 <span>{{items.length}}</span></h4><el-button link type="primary" :disabled="busy||!loaded" @click="edit()">新增折扣</el-button></div>
    <div v-loading="loading" class="discount-history">
     <article v-for="rule in items" :key="rule.id" class="discount-record" :class="{selected:form.id===rule.id}">
      <div class="record-heading"><strong>{{formatBillingDiscount(rule.discount)}}</strong><el-tag size="small" :type="status(rule)==='生效中'?'success':status(rule)==='待生效'?'primary':'info'" effect="plain">{{status(rule)}}</el-tag></div>
      <div class="record-period"><span>{{date(rule.effective_from)}}</span><span class="period-to">至</span><span>{{date(rule.effective_to)}}</span></div>
      <div class="record-actions"><el-button link type="primary" :disabled="busy||!loaded" @click="edit(rule)">编辑</el-button><el-button link type="danger" :loading="deleting===rule.id" :disabled="busy||!loaded" @click="remove(rule)">删除</el-button></div>
     </article>
     <el-empty v-if="!loading&&loaded&&!items.length" :image-size="64" description="尚未设置折扣，默认按原价结算"/>
    </div>
    <p class="discount-note">有效期按北京时间显示。修改或删除不影响已有账单快照。</p>
   </section>
   <section class="discount-edit">
    <div class="section-heading"><h4>{{form.id?'编辑折扣':'新增折扣'}}</h4><el-button v-if="form.id" link :disabled="busy" @click="edit()">取消编辑</el-button></div>
    <el-form label-position="top" :disabled="busy||!loaded" class="discount-form">
     <el-form-item label="折扣倍率"><el-input-number v-model="form.discount" :min="0" :max="1" :precision="6" :step="0.01" controls-position="right"/><p class="field-hint">{{form.discount===1?'原价无需新增规则；例如 0.8 表示 8 折。':`结算按原价 × ${form.discount}（${formatBillingDiscount(form.discount)}）`}}</p></el-form-item>
     <el-form-item label="生效时间" required><el-date-picker v-model="form.effective_from" type="datetime" format="YYYY-MM-DD HH:mm" value-format="YYYY-MM-DDTHH:mm:ssZ" placeholder="选择生效时间"/></el-form-item>
     <el-form-item label="结束时间"><el-date-picker v-model="form.effective_to" type="datetime" format="YYYY-MM-DD HH:mm" value-format="YYYY-MM-DDTHH:mm:ssZ" placeholder="留空表示长期有效" clearable/></el-form-item>
    </el-form>
    <el-alert v-if="conflicts.length" title="有效期与已有折扣重叠" type="warning" :closable="false"><div v-for="rule in conflicts" :key="rule.id">{{formatBillingDiscount(rule.discount)}} · {{date(rule.effective_from)}}<el-button link type="primary" :disabled="busy" @click="edit(rule)">编辑此规则</el-button></div></el-alert>
   </section>
  </div>
  <template #footer><el-button :disabled="busy" @click="open=false">关闭</el-button><el-button type="primary" :loading="saving" :disabled="busy||loading||!loaded||conflicts.length>0||form.discount===1" @click="save">{{form.id?'保存修改':'添加折扣'}}</el-button></template>
 </el-dialog>
</template>
<style scoped>
.discount-header h3{margin:0;font-size:18px;color:var(--ct-ink)}.discount-header p{margin:8px 0 0;font-size:13px;color:var(--ct-ink-2);overflow-wrap:anywhere}.discount-header p span{margin-left:10px;color:var(--ct-ink-3)}.discount-error{margin-bottom:16px}
.discount-layout{display:grid;grid-template-columns:minmax(0,1.2fr) minmax(280px,1fr);gap:24px;max-height:65vh;overflow:auto;color:var(--ct-ink)}.section-heading{display:flex;align-items:center;justify-content:space-between;gap:12px;min-height:32px;margin-bottom:14px}.section-heading h4{margin:0;font-size:14px}.section-heading h4 span{margin-left:6px;font-weight:400;color:var(--ct-ink-3)}.discount-history{min-height:120px;max-height:420px;overflow:auto}.discount-record{padding:14px 16px;margin-bottom:10px;border:1px solid var(--ct-line);border-radius:8px;background:var(--ct-surface)}.discount-record.selected{border-color:var(--ct-accent);background:var(--ct-accent-weak)}.record-heading{display:flex;justify-content:space-between;align-items:center;gap:12px}.record-heading strong{font-size:18px;font-weight:600}.record-period{display:flex;flex-wrap:wrap;gap:7px;margin:12px 0 8px;font-size:12px;color:var(--ct-ink-2);font-variant-numeric:tabular-nums}.period-to{color:var(--ct-ink-3)}.record-actions{display:flex;justify-content:flex-end;gap:8px}.discount-note,.field-hint{font-size:12px;line-height:1.6;color:var(--ct-ink-3);margin:10px 0 0}.discount-edit{border-left:1px solid var(--ct-line);padding-left:24px;min-width:0}.discount-form :deep(.el-input-number),.discount-form :deep(.el-date-editor){width:100%}.discount-form .el-form-item{margin-bottom:20px}.field-hint{margin:7px 0 0}.discount-form :deep(.el-form-item__content){display:block}
@media(max-width:700px){.discount-layout{grid-template-columns:1fr;gap:16px}.discount-edit{border-left:0;border-top:1px solid var(--ct-line);padding:16px 0 0}.discount-history{max-height:260px}}
</style>
