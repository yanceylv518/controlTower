<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { ApiError } from '@ct/shared'
import { client } from '../api'

type TestResult = { status: string; code?: string; tested_at: string; from: number; to: number; request_count?: number; latest_log_time?: number }
type Connection = { endpoint: string; project: string; logstore: string; alb_id: string; access_key_id: string; secret_set: boolean; version: number; last_test?: TestResult }
const defaults = (): Connection => ({ endpoint: '', project: '', logstore: '', alb_id: '', access_key_id: '', secret_set: false, version: 0 })
const form = reactive(defaults())
const secret = ref('')
const ready = ref(false), busy = ref(false), configured = ref(false), error = ref('')
const result = ref<TestResult>(), testedSignature = ref(''), persisted = ref(false)
let sequence = 0
const signature = computed(() => JSON.stringify([form.endpoint, form.project, form.logstore, form.alb_id, form.access_key_id, secret.value, form.version]))
const currentResult = computed(() => testedSignature.value === signature.value ? result.value : undefined)
const messages: Record<string, string> = {
 alb_invalid_config: '请检查公网 Endpoint、Project、Logstore、ALB 实例 ID 和 AccessKey ID。',
 alb_secret_required: '首次配置或更换 AccessKey ID 时，请填写对应的 AccessKey Secret。',
 alb_secret_unavailable: '无法解密已保存凭证，请检查服务器 CT_SECRET_KEY，或重新填写凭证。',
 secret_key_not_configured: '服务器尚未配置 CT_SECRET_KEY，无法加密保存凭证。',
 alb_config_unavailable: '读取 ALB 配置失败，请确认 Server 已升级并完成数据库迁移。',
 alb_config_conflict: '配置已被其他操作更新，请重新加载后修改。',
 alb_config_save_failed: '配置保存失败，请重新加载核对。',
 alb_auth_failed: 'SLS 身份验证或授权失败，请检查 AccessKey、RAM 查询权限及服务器时间。',
 alb_source_not_found: '未找到目标 Project 或 Logstore，请核对名称和地域。',
 alb_query_failed: 'SLS 查询失败，请检查 app_lb_id、request_length 的字段索引及统计配置。',
 alb_network_failed: 'CT 无法连接 SLS 公网端点，请检查地址、DNS、网络及 TLS。',
 alb_timeout: '查询超时，请稍后重试并检查 CT 到 SLS 的网络。',
 alb_rate_limited: 'SLS 查询被限流，请稍后重试。',
 alb_test_busy: '已有连接测试正在运行，请稍后重试。',
 alb_service_failed: 'SLS 服务暂不可用，请稍后重试。',
 alb_invalid_response: 'SLS 返回的数据格式不符合预期，本次未确认连接成功。',
 alb_query_incomplete: 'SLS 查询尚未完成，未将部分结果作为完整统计，请稍后重试。',
}
const statusLabel = computed(() => {
 if (!ready.value) return '连接状态未获取'
 if (!currentResult.value) return configured.value ? '配置已修改，尚未测试' : '尚未配置'
 return ({ success: '连接成功', no_data: '连接成功，暂无日志', incomplete: '查询未完成', failed: '连接失败' } as Record<string, string>)[currentResult.value.status] || '状态未知'
})
const statusType = computed(() => currentResult.value?.status === 'success' ? 'success' : currentResult.value?.status === 'failed' ? 'danger' : 'info')
function message(e: unknown) {
 if (e instanceof ApiError) return messages[e.code] || (e.status === 404 ? '当前 Server 尚未提供 ALB 访问日志接入，请先更新 Server。' : `操作失败（${e.status} · ${e.code}）`)
 return '连接 CT 服务失败，请稍后重试。'
}
function date(value: string | number) {
 return new Date(typeof value === 'number' ? value * 1000 : value).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai' })
}
async function load() {
 const ticket = ++sequence
 busy.value = true; ready.value = false; error.value = ''; secret.value = ''
 result.value = undefined; testedSignature.value = ''
 try {
  const response = await client.request<{ configured: boolean; connection: Connection }>('/api/dashboard/alb-access-log')
  if (ticket !== sequence) return
  Object.assign(form, defaults(), response.connection)
  configured.value = response.configured; ready.value = true
  result.value = form.last_test; persisted.value = true; testedSignature.value = signature.value
 } catch (e) { if (ticket === sequence) error.value = message(e) }
 finally { if (ticket === sequence) busy.value = false }
}
async function submit(save: boolean) {
 if (!ready.value || busy.value) return
 const ticket = ++sequence, before = signature.value
 busy.value = true; error.value = ''
 try {
  const response = await client.request<{ connection: Connection; saved: boolean; persisted: boolean }>(`/api/dashboard/alb-access-log${save ? '' : '/test'}`, {
   method: save ? 'PUT' : 'POST',
   body: JSON.stringify({ endpoint: form.endpoint, project: form.project, logstore: form.logstore, alb_id: form.alb_id, access_key_id: form.access_key_id, access_key_secret: secret.value, version: form.version }),
  })
  if (ticket !== sequence || before !== signature.value) return
  Object.assign(form, response.connection)
  if (response.saved) { secret.value = ''; configured.value = true }
  result.value = response.connection.last_test
  persisted.value = response.persisted
  testedSignature.value = signature.value
  if (save) {
   if (result.value?.status === 'failed' || result.value?.status === 'incomplete') ElMessage.warning('配置已保存，但连接测试尚未通过，请查看原因')
   else ElMessage.success('ALB 访问日志配置已保存')
  }
 } catch (e) {
  if (ticket === sequence) { error.value = message(e); result.value = undefined; testedSignature.value = '' }
 } finally { if (ticket === sequence) busy.value = false }
}
onMounted(load)
onUnmounted(() => { sequence++; secret.value = '' })
</script>

