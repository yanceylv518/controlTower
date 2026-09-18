<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import type { ReadonlyLog } from '@ct/shared'
import { passthrough } from '../api'
import { attemptChannels, buildRequestChain, chainQuery, loadRequestChain, type ChainQuery } from '../utils/fallbackRequestChain'

const props = defineProps<{ row: ReadonlyLog; site: string; sensitive: boolean; money: (quota: number) => string }>()
const emit = defineEmits<{ close: []; filter: [query: ChainQuery]; detail: [row: ReadonlyLog] }>()
const rows = ref<ReadonlyLog[]>([]), loading = ref(false), error = ref(''), truncated = ref(false)
let controller: AbortController | undefined
const query = computed(() => chainQuery(props.site, props.row))
const chain = computed(() => buildRequestChain(rows.value, props.row))
const channelPath = computed(() => attemptChannels(props.row).join(' → '))
const selectedFound = computed(() => rows.value.some(row => row.id === props.row.id))
const formatTime = (time: string) => new Date(time).toLocaleString('zh-CN', { hour12: false })
const outcome = (row: ReadonlyLog) => row.type === 2 ? '消费' : row.type === 5 ? '失败' : row.type === 6 ? '退款' : '关联日志'
const duration = (row: ReadonlyLog) => Number.isFinite(row.use_time) && row.use_time >= 0 ? `${row.use_time.toFixed(1)}s` : '未记录'
async function load() {
  controller?.abort()
  const current = new AbortController()
  controller = current
  rows.value = []; error.value = ''; truncated.value = false
  if (!query.value) { loading.value = false; return }
  loading.value = true
  try {
    const result = await loadRequestChain(query.value, passthrough.logs, current.signal)
    if (current.signal.aborted) return
    rows.value = result.rows; truncated.value = result.truncated
  } catch (reason) {
    if (!current.signal.aborted) error.value = reason instanceof Error ? reason.message : '关联日志加载失败'
  } finally { if (controller === current) loading.value = false }
}
watch(() => [props.site, props.row.id, props.row.request_id, props.row.user_id], load, { immediate: true })
onBeforeUnmount(() => controller?.abort())
</script>

<template>
  <section class="request-chain" aria-label="Fallback 请求链路" :aria-busy="loading">
    <header class="chain-heading">
      <div><strong>请求链路</strong><span class="chain-caption">{{ chain.ordered ? '按记录的渠道尝试顺序' : '按日志时间正序' }}</span><div class="request-id">请求 ID：{{ sensitive ? (row.request_id || '未记录') : '••••' }}</div></div>
      <div class="chain-actions"><button v-if="query" type="button" @click="emit('filter', query)">只看此请求</button><button type="button" @click="emit('close')">收起</button></div>
    </header>
    <p v-if="!query" class="chain-notice">缺少请求 ID 或用户标识，无法可靠关联其他日志。{{ channelPath ? `记录的渠道链：${channelPath}` : '' }}</p>
    <p v-else-if="loading" role="status" class="chain-notice">正在查询此请求的关联日志…</p>
    <div v-else-if="error" class="chain-notice" role="alert">{{ error }} <button type="button" @click="load">重新加载</button></div>
    <template v-else>
      <p v-if="truncated || chain.missing || !selectedFound" class="chain-notice">{{ truncated ? '关联日志超过 1000 条，当前结果已截断。' : '' }}{{ chain.missing ? `有 ${chain.missing} 次渠道尝试未找到对应日志。` : '' }}{{ !selectedFound ? '查询未返回当前记录，日志可能已归档或不在查询范围内。' : '' }}链路可能不完整。</p>
      <p v-if="!chain.ordered && chain.steps.length" class="chain-note">未记录可靠的尝试顺序，以下按日志时间及 ID 排列，不代表实际执行先后。</p>
      <div class="chain-steps">
        <article v-for="(step, index) in chain.steps" :key="step.row?.id ?? `missing-${index}`" class="chain-step" :class="{current: step.row?.id === row.id}">
          <div class="step-heading"><span>{{ step.position ? `${String(step.position).padStart(2, '0')} · ${step.position === 1 ? '首次尝试' : `第 ${step.position - 1} 次重试`}` : `日志 ${index + 1}` }}</span><span :class="{failed: step.row?.type === 5, consumed: step.row?.type === 2}">{{ step.row ? outcome(step.row) : '缺少日志' }}</span></div>
          <div class="step-channel">#{{ step.channel }}<span v-if="step.row?.channel_name"> · {{ step.row.channel_name }}</span></div>
          <template v-if="step.row">
            <div class="step-meta">{{ formatTime(step.row.created_at) }}</div>
            <div class="step-meta">日志耗时 {{ duration(step.row) }} <span v-if="step.row.type === 2">· {{ sensitive ? money(step.row.quota) : '••••' }}</span></div>
            <p v-if="step.row.type === 5" class="step-reason">{{ sensitive ? (step.row.content_summary || step.row.content || '未记录错误原因') : '错误内容已隐藏' }}</p>
            <div class="step-footer"><span v-if="step.row.id === row.id" class="current-label">当前列表记录</span><button type="button" @click="emit('detail', step.row)">查看详情</button></div>
          </template>
          <p v-else class="step-meta">仅有渠道尝试记录，未推断结果及耗时。</p>
        </article>
      </div>
      <div v-if="chain.unplaced?.length || chain.related.length" class="other-logs"><span>其他关联日志（未确定尝试位置）</span><button v-for="item in [...(chain.unplaced || []).map(step => step.row!), ...chain.related]" :key="item.id" type="button" @click="emit('detail', item)">#{{ item.channel_id }} · {{ outcome(item) }} · {{ formatTime(item.created_at) }}</button></div>
      <footer class="chain-foot"><span>同站点 · 同请求 ID · 同用户 · 已关联 {{ rows.length }} 条日志</span><button type="button" @click="load">刷新链路</button></footer>
    </template>
    <div v-if="query" class="chain-note">查询范围：{{ formatTime(query.start_time) }} ～ {{ formatTime(query.end_time) }}；不受列表其他筛选限制。日志耗时沿用源记录，可能为累计值。</div>
  </section>
