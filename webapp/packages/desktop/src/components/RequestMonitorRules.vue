<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ApiError, siteOf } from '@ct/shared'
import { client } from '../api'
import type { ChannelRules } from '../utils/requestMonitor'
import { useFiltersStore } from '../stores/filters'
const filters=useFiltersStore()
const sites=computed(()=>[...new Set(filters.instances.filter(i=>i.enabled).map(siteOf))])
const props=defineProps<{editable:boolean}>()
const emit=defineEmits<{close:[];saved:[]}>()
const rules=ref<ChannelRules>(),busy=ref(false),error=ref('')
async function load(){
 busy.value=true;error.value=''
 try{await filters.loadInstances();rules.value=await client.request<ChannelRules>('/api/dashboard/request-monitor/rules')}
 catch{error.value='读取规则失败，请重试。'}
 finally{busy.value=false}
}
async function save(){
 if(!rules.value||!props.editable||busy.value)return
 busy.value=true;error.value=''
 try{await client.request('/api/dashboard/request-monitor/rules',{method:'PUT',body:JSON.stringify(rules.value)});emit('saved');emit('close')}
 catch(e){error.value=e instanceof ApiError&&e.status===409?'规则已被其他人修改，请重新加载后编辑。':e instanceof ApiError&&e.status===400?'请选择有效的 ALB 站点，并检查阈值和样本数。':'保存失败，请重试。'}
 finally{busy.value=false}
}
onMounted(load)
</script>
<template>
 <el-dialog :model-value="true" title="渠道判断规则" width="min(560px, calc(100vw - 32px))" :close-on-click-modal="false" @close="emit('close')">
  <el-alert v-if="error" :title="error" type="error" :closable="false"/>
  <p class="rule-note">达到最低请求量后，首响应、总耗时、错误率任一项达到阈值即进入关注列表。只查询下方绑定的 ALB 站点，不受顶部站点切换影响。</p>
  <el-form v-if="rules" label-position="top" :disabled="busy||!editable" class="rule-grid">
   <el-form-item label="ALB 对应站点" style="grid-column:1/-1"><el-select v-model="rules.site_id" placeholder="请选择此 ALB 对应的站点"><el-option v-for="site in sites" :key="site" :value="site" :label="site"/></el-select></el-form-item>
   <el-form-item label="统计窗口（分钟）"><el-input-number v-model="rules.window_minutes" :min="1" :max="30" :precision="0"/></el-form-item>
   <el-form-item label="窗口最低请求量"><el-input-number v-model="rules.min_requests" :min="1" :max="10000000" :precision="0"/></el-form-item>
   <el-form-item label="每项延迟最低有效样本数"><el-input-number v-model="rules.min_samples" :min="1" :max="10000000" :precision="0"/></el-form-item>
   <el-form-item label="首响应 P95（秒）"><el-input-number v-model="rules.ttft_seconds" :min="0.1" :max="3600" :precision="1"/></el-form-item>
   <el-form-item label="总耗时 P95（秒）"><el-input-number v-model="rules.duration_seconds" :min="0.1" :max="3600" :precision="1"/></el-form-item>
   <el-form-item label="错误率（%）"><el-input-number v-model="rules.error_percent" :min="0.1" :max="100" :precision="1"/></el-form-item>
  </el-form>
  <p class="rule-note">错误率按 CT 错误请求数 ÷ 有效请求数计算，包含用户侧错误。首响应仅统计有首响应时间的请求。</p>
  <p v-if="rules && (rules.ttft_seconds>90||rules.duration_seconds>60)" class="rule-note">延迟直方图有上限，超出可测范围时会显示下限值；无法确认是否达到阈值的渠道列入“数据待补充”。</p>
  <template #footer><el-button :disabled="busy" @click="load">重新加载</el-button><el-button @click="emit('close')">关闭</el-button><el-button v-if="editable" type="primary" :loading="busy" :disabled="!rules" @click="save">保存并应用</el-button></template>
 </el-dialog>
</template>
<style scoped>
.rule-note{line-height:1.7;color:var(--el-text-color-secondary);font-size:13px;margin:0 0 18px}
.rule-grid{display:grid;grid-template-columns:1fr 1fr;gap:0 16px}
@media(max-width:480px){.rule-grid{grid-template-columns:1fr}}
</style>
