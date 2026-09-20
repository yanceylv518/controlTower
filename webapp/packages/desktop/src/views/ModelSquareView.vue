<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { ApiError } from '@ct/shared'
import { CopyDocument } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { client, dashboard } from '../api'
import AppShell from '../components/AppShell.vue'
import { useFiltersStore } from '../stores/filters'
import { modelPrice, displayPrice, currencyUnit, expressionTiers, pricingMode, type ModelSquare, type SquareModel, type SquareCurrency } from '../utils/modelSquare'

const filters = useFiltersStore()
const data = ref<ModelSquare | null>(null)
const loading = ref(false)
const error = ref('')
const currency = ref<SquareCurrency | null>(null)
const currencyError = ref('')
const currencyLabel = computed(() => currencyUnit(currency.value))
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
const unit = (item:SquareModel) => `${currencyLabel.value} / ${pricingMode(item)==='request' ? '次' : '1M Tokens'}`
const price = (item:SquareModel, field:'input'|'output'|'cache'|'write') => displayPrice(ratio.value===null ? null : modelPrice(item,field,ratio.value),currency.value)
const tierPrice = (value:number) => displayPrice(ratio.value===null ? null : value * ratio.value,currency.value)
const tierMap = computed(() => new Map((data.value?.items || []).map(item=>[item.model_name,expressionTiers(item.billing_expr)])))
const tiers = (item:SquareModel) => tierMap.value.get(item.model_name) || []
const moneyPrefix = computed(() => currency.value?.symbol || (currency.value?.type === 'TOKENS' ? '额度 ' : ''))
function cardPrices(item:SquareModel) {
  if (pricingMode(item)==='expression') {
    const tier=tiers(item)[0]
    if (!tier) return []
    const main=tier.prices.filter(entry=>['输入','输出','每次调用'].includes(entry.label))
    return (main.length ? main : tier.prices.slice(0,2)).map(entry=>({label:entry.label,value:tierPrice(entry.value),unit:tier.unit==='次'?'次':'1M'}))
  }
  if (pricingMode(item)==='request') return [{label:'每次调用',value:price(item,'input'),unit:'次'}]
  if (pricingMode(item)==='token') return [{label:'输入',value:price(item,'input'),unit:'1M'},{label:'输出',value:price(item,'output'),unit:'1M'}]
  return []
}
function shortList(items:string[] = []) { return items.length ? `${items[0]}${items.length>1 ? ` +${items.length-1}` : ''}` : '—' }
async function copyModel(name:string) {
  try {await navigator.clipboard.writeText(name);ElMessage.success('已复制模型名称')} catch {ElMessage.error('复制失败，请手动复制模型名称')}
}
async function loadCurrency(site:string,id:number) {
  try {
    const result = await dashboard.siteCurrency(site)
    if (id!==sequence) return
    if (!Number.isFinite(result.price_multiplier) || result.price_multiplier<=0 || !result.type) throw new Error('invalid currency')
    currency.value=result;currencyError.value=''
  } catch {
    if (id===sequence) {currency.value=null;currencyError.value='未能读取本站点币种配置，价格暂不显示，请刷新重试。'}
  }
}
async function load(force = false, quiet = false) {
  const id=++sequence, site=filters.site_id
  if (!site) {data.value=null;currency.value=null;currencyError.value='';error.value='';loading.value=false;return}
  if (!quiet) loading.value=true
  const currencyRequest=loadCurrency(site,id)
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
  } finally {await currencyRequest;if(id===sequence) loading.value=false}
}
watch(()=>filters.site_id,()=>{data.value=null;currency.value=null;currencyError.value='';selected.value=null;group.value='';vendor.value='';page.value=1;void load()})
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
      <el-alert v-if="currencyError" :title="currencyError" type="warning" :closable="false" show-icon />
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
            <div class="model-card-heading"><span class="model-avatar" aria-hidden="true">{{ item.model_name.slice(0,1).toUpperCase() }}</span><div><h3>{{ item.model_name }}</h3><small>{{ vendors.get(item.vendor_id || 0) || item.owner_by || 'NewAPI' }}</small></div><el-button class="copy-model" text :icon="CopyDocument" :aria-label="`复制模型名称 ${item.model_name}`" title="复制模型名称" @click="copyModel(item.model_name)" /></div>
            <p class="model-description" :title="item.description">{{ item.description || '暂无描述。' }}</p>
            <div class="card-billing"><span :class="{dynamic:pricingMode(item)==='expression'}">{{ pricingMode(item)==='expression' ? '动态计费' : pricingMode(item)==='token' ? '按量计费' : modeLabels[pricingMode(item)] }}</span><small v-if="pricingMode(item)==='expression' && tiers(item).length" :title="tiers(item)[0].label">{{ tiers(item)[0].label }} · {{ tiers(item).length }} 档</small></div>
            <div class="card-price-grid"><div v-for="entry in cardPrices(item)" :key="entry.label"><small>{{ entry.label }}</small><div><b>{{ entry.value==='—' ? '—' : moneyPrefix+entry.value }}</b><small> / {{ entry.unit }}</small></div></div><p v-if="!cardPrices(item).length">动态规则，查看详情</p></div>
            <div class="card-metadata"><div :title="(item.enable_groups || []).join('、')"><small>分组</small><span>{{ shortList(item.enable_groups) }}</span></div><div :title="(item.supported_endpoint_types || []).join('、')"><small>端点</small><span>{{ shortList(item.supported_endpoint_types) }}</span></div></div>
            <footer><small>{{ currencyLabel }} · {{ group || '基础价格' }}</small><el-button text @click="selected=item">详情 ›</el-button></footer>
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
            <template v-if="pricingMode(selected)==='token'"><el-descriptions-item label="输入 / 输出">{{ price(selected,'input') }} / {{ price(selected,'output') }} {{ currencyLabel }} / 1M</el-descriptions-item><el-descriptions-item label="缓存读取 / 写入">{{ price(selected,'cache') }} / {{ price(selected,'write') }} {{ currencyLabel }} / 1M</el-descriptions-item><el-descriptions-item label="图像 / 音频输入 / 音频输出倍率">{{ selected.image_ratio ?? '—' }} / {{ selected.audio_ratio ?? '—' }} / {{ selected.audio_completion_ratio ?? '—' }}</el-descriptions-item></template>
            <el-descriptions-item v-if="pricingMode(selected)==='request'" label="每次调用">{{ price(selected,'input') }} {{ currencyLabel }}</el-descriptions-item>
          </el-descriptions>
          <section v-if="pricingMode(selected)==='expression'" class="expression-prices">
            <p class="detail-description">各档基础单价已按所选分组换算。命中条件及额外请求倍率见下方完整计费规则，实际费用取决于用量。</p>
            <div v-for="(tier,index) in tiers(selected)" :key="index" class="expression-tier">
              <strong>{{ tier.label }}</strong><small>{{ currencyLabel }} / {{ tier.unit }}</small>
              <div v-for="entry in tier.prices" :key="entry.label" class="expression-price"><span>{{ entry.label }}</span><b>{{ tierPrice(entry.value) }}</b></div>
              <p v-if="!tier.prices.length">此档包含动态运算，无法折算为固定单价。</p>
            </div>
            <p v-if="!tiers(selected).length">此表达式无法拆解为固定档位单价，请查看完整规则。</p>
          </section>
          <template v-if="selected.billing_expr"><h3>计费规则</h3><pre>{{ selected.billing_expr }}</pre></template>
          <p class="detail-description">价格按本站点 new-api 配置以 {{ currencyLabel }} 展示，随所选分组调整。“—”表示配置未加载、未提供或不适用。规则原文中的系数保留原始单位。</p>
        </template>
      </el-drawer>
    </div>
  </AppShell>
