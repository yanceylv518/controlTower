<script setup lang="ts">
import { watch, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { client } from '../api'
type Person = { id: string; name: string; phone: string; enabled: boolean; revision: number }
const props = defineProps<{ preview?: boolean }>()
const items = ref<Person[]>([]), loading = ref(false), ready = ref(false), editing = ref(false), saving = ref(false), error = ref('')
const draft = ref<Person>({ id: '', name: '', phone: '', enabled: true, revision: 0 })
async function load() {
 if(props.preview){items.value=[];ready.value=false;error.value='';return}
 loading.value = true; error.value = ''
 try { items.value = (await client.request<{items:Person[]}>('/api/dashboard/operations-people')).items; ready.value = true }
 catch(e) { ready.value=false;error.value=e instanceof Error?e.message:'运营人员加载失败' }
 finally { loading.value=false }
}
function edit(p?: Person) { draft.value=p?{...p}:{id:'',name:'',phone:'',enabled:true,revision:0};editing.value=true }
async function save() {
 if(props.preview)return
 if(!draft.value.name.trim()||!/^(1[3-9]\d{9}|0\d{9,11})$/.test(draft.value.phone.trim())) { ElMessage.error('请填写姓名与有效的国内手机或固话'); return }
 saving.value=true
 try { await client.request('/api/dashboard/operations-people',{method:'PUT',body:JSON.stringify(draft.value)}); editing.value=false;await load();ElMessage.success('人员已保存，后续新提醒使用更新后的信息') }
 catch(e) { ElMessage.error(e instanceof Error?e.message:'保存失败') }
 finally {saving.value=false}
}
const masked=(p:string)=>p.length>7?p.slice(0,3)+'****'+p.slice(-4):p
watch(()=>props.preview,()=>{editing.value=false;void load()},{immediate:true})
</script>
<template>
 <section v-loading="loading" class="people-settings">
  <div class="people-heading"><div><h2>运营人员</h2><p class="sub-note">仅用于测试开始提醒。在测试跟进中为每个测试对象选择通知人员。</p></div><div><el-button :disabled="preview" @click="load">刷新</el-button><el-button type="primary" :disabled="!ready && !preview" @click="edit()">{{ preview ? '预览添加人员' : '添加人员' }}</el-button></div></div>
  <el-alert v-if="error" :title="error" type="error" :closable="false" />
  <el-table v-mobile-cards :data="items" :empty-text="preview ? '界面预览 · 更新 Server 后可加载和维护运营人员' : '暂无运营人员'">
   <el-table-column prop="name" label="姓名 / 备注" /><el-table-column label="联系电话"><template #default="{row}">{{ masked(row.phone) }}</template></el-table-column>
   <el-table-column label="状态"><template #default="{row}"><el-tag :type="row.enabled?'success':'info'">{{ row.enabled?'启用':'停用' }}</el-tag></template></el-table-column>
   <el-table-column label="操作" width="100"><template #default="{row}"><el-button link type="primary" @click="edit(row)">编辑</el-button></template></el-table-column>
  </el-table>
  <p class="sub-note">停用后不再向该人员发送新的测试提醒电话；历史记录保留当时的姓名、号码和拨打结果。客户流量预警的接听号码独立配置。</p>
  <el-dialog v-model="editing" title="运营人员" width="min(480px, 94vw)" :close-on-click-modal="!saving" :show-close="!saving">
   <el-form label-position="top" :disabled="saving"><el-form-item label="姓名 / 备注"><el-input v-model="draft.name" maxlength="80" /></el-form-item><el-form-item label="联系电话"><el-input v-model="draft.phone" maxlength="32" autocomplete="off" /></el-form-item><el-form-item label="启用状态"><el-switch v-model="draft.enabled" /></el-form-item></el-form>
   <template #footer><el-button :disabled="saving" @click="editing=false">取消</el-button><el-button type="primary" :disabled="preview" :loading="saving" @click="save">{{ preview ? '预览模式，暂不能保存' : '保存' }}</el-button></template>
  </el-dialog>
 </section>
</template>
<style scoped>.people-settings{display:grid;gap:16px;min-width:0}.people-heading{display:flex;justify-content:space-between;gap:12px;flex-wrap:wrap}.people-heading h2{margin:0}.people-heading .sub-note{margin:6px 0}</style>
