<script setup lang="ts">
import { computed } from 'vue'
import type { StatRow } from '../utils/archiveAnalysis'
import { historicalPrices } from '../utils/archiveInsights'
import { currencyUnit, type ArchiveCurrency } from '../utils/archiveMoney'
const props = defineProps<{ rows: StatRow[]; currency?: ArchiveCurrency }>()
const prices = computed(() => historicalPrices(props.rows,props.currency))
const unit = computed(() => currencyUnit(props.currency))
</script>
<template>
  <section class="model-prices">
    <h3>模型历史价格</h3>
    <details><summary>价格口径</summary><p>历史参数按当前站点汇率换算，不重复计入折扣。不同价格分别列出，观测日期不代表有效期。</p></details>
    <el-table :data="prices" row-key="key" max-height="480">
      <el-table-column type="expand"><template #default="{ row }"><pre>{{row.evidenceText}}</pre><p>历史原始参数；缺失项不补算。</p></template></el-table-column>
      <el-table-column prop="model" label="模型" min-width="180"/>
      <el-table-column prop="mode" label="价格信息" min-width="165"/>
      <el-table-column label="观测日期" min-width="185"><template #default="{row}">{{row.first}} ～ {{row.last}}</template></el-table-column>
      <el-table-column label="请求数"><template #default="{row}">{{row.requests.toLocaleString()}}</template></el-table-column>
      <el-table-column prop="input" :label="`输入 ${unit} / 百万 Token`" min-width="165"/>
      <el-table-column prop="output" :label="`输出 ${unit} / 百万 Token`" min-width="165"/>
      <el-table-column prop="cache" :label="`缓存读取 ${unit} / 百万 Token`" min-width="180"/>
      <el-table-column prop="request" :label="`${unit} / 次`" min-width="110"/>
    </el-table>
  </section>
</template>
<style scoped>details{margin:12px 0;color:var(--el-text-color-secondary);font-size:13px}summary{cursor:pointer;width:fit-content}.model-prices{margin-top:24px}.model-prices p{color:var(--el-text-color-secondary);font-size:13px;line-height:1.8}pre{white-space:pre-wrap;overflow-wrap:anywhere;max-height:260px;overflow:auto;padding:12px}</style>