<template>
 <section class="alb-settings">
  <header><div><p class="eyebrow">外部数据源 / ALB 访问日志</p><h2>阿里云 ALB 访问日志接入</h2><p>读取负载均衡访问日志，为请求数量、请求大小、响应耗时和流量统计提供数据。</p></div><el-tag :type="statusType">{{ statusLabel }}</el-tag></header>
  <el-alert v-if="error" :title="error" type="error" show-icon :closable="false" />
  <el-form label-position="top" :disabled="busy || !ready" @submit.prevent>
   <div class="connection-grid">
    <el-form-item label="SLS 公网 Endpoint"><el-input v-model="form.endpoint" placeholder="https://cn-hangzhou.log.aliyuncs.com" maxlength="253" /><span class="hint">从 SLS 项目概览复制所属地域的公网地址。</span></el-form-item>
    <el-form-item label="Project"><el-input v-model="form.project" placeholder="pinducloud-alb-logs" maxlength="63" /></el-form-item>
    <el-form-item label="Logstore"><el-input v-model="form.logstore" placeholder="alb-access-log" maxlength="64" /></el-form-item>
    <el-form-item label="ALB 实例 ID"><el-input v-model="form.alb_id" placeholder="alb-…" maxlength="64" /><span class="hint">包含该 ALB 下所有 Host 的日志。</span></el-form-item>
    <el-form-item label="AccessKey ID"><el-input v-model="form.access_key_id" placeholder="专用 RAM 用户的 AccessKey ID" maxlength="128" autocomplete="off" /></el-form-item>
    <el-form-item label="AccessKey Secret"><el-input v-model="secret" type="password" show-password autocomplete="new-password" maxlength="2048" :placeholder="form.secret_set ? '已配置；留空保留，更换 ID 时需重填' : '输入 AccessKey Secret'" /><span class="hint">加密存入 CT 数据库，保存后不回显。</span></el-form-item>
   </div>
   <div class="actions"><el-button :loading="busy" @click="submit(false)">测试连接</el-button><el-button type="primary" :loading="busy" @click="submit(true)">保存配置</el-button></div>
  </el-form>
  <el-button text :disabled="busy" @click="load">重新加载配置</el-button>
  <section v-if="currentResult" class="test-result">
   <h3>ALB 日志连接状态：{{ statusLabel }}</h3>
   <p>{{ persisted ? '已保存配置的最近测试' : '当前表单测试（配置尚未保存）' }} · {{ date(currentResult.tested_at) }}</p>
   <p v-if="currentResult.code">{{ messages[currentResult.code] || '本次查询未完成，请稍后重试。' }}</p>
   <p v-if="currentResult.status === 'no_data'">最近 15 分钟未查询到匹配日志。请核对 ALB 实例 ID、访问日志投递状态和时间范围。</p>
   <dl v-if="currentResult.request_count !== undefined"><dt>测试窗口请求数</dt><dd>{{ currentResult.request_count.toLocaleString() }}</dd><dt>最新日志时间</dt><dd>{{ currentResult.latest_log_time ? date(currentResult.latest_log_time) : '暂无' }}</dd></dl>
   <p>查询窗口：{{ date(currentResult.from) }} — {{ date(currentResult.to) }}（北京时间）</p>
  </section>
  <p class="scope-note">保存时会再次测试。保存后可在“监控分析 → 请求监控”查看分钟汇总；页面打开时每 30 秒查询，后台共用短时缓存。ALB 访问日志记录已结束的请求，无法据此查看正在排队或进行中的请求。</p>
 </section>
</template>
<style scoped>
.alb-settings{padding:24px;background:var(--el-bg-color);border:1px solid var(--el-border-color-light);border-radius:10px;max-width:1100px}
header{display:flex;justify-content:space-between;gap:16px;align-items:flex-start;margin-bottom:20px}
h2{font-size:18px;margin:4px 0 10px}h3{font-size:14px;margin:0 0 8px}p{font-size:13px;line-height:1.7;color:var(--el-text-color-secondary);margin:6px 0}.eyebrow{font-size:12px;color:var(--el-color-primary)}
.connection-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:0 24px;margin-top:16px}.hint{display:block;font-size:12px;color:var(--el-text-color-secondary);line-height:1.6;margin-top:5px}
.actions{display:flex;gap:8px;flex-wrap:wrap}.test-result{margin-top:16px;padding:16px;background:var(--el-fill-color-light);border-radius:8px}.test-result dl{display:grid;grid-template-columns:auto 1fr;gap:8px 18px;font-size:13px}.test-result dd{margin:0}.scope-note{margin-top:16px}
@media(max-width:700px){.alb-settings{padding:16px}.connection-grid{grid-template-columns:1fr}header{flex-direction:column}.test-result dl{grid-template-columns:1fr}}
</style>
