<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { client } from '../api'
type Target = { site: string; user_id: number; label: string }
type Recipient = { phone: string; targets: string[] }
type Config = { enabled: boolean; tts_code: string; called_show_number: string; percent: number; delta: number; targets: Target[]; recipients: Recipient[] }
type Call = { id: string; site: string; user_id: number; phone: string; min_tpm: number; max_tpm: number; direction: string; status: string; code: string; call_id: string; created_at: string }
type Status = { site: string; user_id: number; state: string; min_tpm: number; max_tpm: number; direction: string; window_end: string }
type Response = { config: Config; credentials_ready: boolean; worker_enabled: boolean; calls: Call[]; targets: Status[] }
const config = ref<Config>({ enabled: false, tts_code: '', called_show_number: '', percent: 50, delta: 10000000, targets: [], recipients: [] })
const ready = ref(false), worker = ref(false), loading = ref(false), loaded = ref(false)
const calls = ref<Call[]>([]), statuses = ref<Status[]>([])
const labels: Record<string, string> = { notifications_disabled: '告警通知总开关已关闭', normal: '未达阈值', coverage_pending: '等待完整采集覆盖（约6分钟预热）', query_failed: '数据查询失败', credentials_missing: '待配置凭据', cooldown_or_phone_limit: '客户冷却或号码频控', accepted: '已受理（不代表已接听）', rejected: '接口拒绝', unknown: '结果待确认，不自动重试' }
async function request(save = false) {
 loading.value = true
 try {
  const result = await client.request<Response>('/api/dashboard/voice-alerts', save ? { method: 'PUT', body: JSON.stringify(config.value) } : {})
  config.value = result.config; ready.value = result.credentials_ready; worker.value = result.worker_enabled; calls.value = result.calls; statuses.value = result.targets; loaded.value = true
  if (save) ElMessage.success('电话预警配置已保存')
 } catch (e) { ElMessage.error(e instanceof Error ? e.message : '电话预警配置加载失败') }
 finally { loading.value = false }
}
onMounted(() => request())
const targetKey = (target: Target) => `${target.site}/${target.user_id}`
</script>

