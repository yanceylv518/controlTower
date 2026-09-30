<script setup lang="ts">
import {computed,ref,reactive,watch} from 'vue';
import {ElMessage} from 'element-plus';
import {ApiError,type BillingDiscountRule} from '@ct/shared';
import {dashboard} from '../api';
import {formatBillingDiscount} from '../utils/billingDiscount';
const props=defineProps<{site:string;upstream:number;channel:number;name:string;label?:string}>();
const emit=defineEmits<{changed:[]}>();
const open=ref(false),saving=ref(false),loading=ref(false),loaded=ref(false),error=ref(''),items=ref<BillingDiscountRule[]>([]);
const form=reactive({id:0,discount:1,effective_from:'',effective_to:''});
const date=(s?:string)=>s?new Date(s).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai',hour12:false}):'长期有效';
const conflicts=computed(()=>{
 const from=Date.parse(form.effective_from),to=form.effective_to?Date.parse(form.effective_to):Infinity;
 if(!Number.isFinite(from)||!(to>from))return [];
 return items.value.filter(v=>v.id!==form.id&&from<(v.effective_to?Date.parse(v.effective_to):Infinity)&&Date.parse(v.effective_from)<to);
});
async function load(){loading.value=true;loaded.value=false;try{items.value=(await dashboard.billingDiscounts(props.site,'upstream_channel')).items.filter(v=>v.subject_id===props.upstream&&v.channel_id===props.channel);loaded.value=true;}catch{error.value='渠道折扣加载失败，请重新打开后重试。';}finally{loading.value=false;}}
function edit(v?:BillingDiscountRule){error.value='';Object.assign(form,{id:v?.id||0,discount:Number(v?.discount??1),effective_from:v?.effective_from||'',effective_to:v?.effective_to||''});}
watch(form,()=>{error.value='';});
async function save(){
 error.value='';
 if(form.discount===1){error.value='原价无需设置折扣';return;}
 const from=Date.parse(form.effective_from),to=form.effective_to?Date.parse(form.effective_to):Infinity;
 if(!Number.isFinite(from)||!(to>from)){error.value='请填写生效时间，结束时间必须晚于生效时间。';return;}
 if(conflicts.value.length||!loaded.value)return;
 saving.value=true;
 try{
  await dashboard.saveBillingDiscount({id:form.id,instance_id:props.site,discount_type:'upstream_channel',subject_id:props.upstream,channel_id:props.channel,model_name:'',discount:String(form.discount),effective_from:new Date(from).toISOString(),effective_to:form.effective_to?new Date(to).toISOString():undefined,remark:''});
  edit();await load();emit('changed');ElMessage.success('渠道折扣已保存');
 }catch(e){
  if(e instanceof ApiError&&e.code==='billing_discount_overlap'){await load();error.value='该时间段与已有折扣重叠，请调整时间或编辑已有折扣。';}
  else if(e instanceof ApiError&&e.code==='channel_discount_full_price')error.value='原价无需设置折扣';
  else if(e instanceof ApiError&&e.code==='invalid_discount')error.value='请检查折扣倍率和有效时间区间。';
  else error.value='渠道折扣保存失败，请稍后重试。';
 }finally{saving.value=false;}
}
watch(open,v=>{if(v){edit();items.value=[];void load();}});
</script><template><el-button link type="primary" @click="open=true">{{label||'设置折扣'}}</el-button><el-dialog v-model="open" :title="`${name} · 渠道折扣`" width="min(720px,95vw)" append-to-body><el-table v-loading="loading" :data="items" size="small"><el-table-column label="折扣" width="90"><template #default="s">{{formatBillingDiscount(s.row.discount)}}</template></el-table-column><el-table-column label="生效时间" min-width="180"><template #default="s">{{date(s.row.effective_from)}}</template></el-table-column><el-table-column label="结束时间" min-width="180"><template #default="s">{{date(s.row.effective_to)}}</template></el-table-column><el-table-column width="70"><template #default="s"><el-button link @click="edit(s.row)">编辑</el-button></template></el-table-column></el-table><el-divider>{{form.id?'编辑折扣':'新增折扣'}}</el-divider><el-alert v-if="error" :title="error" type="error" :closable="false" style="margin-bottom:16px"/>
<el-alert v-if="conflicts.length" title="折扣生效时间重叠" type="warning" :closable="false" style="margin-bottom:16px">
 <p style="margin:4px 0">同一渠道在同一时间只能有一个折扣。请调整时间，或编辑以下已有折扣：</p>
 <div v-for="rule in conflicts" :key="rule.id">{{formatBillingDiscount(rule.discount)}} · {{date(rule.effective_from)}} — {{date(rule.effective_to)}} <el-button link type="primary" :disabled="saving" @click="edit(rule)">编辑已有折扣</el-button></div>
</el-alert><el-form label-width="95px" :disabled="saving"><el-form-item label="折扣倍率" :error="form.discount===1?'原价无需设置折扣':''"><el-input-number v-model="form.discount" :min="0" :max="1" :precision="6" :step="0.01"/></el-form-item><el-form-item label="生效时间" required><el-date-picker v-model="form.effective_from" type="datetime" format="YYYY-MM-DD HH:mm" value-format="YYYY-MM-DDTHH:mm:ssZ"/></el-form-item><el-form-item label="结束时间"><el-date-picker v-model="form.effective_to" type="datetime" format="YYYY-MM-DD HH:mm" value-format="YYYY-MM-DDTHH:mm:ssZ" placeholder="长期有效"/></el-form-item></el-form><template #footer><el-button @click="open=false">关闭</el-button><el-button type="primary" :loading="saving" :disabled="loading||!loaded||conflicts.length>0||form.discount===1" @click="save">保存</el-button></template></el-dialog></template>
