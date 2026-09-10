<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { ApiError } from '@ct/shared'
import { client } from '../api'
import AppShell from '../components/AppShell.vue'
import { useFiltersStore } from '../stores/filters'

interface Config { reconcile_id?: string; reconcile_date?: string; version: number; instance_id: string; agent_id: string; running: boolean; batch_size: number; interval_seconds: number; delay_seconds: number }
interface Target { instance_id: string; name: string; agent_id: string; configured: boolean; seen_at: string }
interface Day { date: string; archived_rows: string; request_rows: string; error_rows: string; last_id: string; verified_at: string }
interface Item { site_id: string; name: string; enabled: boolean; config: Config; targets: Target[]; days: Day[]; seen_at?: string; status: { supports_daily_check?: boolean; reconciliation?: { id: string; date: string; state: string; source_rows: number; target_rows: number; finished_at?: string; error?: string }; agent_id: string; configured: boolean; applied_version: number; state: string; last_id: string; last_success?: string; verified_at?: string; verified_rows: number; last_batch_rows: number; error: string } }
const filters = useFiltersStore(), item = ref<Item>(), loading = ref(false), failure = ref(''), saving = ref(false), dialog = ref(false), now = ref(Date.now())
const form = reactive<Config>({ version: 0, instance_id: '', agent_id: '', running: false, batch_size: 500, interval_seconds: 30, delay_seconds: 300 })
const editingSite = ref('')
const checkDialog = ref(false), checkDate = ref(''), checkSite = ref('')
const checkLabels: Record<string,string> = { running: '对账中', matched: '明细一致', mismatched: '明细存在差异', failed: '对账失败' }
function openCheck() { checkSite.value = filters.site_id; checkDate.value = ''; checkDialog.value = true }
async function startCheck() { if (!item.value || !checkDate.value || checkSite.value !== filters.site_id) return; await save({ ...item.value.config, running: true, reconcile_date: checkDate.value, reconcile_id: crypto.randomUUID().replaceAll('-', '') }, checkSite.value); checkDialog.value = false }
function exitCheck() { if (item.value) void save({ ...item.value.config, running: false, reconcile_id: '', reconcile_date: '' }, item.value.site_id) }
const beijingDate = () => new Intl.DateTimeFormat('sv-SE', { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date())
const month = ref(beijingDate().slice(0,7))
function count(value: string) { return value.replace(/\B(?=(\d{3})+(?!\d))/g, ',') }
const selection = computed({ get: () => JSON.stringify([form.instance_id, form.agent_id]), set: (value: string) => { const [instance, agent] = JSON.parse(value); form.instance_id = instance; form.agent_id = agent } })
const online = computed(() => !!item.value?.seen_at && now.value - Date.parse(item.value.seen_at) < 90000)
const chosenReady = computed(() => item.value?.targets.some(t => t.instance_id === item.value?.config.instance_id && t.agent_id === item.value?.config.agent_id && t.configured))
const state = computed<{ text: string; type: 'info' | 'success' | 'warning' | 'danger' }>(() => {
 const i = item.value
 if (!i?.config.agent_id) return { text: '待配置', type: 'info' }
 if (!i.seen_at) return { text: '等待 Agent 确认', type: 'warning' }
 if (!online.value) return { text: '离线 · 等待确认', type: 'warning' }
 if (!i.status.configured) return { text: '目标未配置', type: 'warning' }
 if (i.config.version !== i.status.applied_version) return { text: i.config.running ? '启动 / 配置待确认' : '暂停中', type: 'warning' }
 if (i.status.error) return { text: '异常 · 等待重试', type: 'danger' }
 if (i.config.reconcile_id && i.config.running && i.status.state === 'running') return { text: i.status.reconciliation?.id === i.config.reconcile_id ? (checkLabels[i.status.reconciliation.state] || '等待对账') : '等待对账', type: 'info' }
 if (i.config.running && i.status.state === 'running') return { text: '归档中', type: 'success' }
 if (!i.config.running && i.status.state === 'paused') return { text: '已暂停', type: 'info' }
 return { text: '等待 Agent', type: 'warning' }
})
function date(s?: string) { return s ? new Date(s).toLocaleString('zh-CN', { hour12: false }) : '—' }
function errorMessage(e: unknown) { return e instanceof ApiError && e.status === 409 ? '配置已变化、执行节点不属于当前站点或旧执行授权未结束，请刷新后重试' : '请求失败，请检查网络、权限及服务端迁移' }
let sequence = 0
async function load() {
 const site = filters.site_id, ticket = ++sequence
 if (!site) { item.value = undefined; loading.value = false; return }
 loading.value = true
 try { const result = await client.request<{ items: Item[] }>(`/api/dashboard/log-archives?site_id=${encodeURIComponent(site)}&month=${encodeURIComponent(month.value)}`); if (ticket === sequence && site === filters.site_id) { item.value = result.items[0]; failure.value = '' } }
 catch (e) { if (ticket === sequence) failure.value = errorMessage(e) }
 finally { if (ticket === sequence) { loading.value = false; now.value = Date.now() } }
}
function edit() { if (!item.value) return; editingSite.value = item.value.site_id; Object.assign(form, { reconcile_id: "", reconcile_date: "" }, item.value.config); dialog.value = true }
async function save(config: Config, site: string) {
 if (site !== filters.site_id || saving.value) return
 saving.value = true
 try { await client.request(`/api/dashboard/log-archives/${encodeURIComponent(site)}`, { method: 'PUT', body: JSON.stringify(config) }); if (site === filters.site_id) { dialog.value = false; ElMessage.success('站点配置已保存，等待 Agent 确认'); await load() } }
 catch (e) { ElMessage.error(errorMessage(e)) } finally { saving.value = false }
}
function toggle() { if (item.value) void save({ ...item.value.config, running: !item.value.config.running }, item.value.site_id) }
watch(() => filters.site_id, () => { dialog.value = false; checkDialog.value = false; item.value = undefined; failure.value = ''; void load() })
watch(month, () => { if (item.value) item.value.days = []; void load() })
let timer: ReturnType<typeof setInterval> | undefined
onMounted(async () => { try { await filters.loadInstances(); await load() } catch (e) { failure.value = errorMessage(e) }; timer = setInterval(() => { if (!document.hidden && !loading.value) void load() }, 15000) })
onUnmounted(() => { sequence++; if (timer) clearInterval(timer) })
</script>

<template>
 <AppShell title="日志归档">
  <div class="archive-page">
   <section class="intro"><div><h2>{{ filters.site_id || '请先选择站点' }}</h2><p>按日志日期汇总每日任务，Agent 持续增量归档。切换顶部站点查看对应记录。</p></div><el-button :loading="loading" :disabled="!filters.site_id" @click="load">刷新状态</el-button></section>
   <el-alert v-if="failure" :title="failure" type="error" :closable="false" show-icon />
   <el-empty v-if="!filters.site_id" description="请选择一个站点管理日志归档" />
   <el-empty v-else-if="!item && !loading && !failure" description="当前站点没有可用的归档配置" />
   <div v-else-if="!item && loading" class="panel" v-loading="true" style="min-height:180px" />
   <template v-if="item">
    <section class="panel status-panel"><div><div class="status-line"><span class="label">执行状态</span><el-tag :type="state.type" size="small">{{ state.text }}</el-tag><span class="secondary">{{ item.status.configured ? '目标连接已配置' : '目标连接保留在执行 Agent 本地' }}</span></div><p class="secondary">{{ item.config.agent_id ? `执行 Agent：${item.config.agent_id} · 节点：${item.config.instance_id}` : '先选择本站点的执行 Agent，再启用归档' }}</p></div><div class="actions"><el-button v-if="item.config.reconcile_id" @click="exitCheck" :disabled="saving">结束对账模式</el-button><el-button title="先暂停归档并等待新版 Agent 确认后，可开始按日对账" @click="openCheck" :disabled="saving || !online || item.config.running || item.status.state !== 'paused' || item.config.version !== item.status.applied_version || !item.status.supports_daily_check">按日对账</el-button><el-button @click="edit" :disabled="saving">配置策略</el-button><el-button :type="item.config.running ? 'default' : 'primary'" :disabled="saving || (!item.config.running && (!chosenReady || !item.enabled))" @click="toggle">{{ item.config.reconcile_id ? (item.config.running ? '暂停对账' : '继续对账') : (item.config.running ? '暂停归档' : '启用归档') }}</el-button></div></section>
    <div class="metrics"><section class="panel"><span class="label">已提交日志 ID</span><strong>{{ item.status.last_id || '—' }}</strong><span class="secondary">最近成功：{{ date(item.status.last_success) }}</span></section><section class="panel"><span class="label">最近一批</span><strong>{{ item.status.last_batch_rows || 0 }} <small>条</small></strong><span class="secondary">归档明细与日 / 月统计一起提交</span></section><section class="panel"><span class="label">写入校验</span><strong :class="item.status.verified_at ? 'green' : ''">{{ item.status.verified_at ? `${item.status.verified_rows} 条通过` : '尚无结果' }}</strong><span class="secondary">{{ date(item.status.verified_at) }}</span></section></div>
    <el-alert v-if="item.status.error" :title="item.status.error" type="error" :closable="false" show-icon />
    <section v-if="item.status.reconciliation" class="panel"><div class="section-title"><h3>最近一次完整性对账 · {{ item.status.reconciliation.date }}</h3><el-tag :type="item.status.reconciliation.state === 'matched' ? 'success' : 'warning'">{{ checkLabels[item.status.reconciliation.state] }}</el-tag></div><p class="secondary">源库 {{ item.status.reconciliation.source_rows.toLocaleString() }} 条 · 目标库 {{ item.status.reconciliation.target_rows.toLocaleString() }} 条 · {{ date(item.status.reconciliation.finished_at) }}</p><p v-if="item.status.reconciliation.error" class="secondary">{{ item.status.reconciliation.error }}</p><p class="secondary">比较当日原始明细的全部字段，不修改数据或重算统计。对账结束后，退出对账模式再启用归档。</p></section><section class="panel daily-panel"><div class="section-title"><div><h3>每日归档任务</h3><span class="secondary">北京时间 · 同一天持续更新一条记录，不改变归档执行频率</span></div><el-date-picker v-model="month" type="month" value-format="YYYY-MM" format="YYYY 年 MM 月" :clearable="false" aria-label="归档月份" style="width:170px" /></div>
     <el-table :data="item.days || []" row-key="date" empty-text="该月份暂无已上报的归档记录" v-loading="loading">
      <el-table-column label="日志日期" prop="date" min-width="130" />
      <el-table-column label="归档记录" min-width="120"><template #default="{ row }"><el-tag :type="row.date === beijingDate() && state.type === 'success' ? 'success' : 'info'">{{ row.date === beijingDate() && state.type === 'success' ? '持续归档' : '已归档' }}</el-tag></template></el-table-column>
      <el-table-column label="已归档日志" min-width="120" align="right"><template #default="{ row }">{{ count(row.archived_rows) }}</template></el-table-column>
      <el-table-column label="请求日志" min-width="115" align="right"><template #default="{ row }">{{ count(row.request_rows) }}</template></el-table-column>
      <el-table-column label="错误请求" min-width="105" align="right"><template #default="{ row }">{{ count(row.error_rows) }}</template></el-table-column>
      <el-table-column label="该日已提交 ID" prop="last_id" min-width="145" />
      <el-table-column label="最近写入校验" min-width="190"><template #default="{ row }"><span class="green">通过</span><div class="secondary">{{ date(row.verified_at) }}</div></template></el-table-column>
     </el-table><p class="secondary">条数为目标库当日累计值，重试不会重复计数。历史日期仍可追加；“已归档”不表示源库全量对账完成。</p>
    </section>
    <section class="panel"><div class="section-title"><h3>当前站点策略</h3><span class="secondary">每批结束后应用配置，暂停需要 Agent 确认</span></div><div class="policy"><div><span>每批上限</span><b>{{ item.config.batch_size }} 条</b></div><div><span>批次间隔</span><b>{{ item.config.interval_seconds }} 秒</b></div><div><span>归档延迟</span><b>{{ item.config.delay_seconds / 60 }} 分钟</b></div><div><span>存储方式</span><b>月度明细 · 日 / 月统计</b></div></div></section>
    <section class="setup"><h3>执行节点与源库保持一致</h3><p>同一站点的多个节点共用这份配置，只会授权选定的 Agent 归档。请确保该节点连接的是本站点的源库和对应归档库。</p><p>Agent 开启 <code>CT_LOG_ARCHIVE_MANAGED=true</code> 与 <code>CT_LOG_ARCHIVE_ENABLED=true</code> 后，可在“配置策略”中选择。当前发现 {{ item.targets.length }} 个本站点在线 Agent。</p><p>统计和校验只反映已归档数据；本页不查询源库计算进度百分比。</p></section>
   </template>
  </div>
  <el-dialog v-model="checkDialog" title="按日完整性对账" width="min(500px, calc(100vw - 32px))" append-to-body><p class="dialog-note">{{ checkSite }} · 对账期间逐批读取源库和目标月表，使用当前批量及间隔；不会自动修复差异。</p><el-date-picker v-model="checkDate" type="date" value-format="YYYY-MM-DD" :disabled-date="(d: Date) => d.getTime() >= Date.parse(beijingDate() + 'T00:00:00+08:00')" placeholder="选择已结束的日志日期" /><p class="secondary">请先暂停归档并等待 Agent 确认。仅适用于日志保持不变的历史日期，需要源库已有 created_at 开头的索引；日期结束还需超过归档延迟窗口。首次部署需升级 Agent。</p><template #footer><el-button @click="checkDialog=false">取消</el-button><el-button type="primary" :loading="saving" :disabled="!checkDate || checkSite !== filters.site_id" @click="startCheck">开始对账</el-button></template></el-dialog><el-dialog v-model="dialog" title="站点归档策略" width="540px" :close-on-click-modal="false">
   <p class="dialog-note">{{ editingSite }} · 同站点只选择一个执行 Agent</p>
   <el-form label-position="top" @submit.prevent>
    <el-form-item label="执行 Agent"><el-select v-model="selection" placeholder="选择本站点在线 Agent" style="width:100%"><el-option v-for="t in item?.targets || []" :key="JSON.stringify([t.instance_id,t.agent_id])" :value="JSON.stringify([t.instance_id,t.agent_id])" :label="`${t.name || t.instance_id} · ${t.agent_id}${t.configured ? '' : '（目标未配置）'}`" :disabled="!t.configured" /><el-option v-if="form.agent_id && !item?.targets.some(t => t.agent_id === form.agent_id && t.instance_id === form.instance_id)" :value="selection" :label="`${form.agent_id}（离线 / 不可用）`" disabled /></el-select></el-form-item>
    <el-form-item label="每批最多日志数"><el-input-number v-model="form.batch_size" :min="1" :max="5000" :step="100" /></el-form-item>
    <el-form-item label="批次间隔（秒）"><el-input-number v-model="form.interval_seconds" :min="2" :max="3600" /></el-form-item>
    <el-form-item label="归档延迟（秒）"><el-input-number v-model="form.delay_seconds" :min="60" :max="86400" :step="60" /></el-form-item>
   </el-form><p class="secondary">更换执行节点前请先暂停，并等待旧授权结束。新节点需配置相同源库和目标归档库，已有明细会按 ID 去重。</p>
   <template #footer><el-button @click="dialog=false">取消</el-button><el-button type="primary" :loading="saving" :disabled="!form.agent_id || !form.instance_id || editingSite !== filters.site_id" @click="save({...form},editingSite)">保存站点策略</el-button></template>
  </el-dialog>
 </AppShell>
</template>

<style scoped>
.archive-page{min-width:0;display:grid;gap:12px}.intro{display:flex;justify-content:space-between;align-items:center;gap:16px;padding:0}.eyebrow{font-size:11px;letter-spacing:1.5px;color:#64748b}.intro h2{font-size:20px;margin:0 0 4px}.intro p,.setup p,.dialog-note{font-size:13px;line-height:1.8;color:#64748b;margin:0}.panel{min-height:0;min-width:0;background:white;border:1px solid #e2e8f0;border-radius:10px;padding:16px}.status-panel{display:flex;align-items:center;justify-content:space-between;gap:20px}.status-line{display:flex;align-items:center;gap:12px;margin:0}.status-panel p{margin:6px 0 0}.actions{display:flex;flex-shrink:0}.label{font-size:13px;color:#64748b}.secondary{font-size:12px;color:#8490a0;line-height:1.8}.metrics{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:12px}.metrics .panel{display:flex;flex-direction:column;gap:6px}.metrics strong{font-size:22px;font-weight:600;overflow-wrap:anywhere;font-variant-numeric:tabular-nums}.metrics small{font-size:13px;font-weight:400;color:#8490a0}.green{color:#168267}.section-title{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:12px}h3{font-size:15px;margin:0 0 6px}.policy{display:grid;grid-template-columns:repeat(4,1fr);gap:16px}.policy div{display:flex;flex-direction:column;gap:10px}.policy span{font-size:12px;color:#8490a0}.policy b{font-size:14px;font-weight:500}.setup{padding:22px 24px;border:1px solid #e2e8f0;background:#f8fafc;border-radius:12px}.setup p{margin-top:8px}code{font:12px Consolas,monospace}.dialog-note{margin-bottom:20px}@media(max-width:850px){.metrics{grid-template-columns:1fr}.policy{grid-template-columns:repeat(2,1fr)}.status-panel,.section-title{align-items:flex-start;flex-direction:column}.intro{align-items:flex-start}}
</style>