</template>

<style scoped>
.request-chain{padding:16px 20px;background:var(--rc35-surface-2,#f8fafc);border-left:3px solid var(--rc35-blue,#315edb);color:var(--rc35-ink,#26364e);font-size:12px;line-height:1.6;white-space:normal}
.chain-heading,.chain-actions,.step-heading,.step-footer,.chain-foot{display:flex;align-items:center;justify-content:space-between;gap:10px;flex-wrap:wrap}.chain-heading strong{font-size:13px;font-weight:600}.chain-caption{margin-left:12px;color:var(--rc35-ink-2)}.request-id{margin-top:4px;overflow-wrap:anywhere;font-family:Consolas,monospace;color:var(--rc35-ink-2)}
button{font:inherit;cursor:pointer;border:1px solid var(--rc35-line);border-radius:5px;background:var(--rc35-surface,#fff);color:var(--rc35-blue);padding:4px 9px}button:focus-visible{outline:2px solid var(--rc35-blue);outline-offset:2px}.chain-actions{justify-content:flex-end}.chain-actions button:first-child{background:var(--rc35-blue);color:white;border-color:var(--rc35-blue)}
.chain-steps{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(230px,100%),1fr));gap:12px;margin-top:14px}.chain-step{padding:12px;background:var(--rc35-surface,#fff);border:1px solid var(--rc35-line);border-radius:7px;min-width:0}.chain-step.current{border-color:var(--rc35-blue);box-shadow:inset 0 2px var(--rc35-blue)}.step-heading{color:var(--rc35-ink-2);margin-bottom:8px}.failed{color:var(--rc35-danger,#d05454)}.consumed{color:var(--rc35-green,#158566)}.step-channel{font-weight:500;overflow-wrap:anywhere}.step-meta{margin-top:5px;color:var(--rc35-ink-2);font-variant-numeric:tabular-nums}.step-reason{padding-top:8px;margin:8px 0;border-top:1px solid var(--rc35-line);overflow-wrap:anywhere}.step-footer{margin-top:10px}.step-footer button{margin-left:auto;border:0;padding:0}.current-label{color:var(--rc35-blue);background:var(--rc35-accent-weak);border-radius:4px;padding:1px 6px}.chain-foot{color:var(--rc35-ink-2);margin-top:12px}.chain-foot button{border:0;background:transparent}.chain-note{margin-top:8px;color:var(--rc35-ink-2);font-size:11px}.chain-notice{padding:10px 12px;background:var(--rc35-surface,#fff);border:1px solid var(--rc35-line);border-radius:5px;margin:12px 0}.other-logs{display:flex;gap:8px;flex-wrap:wrap;border-top:1px solid var(--rc35-line);padding-top:12px;margin-top:12px}.other-logs>span{width:100%}
@media(max-width:600px){.request-chain{padding:12px}.chain-caption{display:block;margin-left:0}.chain-actions{width:100%;justify-content:flex-start}}
</style>