<template>
 <section v-loading="loading" class="panel sub-panel voice-settings">
  <h2>客户 TPM 电话预警</h2>
  <p class="sub-note">每次上报后检查近 5 分钟的滚动 TPM。最大值与最小值之差、差值占最小值的比例必须同时超过阈值；真实零流量可触发，缺数据不触发。同一客户拨号后至少冷却 10 分钟。</p>
  <el-alert :type="ready && worker ? 'success' : 'info'" :closable="false" :title="!worker ? '当前为 API-only 模式，电话检测后台未运行' : ready ? '服务端凭据已配置；模板和号码需通过阿里云审核' : '尚未配置服务端 AccessKey，可先保存规则，当前不会拨号'" />
  <el-form label-position="top" :disabled="!loaded || loading">
   <el-form-item label="启用电话预警"><el-switch v-model="config.enabled" /></el-form-item>
   <div class="voice-fields">
    <el-form-item label="语音通知模板 ID"><el-input v-model="config.tts_code" placeholder="TTS_...，模板变量必须为 customer 和 direction" /></el-form-item>
    <el-form-item label="专属显号 / 服务实例（公共模式留空）"><el-input v-model="config.called_show_number" placeholder="留空使用公共号码池" /></el-form-item>
    <el-form-item label="波动比例大于（%）"><el-input-number v-model="config.percent" :min="0.1" :max="10000" /></el-form-item>
    <el-form-item label="TPM 最大差值大于（Token）"><el-input-number v-model="config.delta" :min="1" :max="1000000000000" :step="1000000" /></el-form-item>
   </div>
   <p>按站点 ID 与 NewAPI 用户 ID 配置客户；customer 变量使用下方填写的 NewAPI 用户名。</p>
   <div v-for="(target, index) in config.targets" :key="index" class="voice-target">
    <el-form-item label="站点 ID"><el-input v-model="target.site" /></el-form-item>
    <el-form-item label="用户 ID"><el-input-number v-model="target.user_id" :min="1" :max="9007199254740991" :controls="false" /></el-form-item>
    <el-form-item label="NewAPI 用户名（用于播报）"><el-input v-model="target.label" :maxlength="40" /></el-form-item>
    <el-button @click="config.targets.splice(index, 1)">移除</el-button>
   </div>
   <el-button :disabled="config.targets.length >= 100" @click="config.targets.push({ site: '', user_id: 1, label: '' })">添加客户</el-button>
   <h3>值班接听号码</h3>
   <p class="sub-note">可配置多个内部值班号码。客户范围留空表示接收全部客户的预警；选择客户后，仅接收所选客户。</p>
   <div v-for="(recipient, index) in config.recipients" :key="`recipient-${index}`" class="voice-recipient">
    <el-form-item label="接听号码"><el-input v-model="recipient.phone" placeholder="国内手机或固话" /></el-form-item>
    <el-form-item label="接收客户范围（不选即全部）">
     <el-select v-model="recipient.targets" multiple clearable collapse-tags placeholder="全部客户">
      <el-option v-for="target in config.targets" :key="targetKey(target)" :label="`${target.label || '未命名'}（${target.site}/${target.user_id}）`" :value="targetKey(target)" />
     </el-select>
    </el-form-item>
    <el-button @click="config.recipients.splice(index, 1)">移除号码</el-button>
   </div>
   <el-button @click="config.recipients.push({ phone: '', targets: [] })">添加值班号码</el-button>
   <el-button type="primary" :loading="loading" @click="request(true)">保存电话预警</el-button>
  </el-form>
  <p class="sub-note">号码频控：1 次/分钟、5 次/小时、20 次/24 小时，多客户共用号码合并计数。失败和结果未知的尝试也计入；持续异常在冷却结束后可再次通知。</p>
  <h3>检测状态与最近 100 次电话 <el-button size="small" @click="request()">刷新（重新加载配置）</el-button></h3>
  <el-table :data="statuses" size="small" empty-text="尚未启用或等待首次检测">
   <el-table-column prop="site" label="站点" /><el-table-column prop="user_id" label="用户 ID" />
   <el-table-column label="状态" min-width="200"><template #default="{ row }">{{ labels[row.state] || row.state }}</template></el-table-column>
   <el-table-column prop="direction" label="方向" width="80" />
   <el-table-column prop="min_tpm" label="最低 TPM" /><el-table-column prop="max_tpm" label="最高 TPM" />
  </el-table>
  <el-table :data="calls" size="small" empty-text="暂无拨号记录" max-height="320">
   <el-table-column label="时间" min-width="160"><template #default="{ row }">{{ new Date(row.created_at).toLocaleString() }}</template></el-table-column>
   <el-table-column prop="site" label="站点" /><el-table-column prop="user_id" label="用户" />
   <el-table-column prop="phone" label="接听号码" min-width="120" />
   <el-table-column prop="direction" label="方向" width="80" />
   <el-table-column label="结果" min-width="180"><template #default="{ row }">{{ labels[row.status] || row.status }} {{ row.code }}</template></el-table-column>
   <el-table-column prop="call_id" label="阿里云 CallId" min-width="180" />
  </el-table>
 </section>
</template>

<style scoped>
.voice-settings { grid-column: 1 / -1; min-width: 0; }
.voice-fields { display: grid; grid-template-columns: repeat(2,minmax(0,1fr)); gap: 0 16px; }
.voice-target { display: grid; grid-template-columns: repeat(4,minmax(0,1fr)) auto; gap: 12px; align-items: center; }
.voice-recipient { display: grid; grid-template-columns: minmax(220px,1fr) minmax(320px,2fr) auto; gap: 12px; align-items: center; }
.voice-target .el-input-number { width: 100%; }
.voice-settings .el-form { margin-top: 16px; }
@media(max-width:900px) { .voice-target,.voice-recipient,.voice-fields { grid-template-columns: 1fr; } }
</style>
