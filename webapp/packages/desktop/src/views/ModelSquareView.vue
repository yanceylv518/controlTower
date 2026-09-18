<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { ApiError } from '@ct/shared'
import { client } from '../api'
import AppShell from '../components/AppShell.vue'
import { useFiltersStore } from '../stores/filters'
import { modelPrice, priceText, pricingMode, type ModelSquare, type SquareModel } from '../utils/modelSquare'

const filters = useFiltersStore()
const data = ref<ModelSquare | null>(null)
const loading = ref(false)
const error = ref('')
const query = ref(''), group = ref(''), vendor = ref<number | ''>(''), mode = ref('')
const selected = ref<SquareModel | null>(null)
const page = ref(1)
let sequence = 0
let disposed = false
let timer: ReturnType<typeof setInterval> | undefined
const vendors = computed(() => new Map((data.value?.vendors || []).map(v => [v.id,v.name])))
const modeLabels = {token:'按 Token',request:'按次',expression:'表达式计费',unknown:'其他计费'}
const ratio = computed(() => group.value ? data.value?.group_ratios[group.value] ?? null : 1)
const groups = computed(() => Object.entries(data.value?.group_ratios || {}).sort(([a],[b])=>a.localeCompare(b)))
const visible = computed(() => (data.value?.items || []).filter(item => {
  const term=query.value.trim().toLowerCase()
  return (!term || [item.model_name,item.description,item.tags].some(text=>text?.toLowerCase().includes(term)))
    && (!group.value || (item.enable_groups || []).includes(group.value) || (item.enable_groups || []).includes('all'))
    && (vendor.value === '' || item.vendor_id === vendor.value)
    && (!mode.value || pricingMode(item) === mode.value)
}))
const paged = computed(() => visible.value.slice((page.value-1)*24,page.value*24))
const unit = (item:SquareModel) => pricingMode(item)==='request' ? 'USD / 次' : 'USD / 1M Tokens'
const price = (item:SquareModel, field:'input'|'output'|'cache'|'write') => priceText(ratio.value===null ? null : modelPrice(item,field,ratio.value))
async function load(force = false, quiet = false) {
  const id=++sequence, site=filters.site_id
  if (!site) {data.value=null;error.value='';loading.value=false;return}
  if (!quiet) loading.value=true
  try {
    const result=await client.request<ModelSquare>(`/api/dashboard/model-square?instance_id=${encodeURIComponent(site)}`,force?{method:'POST'}:undefined)
    if (id!==sequence) return
    data.value=result;error.value=''
    if (selected.value) selected.value=result.items.find(item=>item.model_name===selected.value?.model_name) || null
    if (group.value && !(group.value in result.group_ratios)) group.value=''
    if (vendor.value!=='' && !(result.vendors || []).some(v=>v.id===vendor.value)) vendor.value=''
  } catch (e) {
    if (id!==sequence) return
    error.value=e instanceof ApiError && e.status===422 ? '本站点尚未配置 NewAPI API 地址，请在实例管理中配置。'
      : e instanceof ApiError && e.status===404 ? '当前 Server 尚未支持模型广场，请升级后使用。'
      : '无法读取 NewAPI 模型广场，请检查站点 API 地址、访问权限和网络连接。'
  } finally {if(id===sequence) loading.value=false}
}
watch(()=>filters.site_id,()=>{data.value=null;selected.value=null;group.value='';vendor.value='';page.value=1;void load()})
watch([query,group,vendor,mode],()=>{page.value=1})
watch(()=>visible.value.length,()=>{page.value=Math.min(page.value,Math.max(1,Math.ceil(visible.value.length/24)))})
const refresh = () => {if(document.visibilityState==='visible' && !loading.value) void load(false,true)}
onMounted(async()=>{
  try {await filters.loadInstances()} catch {error.value='无法加载站点列表，请稍后刷新';return}
  if(disposed) return
  await load()
  if(disposed) return
  timer=setInterval(refresh,60000);window.addEventListener('focus',refresh)
})
onUnmounted(()=>{disposed=true;sequence++;clearInterval(timer);window.removeEventListener('focus',refresh)})
</script>

