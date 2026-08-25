<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import type { BillingDiscountRule, BillingUpstream } from "@ct/shared";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import { dashboard } from "../api";
import { useAsyncData } from "../composables/useAsyncData";
import { useFiltersStore } from "../stores/filters";
import { billingReadErrorMessage } from "../utils/httpError";

const filters=useFiltersStore(),dialogOpen=ref(false),saving=ref(false),upstreams=ref<BillingUpstream[]>([]);
const form=reactive({id:0,instance_id:"",discount_type:"upstream_channel" as const,subject_id:undefined as number|undefined,channel_id:undefined as number|undefined,model_name:"",discount:"1.000000",effective_from:"",effective_to:undefined as string|undefined,remark:""});
const state=useAsyncData(async()=>{await filters.loadInstances();if(!filters.site_id)return{items:[] as BillingDiscountRule[]};const [rules,ups]=await Promise.all([dashboard.billingDiscounts(filters.site_id,"upstream_channel"),dashboard.billingUpstreams(filters.site_id)]);upstreams.value=ups.items||[];return rules});
const items=computed(()=>state.data.value?.items||[]),selectedUpstream=computed(()=>upstreams.value.find(v=>v.id===form.subject_id));
const upstreamName=(id:number)=>upstreams.value.find(v=>v.id===id)?.name||`上游 #${id}`;
const day=(v?:string)=>v?String(v).slice(0,10):"长期有效";
const iso=(v?:string)=>v?new Date(`${v.slice(0,10)}T00:00:00+08:00`).toISOString():undefined;
function openCreate(){Object.assign(form,{id:0,instance_id:filters.site_id,discount_type:"upstream_channel",subject_id:undefined,channel_id:undefined,model_name:"",discount:"1.000000",effective_from:"",effective_to:undefined,remark:""});dialogOpen.value=true}
function openEdit(v:BillingDiscountRule){Object.assign(form,{...v,effective_from:day(v.effective_from),effective_to:v.effective_to?day(v.effective_to):undefined});dialogOpen.value=true}
async function save(){if(!form.subject_id||!form.channel_id||!form.effective_from||Number(form.discount)<=0||Number(form.discount)>1){ElMessage.warning("请完整填写上游、渠道、生效日期和 0～1 之间的折扣");return}saving.value=true;try{await dashboard.saveBillingDiscount({...form,subject_id:form.subject_id,channel_id:form.channel_id,instance_id:filters.site_id,effective_from:iso(form.effective_from)!,effective_to:iso(form.effective_to)});ElMessage.success("渠道折扣已保存");dialogOpen.value=false;await state.reload()}catch(e){ElMessage.error(billingReadErrorMessage(e,"保存失败；请检查生效时间是否与已有规则重叠"))}finally{saving.value=false}}
async function remove(v:BillingDiscountRule){await ElMessageBox.confirm("删除后只影响以后创建的账单任务，已有任务快照不受影响。确定删除吗？","删除折扣",{type:"warning"});await dashboard.deleteBillingDiscount(filters.site_id,v.id);ElMessage.success("已删除");await state.reload()}
watch(()=>filters.site_id,()=>void state.reload(),{immediate:true});
</script>

<template><AppShell title="渠道折扣配置">
  <template #tools><el-button @click="state.reload">刷新</el-button><el-button type="primary" :disabled="!filters.site_id" @click="openCreate">新增渠道折扣</el-button></template>
  <el-alert title="渠道折扣保存在 CT，仅用于上游账单；创建任务时固化快照。未匹配规则时折扣默认为 1。" type="info" :closable="false" show-icon/>
  <AsyncPanel :loading="state.loading.value" :error="state.error.value" :empty="!items.length" empty-text="暂无渠道折扣" @retry="state.reload">
    <el-table :data="items" stripe class="rules"><el-table-column label="上游" min-width="180"><template #default="s">{{upstreamName(s.row.subject_id)}}</template></el-table-column><el-table-column label="渠道" min-width="200"><template #default="s">{{s.row.channel_name||`渠道 #${s.row.channel_id}`}}</template></el-table-column><el-table-column label="折扣" width="120"><template #default="s"><b>{{(Number(s.row.discount)*100).toFixed(2)}}%</b></template></el-table-column><el-table-column label="生效日期" width="130"><template #default="s">{{day(s.row.effective_from)}}</template></el-table-column><el-table-column label="失效日期" width="130"><template #default="s">{{day(s.row.effective_to)}}</template></el-table-column><el-table-column prop="remark" label="备注" min-width="180"/><el-table-column label="操作" width="130"><template #default="s"><el-button link type="primary" @click="openEdit(s.row)">编辑</el-button><el-button link type="danger" @click="remove(s.row)">删除</el-button></template></el-table-column></el-table>
  </AsyncPanel>
  <el-dialog v-model="dialogOpen" :title="form.id?'编辑渠道折扣':'新增渠道折扣'" width="560px"><el-form label-width="90px"><el-form-item label="上游" required><el-select v-model="form.subject_id" placeholder="请选择上游" style="width:100%" @change="form.channel_id=undefined"><el-option v-for="u in upstreams" :key="u.id" :label="u.name" :value="u.id"/></el-select></el-form-item><el-form-item label="渠道" required><el-select v-model="form.channel_id" placeholder="请先选择上游，再选择渠道" :disabled="!form.subject_id" style="width:100%"><el-option v-for="c in selectedUpstream?.channels||[]" :key="c.channel_id" :label="`${c.channel_name} · #${c.channel_id}`" :value="c.channel_id"/></el-select></el-form-item><el-form-item label="折扣" required><el-input v-model="form.discount" placeholder="0～1，例如 0.85"><template #append>{{(Number(form.discount||0)*100).toFixed(2)}}%</template></el-input></el-form-item><el-form-item label="生效区间" required><el-date-picker v-model="form.effective_from" type="date" value-format="YYYY-MM-DD" placeholder="开始日期"/><span class="range-sep">至</span><el-date-picker v-model="form.effective_to" type="date" value-format="YYYY-MM-DD" placeholder="长期有效"/></el-form-item><el-form-item label="备注"><el-input v-model="form.remark" type="textarea" :rows="3" maxlength="500"/></el-form-item></el-form><template #footer><el-button @click="dialogOpen=false">取消</el-button><el-button type="primary" :loading="saving" @click="save">保存</el-button></template></el-dialog>
</AppShell></template>
<style scoped>.rules{margin-top:16px}.range-sep{margin:0 8px;color:var(--el-text-color-secondary)}</style>
