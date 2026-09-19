<script setup lang="ts">
import { computed, watch, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { client } from '../api'
import { useFiltersStore } from '../stores/filters'
import StatusTag from './StatusTag.vue'
type Target = { site: string; user_id: number; label: string }
type Recipient = { phone: string; targets: string[]; scope?: 'all' | 'selected' }
type DirectionRule = { enabled: boolean; use_percent: boolean; percent: number; delta: number }
type Config = { rise: DirectionRule; fall: DirectionRule; enabled: boolean; tts_code: string; called_show_number: string; use_percent: boolean; percent: number; delta: number; recipients: Recipient[] }
type Call = { id: string; site: string; user_id: number; phone: string; min_tpm: number; max_tpm: number; direction: string; status: string; code: string; call_id: string; created_at: string }
type Status = { site: string; user_id: number; state: string; min_tpm: number; max_tpm: number; direction: string; window_end: string }
type Response = { site_id: string; site_scoped: boolean; direction_rules: boolean; config: Config; credentials_ready: boolean; worker_enabled: boolean; calls: Call[]; targets: Status[]; customers: Target[]; unavailable_sites: string[]; directory_error: string }
const defaultRule = (): DirectionRule => ({ enabled: true, use_percent: true, percent: 20, delta: 10000000 })
const directions = [{ key: 'rise' as const, label: '上涨预警' }, { key: 'fall' as const, label: '下降预警' }]
const normalizeRules = (c: Config) => {
 const legacy = { enabled: true, use_percent: c.use_percent ?? true, percent: c.percent ?? 20, delta: c.delta ?? 10000000 }
 return { rise: { ...(c.rise || legacy) }, fall: { ...(c.fall || legacy) } }
}
const filters = useFiltersStore()
let requestVersion = 0
const config = ref<Config>({ rise: defaultRule(), fall: defaultRule(), enabled: false, tts_code: '', called_show_number: '', use_percent: true, percent: 20, delta: 10000000, recipients: [] })
const deltaWan = {
 rise: computed({get: () => config.value.rise.delta / 10000, set: (v: number | undefined) => { config.value.rise.delta = typeof v === 'number' ? Math.round(v * 10000) : 0 }}),
 fall: computed({get: () => config.value.fall.delta / 10000, set: (v: number | undefined) => { config.value.fall.delta = typeof v === 'number' ? Math.round(v * 10000) : 0 }}),
}
const customers = ref<Target[]>([]), directoryWarning = ref('')
const serverCompatible = ref(false)
const ready = ref(false), worker = ref(false), loading = ref(false), loaded = ref(false)
const calls = ref<Call[]>([]), statuses = ref<Status[]>([])
const recordsTab = ref('status')
const labels: Record<string, string> = { notifications_disabled: '告警通知总开关已关闭', normal: '未达阈值', coverage_pending: '等待完整采集覆盖（约6分钟预热）', query_failed: '数据查询失败', credentials_missing: '待配置凭据', cooldown_or_phone_limit: '客户冷却或号码频控', accepted: '已受理（不代表已接听）', rejected: '接口拒绝', unknown: '结果待确认，不自动重试' }
async function request(save = false, refreshOnly = false) {
 const site = filters.site_id
 if (!site || (save && (loading.value || !loaded.value))) return
 if (save && !serverCompatible.value) {
  ElMessage.error('请先更新远程 Server，再保存电话预警配置')
  return
 }
 if (save && [config.value.rise, config.value.fall].some(rule => !Number.isInteger(rule.delta) || rule.delta < 1 || rule.delta > 1000000000000)) {
  ElMessage.error('请填写有效的 TPM 差值（0.0001–100000000 万 Token）')
  return
 }
 if (save && config.value.recipients.some(r => r.scope === 'selected' && r.targets.length === 0)) {
  ElMessage.error('请选择至少一个客户，或切换为全部客户')
  return
 }
 const version = ++requestVersion
 loading.value = true
 try {
  const { enabled, tts_code, called_show_number, rise, fall, use_percent, percent, delta, recipients } = config.value
  const body = { enabled, tts_code, called_show_number, rise, fall, use_percent, percent, delta, recipients: recipients.map(r => ({ phone: r.phone, targets: r.scope === 'all' ? [] : r.targets })) }
  const result = await client.request<Response>(`/api/dashboard/voice-alerts?site_id=${encodeURIComponent(site)}`, save ? { method: 'PUT', body: JSON.stringify(body) } : {})
  if (version !== requestVersion || site !== filters.site_id) return
  if (!refreshOnly || !loaded.value) config.value = { ...result.config, ...normalizeRules(result.config), use_percent: result.config.use_percent ?? true, recipients: (result.config.recipients || []).map(r => ({ ...r, targets: r.targets || [], scope: r.targets?.length ? 'selected' : 'all' })) }
  customers.value = result.customers || []
  serverCompatible.value = Array.isArray(result.customers) && result.site_scoped === true && result.direction_rules === true && result.site_id === site
  directoryWarning.value = !serverCompatible.value ? '远程 Server 尚未支持上涨/下降独立配置。请先更新 Server；当前可预览页面，暂不能保存电话预警配置。' : result.directory_error || (result.unavailable_sites?.length ? `以下站点客户列表暂时无法读取：${result.unavailable_sites.join('、')}。已有选择保留，这些站点暂不检测。` : '')
  ready.value = result.credentials_ready; worker.value = result.worker_enabled; calls.value = result.calls; statuses.value = result.targets; loaded.value = true
  if (save) ElMessage.success('电话预警配置已保存')
 } catch (e) { if (version !== requestVersion || site !== filters.site_id) return; ElMessage.error(e instanceof Error ? e.message : '电话预警配置加载失败') }
 finally { if (version === requestVersion) loading.value = false }
}
watch(() => filters.site_id, () => {
 ++requestVersion
 loaded.value = false; loading.value = false; serverCompatible.value = false
 customers.value = []; calls.value = []; statuses.value = []; directoryWarning.value = ''
 config.value = { rise: defaultRule(), fall: defaultRule(), enabled: false, tts_code: '', called_show_number: '', use_percent: true, percent: 20, delta: 10000000, recipients: [] }
 void request()
}, { immediate: true, flush: 'sync' })
const targetKey = (target: Target) => `${target.site}/${target.user_id}`
const customerOptions = computed(() => {
 const options = customers.value.map(t => ({ key: targetKey(t), label: `${t.label}（${t.site} · #${t.user_id}）`, unavailable: false }))
 const known = new Set(options.map(t => t.key))
 for (const r of config.value.recipients) for (const key of r.targets) {
  if (!known.has(key)) { options.push({ key, label: `暂不可用（${key}）`, unavailable: true }); known.add(key) }
 }
 return options
})
</script>

<template>
 <section v-loading="loading" class="voice-settings">
  <header class="voice-heading">
   <div><h2>客户 TPM 电话预警 <StatusTag :value="config.enabled ? 'enabled' : 'disabled'" /></h2><p class="sub-note">当前站点：{{ filters.site_id || '请选择站点' }} · 配置仅对本站点生效</p></div>
   <div class="heading-actions"><el-tag size="small" :type="ready && worker ? 'success' : 'info'">{{ !worker ? '检测后台未运行' : ready ? '服务端凭据已配置' : '待配置 AccessKey' }}</el-tag><el-button type="primary" :disabled="!loaded || loading || !serverCompatible" :loading="loading" @click="request(true)">保存电话预警</el-button></div>
  </header>
  <el-alert v-if="loaded && (!ready || !worker)" type="info" :closable="false" :title="!worker ? '电话检测后台未运行' : '尚未配置服务端 AccessKey，可先保存规则，当前不会拨号'" />
  <el-alert v-if="loaded && !config.enabled" type="info" :closable="false" title="当前站点未启用电话预警。旧全局配置仅预填规则和号码，请确认后启用并保存。" />
  <el-form label-position="top" :disabled="!loaded || loading">
   <section class="panel sub-panel voice-card rule-card">
   <div class="support-panel-head card-heading"><h2>预警规则</h2><div class="enable-control"><span>启用</span><el-switch v-model="config.enabled" aria-label="启用电话预警" size="small" /></div></div>
   <div class="voice-fields">
    <el-form-item class="full-field" label="语音通知模板"><el-input v-model="config.tts_code" placeholder="填写已审核的 TTS 模板 ID" /><span class="field-hint">模板变量：customer、direction</span></el-form-item>
    <el-form-item class="full-field" label="专属显号 / 服务实例"><el-input v-model="config.called_show_number" placeholder="公共模式留空" /></el-form-item>
    <section v-for="direction in directions" :key="direction.key" class="full-field direction-rule">
     <div class="threshold-caption"><span>{{ direction.label }}</span><el-switch v-model="config[direction.key].enabled" :aria-label="`启用${direction.label}`" size="small" /></div>
     <div class="voice-fields">
      <div class="full-field"><el-checkbox v-model="config[direction.key].use_percent" :disabled="!config[direction.key].enabled">同时判断{{ direction.key === 'rise' ? '上涨' : '下降' }}比例</el-checkbox></div>
      <el-form-item :label="`${direction.key === 'rise' ? '上涨' : '下降'}比例大于（%）`"><el-input-number v-model="config[direction.key].percent" :disabled="!config[direction.key].enabled || !config[direction.key].use_percent" :min="0.1" :max="10000" controls-position="right" /></el-form-item>
      <el-form-item label="TPM 差值大于（万）"><el-input-number v-model="deltaWan[direction.key].value" :disabled="!config[direction.key].enabled" :min="0.0001" :max="100000000" :step="100" controls-position="right" :aria-label="`${direction.label} TPM 差值（万 Token）`" /></el-form-item>
     </div>
    </section>
   </div>
   <details class="rule-details"><summary>计算口径与冷却规则</summary><p>差值 = 最高 TPM − 最低 TPM；按最高、最低值最后出现的先后判断方向：上涨比例 = 差值 ÷ 最低 TPM × 100%，下降比例 = 差值 ÷ 最高 TPM × 100%。关闭比例判断时，只需差值达标；开启时两项同时达标，从 0 上涨时只判断差值，降至 0 按下降 100% 判断；等于阈值不触发。采集不完整时等待数据，同一客户对同一号码至少冷却 10 分钟。</p></details>
   </section>
   <section class="panel sub-panel voice-card recipients-card">
   <div class="support-panel-head card-heading"><div><h2>值班接听号码 <el-tag size="small" type="info">{{ config.recipients.length }}</el-tag></h2>
   <p class="sub-note">默认本站点全部客户，也可为每个号码单独多选。</p>
   </div><el-button @click="config.recipients.push({ phone: '', targets: [], scope: 'all' })">＋ 添加接听号码</el-button></div>
   <el-alert v-if="directoryWarning" type="warning" :closable="false" :title="directoryWarning" />
   <div class="recipient-list">
   <el-empty v-if="!config.recipients.length" :image-size="48" description="尚未添加接听号码" />
   <div v-for="(recipient, index) in config.recipients" :key="`recipient-${index}`" class="voice-recipient">
    <span class="recipient-index">{{ String(index + 1).padStart(2, '0') }}</span>
    <el-form-item label="接听号码"><el-input v-model="recipient.phone" placeholder="国内手机或固话" /></el-form-item>
    <el-form-item label="接收客户范围">
     <el-radio-group v-model="recipient.scope">
      <el-radio value="all">本站点全部客户</el-radio>
      <el-radio value="selected">指定客户</el-radio>
     </el-radio-group>
     <el-select v-if="recipient.scope === 'selected'" v-model="recipient.targets" multiple filterable clearable collapse-tags collapse-tags-tooltip placeholder="搜索并选择客户（可多选）" :no-data-text="serverCompatible ? '暂无可选客户，请检查站点客户列表' : '请先更新远程 Server 以获取客户列表'">
      <el-option v-for="target in customerOptions" :key="target.key" :label="target.label" :value="target.key" :disabled="target.unavailable" />
     </el-select>
    </el-form-item>
    <el-button text type="danger" @click="config.recipients.splice(index, 1)">移除</el-button>
   </div>
   </div>
   <footer class="recipient-footer"><p class="sub-note">单号码频控：1 次/分钟 · 5 次/小时 · 20 次/天</p><details class="rule-details"><summary>客户范围与拨号说明</summary><p>全部范围仅包含本站点及其后续新增客户，电话自动播报 NewAPI 用户名。同一号码跨站点共用呼叫额度，失败和结果未知也计入；持续异常在冷却结束后可再次通知。</p></details></footer>
   </section>
  </el-form>
  <section class="panel sub-panel voice-card records-card">
  <div class="support-panel-head card-heading"><h2>预警记录 <span class="record-caption">最近 100 次拨号</span></h2><el-button size="small" @click="request(false, true)">刷新记录</el-button></div>
  <el-tabs v-model="recordsTab">
   <el-tab-pane label="检测状态" name="status">
  <el-table v-mobile-cards :data="statuses" size="small" empty-text="尚未启用或等待首次检测" max-height="280">
   <el-table-column prop="site" label="站点" /><el-table-column prop="user_id" label="用户 ID" />
   <el-table-column label="状态" min-width="200"><template #default="{ row }">{{ labels[row.state] || row.state }}</template></el-table-column>
   <el-table-column prop="direction" label="方向" width="80" />
   <el-table-column prop="min_tpm" label="最低 TPM" /><el-table-column prop="max_tpm" label="最高 TPM" />
  </el-table>
   </el-tab-pane>
   <el-tab-pane label="拨号记录" name="calls">
  <el-table v-mobile-cards :data="calls" size="small" empty-text="暂无拨号记录" max-height="320">
   <el-table-column label="时间" min-width="160"><template #default="{ row }">{{ new Date(row.created_at).toLocaleString() }}</template></el-table-column>
   <el-table-column prop="site" label="站点" /><el-table-column prop="user_id" label="用户" />
   <el-table-column prop="phone" label="接听号码" min-width="120" />
   <el-table-column prop="direction" label="方向" width="80" />
   <el-table-column label="结果" min-width="180"><template #default="{ row }">{{ labels[row.status] || row.status }} {{ row.code }}</template></el-table-column>
   <el-table-column prop="call_id" label="阿里云 CallId" min-width="180" />
  </el-table>
   </el-tab-pane>
  </el-tabs>
  </section>
 </section>
</template>

<style scoped>
.voice-settings { min-width: 0; display: grid; gap: 12px; }
.voice-heading { display: flex; justify-content: space-between; align-items: center; gap: 16px; }
.voice-heading h2 { margin: 0; font-size: 16px; font-weight: 700; display: flex; align-items: center; gap: 8px; }
.voice-heading .sub-note { margin: 4px 0 0; }
.heading-actions { display: flex; align-items: center; gap: 8px; }
.voice-settings > .el-form { display: grid; grid-template-columns: minmax(320px,.8fr) minmax(420px,1.2fr); gap: 12px; align-items: stretch; }
.voice-card { min-width: 0; margin: 0; }
.card-heading { margin-bottom: 12px; }
.card-heading h2 { display: flex; align-items: center; gap: 8px; }
.card-heading .sub-note { margin: 4px 0 0; }
.card-heading > .el-button { flex-shrink: 0; }
.enable-control { display: flex; align-items: center; gap: 8px; color: var(--ct-ink-2); }
.voice-fields { display: grid; grid-template-columns: repeat(2,minmax(0,1fr)); gap: 0 12px; }
.full-field,.threshold-caption { grid-column: 1 / -1; }
.threshold-caption { display: flex; justify-content: space-between; margin: 0 0 12px; padding-top: 12px; border-top: 1px solid var(--ct-line); }
.threshold-caption span + span { color: var(--ct-ink-3); font-size: 11px; }
.voice-fields :deep(.el-input-number) { width: 100%; }
.voice-fields .el-form-item { margin-bottom: 12px; }
.field-hint { color: var(--ct-ink-3); font-size: 11px; line-height: 1.6; margin-top: 4px; }
.recipients-card { display: flex; flex-direction: column; }
.recipient-list { flex: 1; min-height: 100px; }
.voice-recipient { display: grid; grid-template-columns: 16px minmax(120px,.85fr) minmax(180px,1.35fr) 40px; gap: 8px; align-items: start; padding: 8px 0 12px; border-bottom: 1px solid var(--ct-line); }
.voice-recipient .el-form-item { margin-bottom: 0; min-width: 0; }
.voice-recipient > .el-button { margin-top: 28px; padding: 0 4px; }
.recipient-index { margin-top: 36px; font-size: 11px; color: var(--ct-ink-3); font-variant-numeric: tabular-nums; }
.voice-recipient :deep(.el-radio-group) { min-height: 32px; }
.voice-recipient :deep(.el-radio) { margin-right: 12px; }
.voice-recipient :deep(.el-select) { margin-top: 4px; width: 100%; }
.rule-details { color: var(--ct-ink-3); font-size: 12px; line-height: 1.6; }
.rule-details summary { width: fit-content; cursor: pointer; }
.rule-details summary:hover { color: var(--ct-accent); }
.rule-details p { margin: 8px 0 0; }
.recipient-footer { border-top: 1px solid var(--ct-line); margin-top: 12px; padding-top: 12px; }
.recipient-footer .sub-note { margin-bottom: 4px; }
.record-caption { color: var(--ct-ink-3); font-size: 12px; font-weight: 400; }
@media(max-width:1050px) { .voice-settings > .el-form { grid-template-columns: 1fr; } }
</style>
