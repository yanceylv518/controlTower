<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { NginxSlowSample, NginxTimingResponse } from '@ct/shared'
import { dashboard } from '../api'
import { useFiltersStore } from '../stores/filters'
import { useAsyncData } from '../composables/useAsyncData'
import AppShell from '../components/AppShell.vue'
import AsyncPanel from '../components/AsyncPanel.vue'
import HoursSelect from '../components/HoursSelect.vue'
import MetricMini from '../components/MetricMini.vue'
import TrendChart, { type TrendSeries } from '../components/TrendChart.vue'

const filters = useFiltersStore()
const hours = ref(1)
const emptySummary = () => ({ total_requests: 0, status_5xx: 0, status_504: 0, slow_count: 0, slow_ttft_count: 0, slow_transfer_count: 0, slow_ttft_percent: 0, slow_transfer_percent: 0 })
const timing = ref<NginxTimingResponse>({ items: [], summary: emptySummary() })
const samples = ref<NginxSlowSample[]>([])
const state = useAsyncData(async () => {
  if (!filters.instance_id) { timing.value = { items: [], summary: emptySummary() }; samples.value = []; return false }
  const [timingResponse, sampleResponse] = await Promise.all([dashboard.nginxTiming({ instance_id: filters.instance_id, hours: hours.value }), dashboard.nginxSlowSamples({ instance_id: filters.instance_id, hours: hours.value, limit: 50 })])
  timing.value = timingResponse; samples.value = sampleResponse.items; return timingResponse.items.length > 0
})
watch(() => [filters.instance_id, hours.value], () => void state.reload(), { immediate: true })
const points = (field: keyof NginxTimingResponse['items'][number]) => timing.value.items.map(item => [item.bucket_at, Number(item[field])] as [string, number])
const ttft = computed<TrendSeries[]>(() => [{ name: 'TTFT P50', color: '#36a2eb', unit: 's', data: points('uht_p50') }, { name: 'TTFT P95', color: '#ff9f43', unit: 's', data: points('uht_p95') }])
const transfer = computed<TrendSeries[]>(() => [{ name: '传输 P50', color: '#17a2b8', unit: 's', data: points('transfer_p50') }, { name: '传输 P95', color: '#9b59b6', unit: 's', data: points('transfer_p95') }])
const volume = computed<TrendSeries[]>(() => [{ name: '请求量', color: '#246bfe', type: 'bar', data: points('request_count') }, { name: '5xx', color: '#f56c6c', data: points('status_5xx') }, { name: '504', color: '#e6a23c', data: points('status_504') }])
const formatBytes = (value: number) => value < 1024 ? `${value} B` : value < 1048576 ? `${(value / 1024).toFixed(1)} KiB` : `${(value / 1048576).toFixed(1)} MiB`
</script>

<template><AppShell title="延时分诊"><div class="latency-toolbar"><span>实例：{{ filters.instance_id || '请选择实例' }}</span><HoursSelect v-model="hours" /></div><AsyncPanel :loading="state.loading.value" :error="state.error.value" :empty="!timing.items.length" @retry="state.reload"><template #empty><el-empty description="未启用 Nginx timing 采集，配置 CT_NGINX_ACCESS_LOG 后生效" /></template><div class="latency-cards"><MetricMini label="慢请求" :value="timing.summary.slow_count"/><MetricMini label="首字节段主导" :value="`${timing.summary.slow_ttft_count} / ${timing.summary.slow_ttft_percent.toFixed(1)}%`"/><MetricMini label="传输段主导" :value="`${timing.summary.slow_transfer_count} / ${timing.summary.slow_transfer_percent.toFixed(1)}%`"/><MetricMini label="5xx / 504" :value="`${timing.summary.status_5xx} / ${timing.summary.status_504}`"/></div><div class="latency-trends"><TrendChart title="TTFT（首字节段）" :series="ttft"/><TrendChart title="响应传输段" :series="transfer"/><TrendChart title="请求量与 5xx / 504" :series="volume"/></div><section class="panel latency-table"><h3>慢样本</h3><el-table :data="samples"><el-table-column prop="occurred_at" label="时间" width="190"/><el-table-column prop="path" label="Path" min-width="240" show-overflow-tooltip/><el-table-column prop="status" label="状态" width="80"/><el-table-column label="RT" width="100"><template #default="{row}"><span :class="{hot:row.rt>=10}">{{row.rt.toFixed(3)}}s</span></template></el-table-column><el-table-column label="UHT" width="100"><template #default="{row}"><span :class="{hot:row.uht>=5}">{{row.uht.toFixed(3)}}s</span></template></el-table-column><el-table-column prop="urt" label="URT(s)" width="100"/><el-table-column label="Bytes" width="110"><template #default="{row}">{{formatBytes(row.bytes)}}</template></el-table-column></el-table></section></AsyncPanel></AppShell></template>