<template>
  <AppShell title="模型广场">
    <template #tools><el-button :loading="loading" :disabled="!filters.site_id" @click="load(true)">刷新</el-button></template>
    <div class="square">
      <section class="square-intro panel">
        <div><h2>发现可用模型</h2><p>浏览模型能力与价格，找到适合业务的模型。</p></div>
        <div class="square-summary"><strong>{{ data?.items.length ?? '—' }}</strong><span>个模型</span></div>
      </section>
      <el-alert v-if="error" :title="error" type="warning" :closable="false" show-icon />
      <el-alert v-else-if="data?.warning" title="暂时无法更新，当前显示最近一次可用数据。" type="warning" :closable="false" show-icon />
      <div class="square-filters">
        <el-input v-model="query" placeholder="搜索模型、说明或标签" aria-label="搜索模型" clearable />
        <el-select v-model="group" placeholder="全部分组 · 基础价格" aria-label="模型分组"><el-option label="全部分组 · 基础价格" value=""/><el-option v-for="[name,value] in groups" :key="name" :label="`${name} · ${value}×`" :value="name"/></el-select>
        <el-select v-model="vendor" placeholder="全部厂商" aria-label="模型厂商"><el-option label="全部厂商" value=""/><el-option v-for="item in data?.vendors || []" :key="item.id" :label="item.name" :value="item.id"/></el-select>
        <el-select v-model="mode" placeholder="全部计费方式" aria-label="计费方式"><el-option label="全部计费方式" value=""/><el-option v-for="(label,key) in modeLabels" :key="key" :label="label" :value="key"/></el-select>
      </div>
      <div v-loading="loading" class="square-results">
        <el-empty v-if="!filters.site_id" description="请先配置并选择站点" />
        <el-empty v-else-if="!data && !loading" description="暂时无法加载模型，请稍后刷新" />
        <el-empty v-else-if="data && !visible.length" :description="data.items.length ? '没有匹配的模型' : '暂无可用模型'" />
        <div class="model-grid">
          <article v-for="item in paged" :key="item.model_name" class="model-card panel">
            <div class="model-card-heading"><span class="model-avatar" aria-hidden="true">{{ item.model_name.slice(0,1).toUpperCase() }}</span><div><h3>{{ item.model_name }}</h3><small>{{ vendors.get(item.vendor_id || 0) || item.owner_by || 'NewAPI' }}</small></div><el-tag size="small" effect="plain">{{ modeLabels[pricingMode(item)] }}</el-tag></div>
            <p class="model-description">{{ item.description || '暂无模型说明' }}</p>
            <div class="model-groups"><span v-for="name in (item.enable_groups || []).slice(0,4)" :key="name">{{ name }}</span><span v-if="(item.enable_groups || []).length>4">+{{ item.enable_groups.length-4 }}</span></div>
            <div v-if="pricingMode(item)==='token' || pricingMode(item)==='request'" class="model-prices"><div><small>{{ pricingMode(item)==='request'?'每次调用':'输入' }}</small><strong>{{ price(item,'input') }}</strong></div><div v-if="pricingMode(item)==='token'"><small>输出</small><strong>{{ price(item,'output') }}</strong></div></div>
            <div v-else class="model-special">{{ pricingMode(item)==='expression'?'按用量规则计费，查看详情':'查看详情了解计费规则' }}</div>
            <footer><small>{{ ['token','request'].includes(pricingMode(item)) ? unit(item) : '自定义计费' }}</small><el-button text @click="selected=item">查看详情</el-button></footer>
          </article>
        </div>
      </div>
      <el-pagination v-if="visible.length>24" v-model:current-page="page" :total="visible.length" :page-size="24" layout="total, prev, pager, next" />
      <el-drawer :model-value="!!selected" title="模型详情" size="520px" style="max-width:100vw" @close="selected=null">
        <template v-if="selected"><h2 class="detail-name">{{ selected.model_name }}</h2><p class="detail-description">{{ selected.description || '暂无模型说明' }}</p>
          <el-descriptions :column="1" border>
            <el-descriptions-item label="厂商">{{ vendors.get(selected.vendor_id || 0) || selected.owner_by || '—' }}</el-descriptions-item>
            <el-descriptions-item label="标签">{{ selected.tags || '—' }}</el-descriptions-item>
            <el-descriptions-item label="计费方式">{{ modeLabels[pricingMode(selected)] }}</el-descriptions-item>
            <el-descriptions-item label="可用分组">{{ (selected.enable_groups || []).join('、') || '—' }}</el-descriptions-item>
            <el-descriptions-item label="端点类型">{{ (selected.supported_endpoint_types || []).join('、') || '—' }}</el-descriptions-item>
            <template v-if="pricingMode(selected)==='token'"><el-descriptions-item label="输入 / 输出">{{ price(selected,'input') }} / {{ price(selected,'output') }} USD / 1M</el-descriptions-item><el-descriptions-item label="缓存读取 / 写入">{{ price(selected,'cache') }} / {{ price(selected,'write') }} USD / 1M</el-descriptions-item><el-descriptions-item label="图像 / 音频输入 / 音频输出倍率">{{ selected.image_ratio ?? '—' }} / {{ selected.audio_ratio ?? '—' }} / {{ selected.audio_completion_ratio ?? '—' }}</el-descriptions-item></template>
            <el-descriptions-item v-if="pricingMode(selected)==='request'" label="每次调用">{{ price(selected,'input') }} USD</el-descriptions-item>
          </el-descriptions>
          <template v-if="selected.billing_expr"><h3>计费规则</h3><pre>{{ selected.billing_expr }}</pre></template>
          <p class="detail-description">价格以 USD 展示，随所选分组调整。“—”表示未提供或不适用。</p>
        </template>
      </el-drawer>
    </div>
  </AppShell>
