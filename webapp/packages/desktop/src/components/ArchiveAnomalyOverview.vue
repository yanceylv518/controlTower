<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { ApiError } from '@ct/shared'
import { client } from '../api'
import { anomalySummary, percent } from '../utils/archiveInsights'
import TrendChart from './TrendChart.vue'
const props = defineProps<{ query: Record<string,string> }>()
const data = ref<ReturnType<typeof anomalySummary>>(), busy = ref(false), error = ref(''), observedAt = ref('')
let sequence = 0
async function load() {
  const ticket = ++sequence, query = { ...props.query }
  data.value = undefined; error.value = ''; busy.value = true
  try {
    const page = await client.request<{items: Record<string,unknown>[];has_more:boolean}>(`/api/dashboard/log-archive-read/anomalies?${new URLSearchParams(query)}`)
    if (ticket !== sequence) return
    if (page.has_more) throw new Error('统计返回不完整，请重试')
    data.value = anomalySummary(page.items)
    observedAt.value = new Date().toLocaleTimeString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false })
  } catch (e) {
    if (ticket === sequence) error.value = e instanceof ApiError ? `全天统计读取失败（${e.status} · ${e.code}），请确认 Server 已升级；可重试或缩小筛选范围。` : e instanceof Error ? e.message : '统计读取失败'
  } finally { if (ticket === sequence) busy.value = false }
}
watch(() => props.query, () => void load(), { immediate: true }); onUnmounted(() => sequence++)
const series = computed(() => [
  {key:'empty_output' as const,name:'空输出',color:'#d69230'},
  {key:'missing_output' as const,name:'输出缺失',color:'#8994a5'},
  {key:'error' as const,name:'错误日志',color:'#d75252'},
].map(item => ({name:item.name,color:item.color,type:'bar' as const,data:(data.value?.hours ?? []).map(row => [`${props.query.date}T${String(row.hour).padStart(2,'0')}:00:00+08:00`, Number(row[item.key])] as [string,number])})))
</script>
<template>
  <section class="anomaly-overview" aria-label="当日归档异常统计">
    <p v-if="busy">正在汇总当日归档记录…</p>
    <el-alert v-if="error" :title="error" type="error" :closable="false"><el-button link @click="load">重试统计</el-button></el-alert>
    <template v-if="data">
      <div class="kpis">
        <div><span>消费请求</span><strong>{{data.total.consumption.toLocaleString()}}</strong></div>
        <div><span>空输出请求</span><strong>{{data.total.empty_output.toLocaleString()}}</strong><small>占消费请求 {{percent(data.total.empty_output,data.total.consumption)}}</small></div>
        <div><span>输出 Token 缺失</span><strong>{{data.total.missing_output.toLocaleString()}}</strong></div>
        <div><span>错误日志</span><strong>{{data.total.error.toLocaleString()}}</strong></div>
        <div><span>有扣费的空输出</span><strong>{{data.total.charged_empty_output.toLocaleString()}}</strong></div>
      </div>
      <p class="note">{{query.date}} · 北京时间 · {{observedAt}} 更新</p>
      <TrendChart title="当日异常小时趋势（北京时间）" :series="series"/>
    </template>
  </section>
</template>
<style scoped>.kpis{display:grid;grid-template-columns:repeat(5,minmax(0,1fr));gap:16px;margin-top:20px}.kpis div{display:flex;flex-direction:column;gap:6px}.kpis strong{font-size:23px}.note,small{font-size:12px;color:var(--el-text-color-secondary);line-height:1.8}@media(max-width:900px){.kpis{grid-template-columns:repeat(2,minmax(0,1fr))}}</style>