</template>

<style scoped>
.expression-prices{display:grid;gap:10px;margin-top:12px;font-size:12px}.expression-tier{display:grid;gap:6px;border-top:1px solid var(--ct-line);padding-top:10px;overflow-wrap:anywhere}.expression-tier>small{color:var(--ct-ink-3)}.expression-price{display:flex;justify-content:space-between;gap:12px;color:var(--ct-ink-2)}.expression-price b{color:var(--ct-ink);font-variant-numeric:tabular-nums}
.square {display:grid;gap:16px}.square-intro.panel {min-height:0;padding:22px;display:flex;align-items:center;justify-content:space-between;gap:24px}.square-intro h2{font-size:20px;margin:0 0 8px}.square-intro p,.detail-description{font-size:13px;color:var(--ct-ink-2);line-height:1.8;margin:0}.square-summary{flex-shrink:0;text-align:right}.square-summary strong{font-size:28px;margin-right:8px}.square-summary small{display:block;margin-top:6px;font-size:11px;line-height:1.8;color:var(--ct-ink-3)}.square-filters{display:grid;grid-template-columns:minmax(220px,1.8fr) repeat(3,minmax(150px,1fr));gap:12px}.model-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(290px,1fr));gap:16px}.model-card.panel{min-height:0;padding:20px;display:flex;flex-direction:column}.model-card-heading{display:flex;align-items:center;gap:10px}.model-card-heading>div{min-width:0;flex:1}.model-card h3{margin:0 0 4px;font-size:14px;overflow-wrap:anywhere}.model-avatar{width:32px;height:32px;display:grid;place-items:center;border-radius:9px;background:var(--ct-accent-weak);color:var(--ct-accent);flex-shrink:0;font-weight:600}.model-card small{font-size:11px;color:var(--ct-ink-3)}.model-description{font-size:12px;line-height:1.7;color:var(--ct-ink-2);min-height:40px;display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden}.model-groups{display:flex;gap:5px;flex-wrap:wrap;min-height:24px;margin-bottom:16px}.model-groups span{font-size:10px;padding:3px 6px;border-radius:4px;background:var(--ct-surface-2);color:var(--ct-ink-2);overflow-wrap:anywhere}.model-prices{display:flex;gap:28px;padding-top:14px;margin-top:auto;border-top:1px solid var(--ct-line)}.model-prices strong{display:block;font-size:22px;font-variant-numeric:tabular-nums;margin-top:4px}.model-special{margin-top:auto;padding:16px 0;font-size:12px;color:var(--ct-ink-2)}footer{display:flex;justify-content:space-between;align-items:center;margin-top:8px}footer .el-button{padding-right:0}.detail-name{overflow-wrap:anywhere}.detail-description{margin:14px 0}pre{white-space:pre-wrap;overflow-wrap:anywhere;background:var(--ct-surface-2);padding:16px;border-radius:8px;font-size:12px} .square :deep(.el-descriptions__content){overflow-wrap:anywhere}@media(max-width:1000px){.square-filters{grid-template-columns:1fr 1fr}.square-intro.panel{align-items:start}.square-summary{max-width:200px}}@media(max-width:600px){.square-intro.panel{flex-direction:column}.square-summary{text-align:left}.square-filters{grid-template-columns:1fr}.model-grid{grid-template-columns:1fr}}
/* Cards summarize prices; complete rules live in the detail drawer. */
.square{gap:12px}
.square-intro.panel{padding:12px 16px;gap:12px;flex-direction:row;align-items:center}
.square-intro h2{font-size:16px;margin:0 0 3px}
.square-intro p{font-size:12px}
.square-summary strong{font-size:22px}
.square-summary span{font-size:12px;color:var(--ct-ink-3)}
.square-filters{gap:8px}
.model-grid{gap:12px;align-items:start;grid-template-columns:repeat(auto-fill,minmax(300px,1fr))}
.model-card.panel{padding:14px;gap:0;box-shadow:none}
.model-card-heading{gap:8px;flex-wrap:wrap}
.model-card-heading>div{min-width:150px}
.model-card-heading :deep(.el-tag){font-size:10px;padding:0 5px;height:20px}
.model-avatar{width:28px;height:28px;border-radius:7px}
.model-description{min-height:0;margin:8px 0 0;line-height:1.5}
.model-groups{flex-wrap:nowrap;overflow:hidden;min-height:0;margin:12px 0;gap:4px}
.model-groups span{white-space:nowrap;overflow:hidden;text-overflow:ellipsis;max-width:120px;flex-shrink:0;font-size:10px;padding:3px 5px}
.model-groups span:nth-child(4){flex-shrink:1;min-width:0}
.model-prices{margin-top:0;padding-top:10px;display:grid;grid-template-columns:1fr 1fr;gap:12px}
.model-prices strong{font-size:20px;overflow-wrap:anywhere}
.model-special{margin-top:0;padding:10px 0}
.card-tiers{border-top:1px solid var(--ct-line);padding-top:10px;display:grid;gap:10px}
.card-tier-heading{display:flex;gap:8px;justify-content:space-between;align-items:baseline}
.card-tier-heading strong{font-size:12px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;min-width:0}
.card-tier-heading small{font-size:10px;white-space:nowrap}
.card-tier-prices{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:8px;margin-top:5px}
.card-tier-prices span{display:flex;gap:5px;align-items:baseline;flex-wrap:wrap}
.card-tier-prices small{font-size:10px}
.card-tier-prices b{font-size:14px;overflow-wrap:anywhere;font-variant-numeric:tabular-nums}
footer{margin-top:10px;gap:8px}
footer .el-button{height:28px;padding:4px 0;font-size:12px}
@media(max-width:600px){.model-grid{grid-template-columns:minmax(0,1fr)}.square-intro.panel{padding:10px 12px}.square-intro p{display:none}.square-summary{white-space:nowrap}.square-filters{grid-template-columns:1fr 1fr}.square-filters>.el-input{grid-column:1 / -1}.model-card.panel{padding:12px}}
.model-grid{grid-template-columns:repeat(3,minmax(0,1fr));gap:14px;align-items:stretch}
.model-card.panel{padding:16px;border-radius:18px;box-shadow:none}
.model-card-heading{flex-wrap:nowrap;gap:10px}.model-card-heading>div{min-width:0}
.model-avatar{width:36px;height:36px;border-radius:12px}
.model-card h3{line-height:1.4;margin-bottom:5px}.model-card small{font-size:12px}
.copy-model.el-button{width:30px;height:30px;padding:5px;flex-shrink:0;margin:0;align-self:flex-start}
.model-description{display:block;font-size:12px;line-height:18px;height:18px;min-height:0;margin:12px 0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:var(--ct-ink-3)}
.card-billing{display:flex;align-items:baseline;justify-content:space-between;gap:8px;font-size:12px;margin-bottom:8px;color:var(--ct-accent)}
.card-billing .dynamic{color:var(--el-color-warning)}.card-billing small{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;min-width:0;font-size:11px}
.card-price-grid{display:grid;grid-template-columns:1fr 1fr;gap:12px;min-height:44px}
.card-price-grid>div{min-width:0}.card-price-grid>div>div{margin-top:5px;line-height:18px;overflow-wrap:anywhere}.card-price-grid b{font-size:14px;font-variant-numeric:tabular-nums}.card-price-grid p{font-size:12px;color:var(--ct-ink-3);margin:0;grid-column:1/-1}
.card-metadata{display:grid;grid-template-columns:1fr 1fr;gap:12px;margin-top:14px;font-size:12px}.card-metadata>div{display:flex;gap:6px;min-width:0}.card-metadata small{flex-shrink:0}.card-metadata span{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
footer{margin-top:12px;padding-top:8px;border-top:1px solid var(--ct-line)}footer>small{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}footer .el-button{flex-shrink:0}
@media(min-width:2100px){.model-grid{grid-template-columns:repeat(4,minmax(0,1fr))}}
@media(max-width:1200px){.model-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.square-filters{grid-template-columns:1fr 1fr}}
@media(max-width:600px){.model-grid{grid-template-columns:minmax(0,1fr);gap:10px}.model-card.panel{padding:14px;border-radius:14px}}
</style>