</template>

<style scoped>
.square {display:grid;gap:16px}.square-intro.panel {min-height:0;padding:22px;display:flex;align-items:center;justify-content:space-between;gap:24px}.square-intro h2{font-size:20px;margin:0 0 8px}.square-intro p,.detail-description{font-size:13px;color:var(--ct-ink-2);line-height:1.8;margin:0}.square-summary{flex-shrink:0;text-align:right}.square-summary strong{font-size:28px;margin-right:8px}.square-summary small{display:block;margin-top:6px;font-size:11px;line-height:1.8;color:var(--ct-ink-3)}.square-filters{display:grid;grid-template-columns:minmax(220px,1.8fr) repeat(3,minmax(150px,1fr));gap:12px}.model-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(290px,1fr));gap:16px}.model-card.panel{min-height:0;padding:20px;display:flex;flex-direction:column}.model-card-heading{display:flex;align-items:center;gap:10px}.model-card-heading>div{min-width:0;flex:1}.model-card h3{margin:0 0 4px;font-size:14px;overflow-wrap:anywhere}.model-avatar{width:32px;height:32px;display:grid;place-items:center;border-radius:9px;background:var(--ct-accent-weak);color:var(--ct-accent);flex-shrink:0;font-weight:600}.model-card small{font-size:11px;color:var(--ct-ink-3)}.model-description{font-size:12px;line-height:1.7;color:var(--ct-ink-2);min-height:40px;display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden}.model-groups{display:flex;gap:5px;flex-wrap:wrap;min-height:24px;margin-bottom:16px}.model-groups span{font-size:10px;padding:3px 6px;border-radius:4px;background:var(--ct-surface-2);color:var(--ct-ink-2);overflow-wrap:anywhere}.model-prices{display:flex;gap:28px;padding-top:14px;margin-top:auto;border-top:1px solid var(--ct-line)}.model-prices strong{display:block;font-size:22px;font-variant-numeric:tabular-nums;margin-top:4px}.model-special{margin-top:auto;padding:16px 0;font-size:12px;color:var(--ct-ink-2)}footer{display:flex;justify-content:space-between;align-items:center;margin-top:8px}footer .el-button{padding-right:0}.detail-name{overflow-wrap:anywhere}.detail-description{margin:14px 0}pre{white-space:pre-wrap;overflow-wrap:anywhere;background:var(--ct-surface-2);padding:16px;border-radius:8px;font-size:12px} .square :deep(.el-descriptions__content){overflow-wrap:anywhere}@media(max-width:1000px){.square-filters{grid-template-columns:1fr 1fr}.square-intro.panel{align-items:start}.square-summary{max-width:200px}}@media(max-width:600px){.square-intro.panel{flex-direction:column}.square-summary{text-align:left}.square-filters{grid-template-columns:1fr}.model-grid{grid-template-columns:1fr}}
</style>
