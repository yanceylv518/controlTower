<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import type { BillingReadonlyChannel, BillingUpstream } from "@ct/shared";
import { dashboard } from "../api";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import { useAsyncData } from "../composables/useAsyncData";
import { useFiltersStore } from "../stores/filters";

const filters=useFiltersStore();
const dialogOpen=ref(false),saving=ref(false),search=ref("");
const form=reactive<BillingUpstream>({id:0,instance_id:"",name:"",enabled:true,remark:"",channels:[]});
const state=useAsyncData(async()=>{await filters.loadInstances();if(!filters.site_id)return{items:[] as BillingUpstream[],channels:[] as BillingReadonlyChannel[]};const result=await dashboard.billingUpstreams(filters.site_id);return{items:result.items||[],channels:result.channels||[]}});
const items=computed(()=>{const q=search.value.trim().toLowerCase();return(state.data.value?.items||[]).filter(v=>!q||v.name.toLowerCase().includes(q)||v.remark.toLowerCase().includes(q)||v.channels.some(c=>String(c.channel_id).includes(q)||c.channel_name.toLowerCase().includes(q)))});
const channels=computed(()=>state.data.value?.channels||[]);
const occupied=computed(()=>{const result=new Map<number,string>();for(const upstream of state.data.value?.items||[]){if(upstream.id===form.id)continue;for(const channel of upstream.channels)result.set(channel.channel_id,upstream.name)}return result});
function openCreate(){Object.assign(form,{id:0,instance_id:filters.site_id,name:"",enabled:true,remark:"",channels:[]});dialogOpen.value=true}
function openEdit(row:BillingUpstream){Object.assign(form,{...row,channels:row.channels.map(v=>({...v}))});dialogOpen.value=true}
function channelDisabled(id:number){return occupied.value.has(id)}
async function save(){if(!form.name.trim()){ElMessage.warning("请输入上游名称");return}saving.value=true;try{await dashboard.saveBillingUpstream({...form,instance_id:filters.site_id,channels:form.channels.map(v=>({...v}))});ElMessage.success(form.id?"上游已更新":"上游已创建");dialogOpen.value=false;await state.reload()}finally{saving.value=false}}
async function remove(row:BillingUpstream){await ElMessageBox.confirm(`删除上游“${row.name}”后，其渠道将变为未配置。是否继续？`,"删除上游",{type:"warning"});await dashboard.deleteBillingUpstream(filters.site_id,row.id);ElMessage.success("上游已删除");await state.reload()}
watch(()=>filters.site_id,()=>state.reload());void state.reload();
</script>

<template>
  <AppShell title="上游管理">
    <template #tools><el-input v-model="search" clearable placeholder="搜索上游或渠道" style="width:240px"/><el-button @click="state.reload">刷新</el-button><el-button type="primary" :disabled="!filters.site_id" @click="openCreate">新建上游</el-button></template>
    <el-alert title="渠道列表实时读取当前站点的只读数据库" description="只展示 NewAPI 当前真实渠道，不使用 CT 渠道快照；API Key 仍由 NewAPI 管理，CT 不保存上游密钥。" type="info" :closable="false" show-icon style="margin-bottom:12px"/>
    <AsyncPanel :loading="state.loading.value" :error="state.error.value" :empty="!items.length" :empty-text="filters.site_id?'当前站点还没有配置上游':'尚未选择站点'" @retry="state.reload">
      <el-table :data="items"><el-table-column prop="name" label="上游名称" min-width="180"><template #default="s"><b>{{s.row.name}}</b></template></el-table-column><el-table-column label="关联渠道" min-width="360"><template #default="s"><el-tag v-for="channel in s.row.channels" :key="channel.channel_id" class="channel-tag" effect="plain">{{channel.channel_name||`渠道 ${channel.channel_id}`}} · #{{channel.channel_id}}</el-tag><span v-if="!s.row.channels.length" class="muted">尚未关联渠道</span></template></el-table-column><el-table-column prop="remark" label="备注" min-width="180" show-overflow-tooltip/><el-table-column label="状态" width="90"><template #default="s"><el-tag :type="s.row.enabled?'success':'info'">{{s.row.enabled?'启用':'停用'}}</el-tag></template></el-table-column><el-table-column label="操作" width="140"><template #default="s"><el-button link type="primary" @click="openEdit(s.row)">编辑</el-button><el-button link type="danger" @click="remove(s.row)">删除</el-button></template></el-table-column></el-table>
    </AsyncPanel>
    <el-dialog v-model="dialogOpen" :title="form.id?'编辑上游':'新建上游'" width="620px"><el-form label-width="90px"><el-form-item label="上游名称" required><el-input v-model="form.name" maxlength="128" placeholder="例如：Anthropic 生产账户"/></el-form-item><el-form-item label="关联渠道"><el-select v-model="form.channels" value-key="channel_id" multiple filterable collapse-tags collapse-tags-tooltip placeholder="选择只读数据库中的真实渠道" style="width:100%"><el-option v-for="channel in channels" :key="channel.channel_id" :label="`${channel.channel_name||`渠道 ${channel.channel_id}`} · #${channel.channel_id} · ${channel.models||'未配置模型'}`" :value="{channel_id:channel.channel_id,channel_name:channel.channel_name}" :disabled="channelDisabled(channel.channel_id)"><span>{{channel.channel_name||`渠道 ${channel.channel_id}`}} · #{{channel.channel_id}}</span><small class="option-note">{{occupied.get(channel.channel_id)?`已属于 ${occupied.get(channel.channel_id)}`:`${channel.status===1?'启用':'停用'} · ${channel.models||'未配置模型'}`}}</small></el-option></el-select></el-form-item><el-form-item label="状态"><el-switch v-model="form.enabled" active-text="启用" inactive-text="停用"/></el-form-item><el-form-item label="备注"><el-input v-model="form.remark" type="textarea" :rows="3" maxlength="500" show-word-limit/></el-form-item></el-form><template #footer><el-button @click="dialogOpen=false">取消</el-button><el-button type="primary" :loading="saving" @click="save">保存</el-button></template></el-dialog>
  </AppShell>
</template>
<style scoped>.sub,.muted,.option-note{display:block;color:var(--el-text-color-secondary);font-size:12px}.channel-tag{margin:3px 6px 3px 0}.option-note{float:right;margin-left:18px;max-width:270px;overflow:hidden;text-overflow:ellipsis}</style>
