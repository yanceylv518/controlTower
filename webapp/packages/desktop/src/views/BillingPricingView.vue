<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { ElMessage } from "element-plus";
import type { BillingModelItem } from "@ct/shared";
import { dashboard } from "../api";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import { useAsyncData } from "../composables/useAsyncData";
import { useFiltersStore } from "../stores/filters";

const filters = useFiltersStore();
const query = ref("");
const priceOpen = ref(false);
const modelOpen = ref(false);
const syncing = ref(false);
const selected = ref<BillingModelItem>();
const modelForm = reactive({ model_name: "", max_context_tokens: 0 });
const priceForm = reactive({ model_name: "", effective_from: new Date().toISOString().slice(0, 10), tiers: [{ tier_from: 0, input_price: "0", output_price: "0", cache_price: "0" }] });
const state = useAsyncData(async () => { await filters.loadInstances(); if (!filters.site_id) return { items: [], warning: "" }; return dashboard.billingModels(filters.site_id); });
const visible = computed(() => (state.data.value?.items || []).filter((item) => item.model_name.toLowerCase().includes(query.value.trim().toLowerCase())));
const configured = computed(() => (state.data.value?.items || []).filter((item) => item.price_source === "ct").length);
const formatTokens = (value: number) => value > 0 ? Number(value).toLocaleString() : "未设置";
const formatPrice = (value: string) => value ? Number(value).toFixed(6) : "—";

function editModel(item: BillingModelItem) { selected.value=item; modelForm.model_name=item.model_name; modelForm.max_context_tokens=item.max_context_tokens || 0; modelOpen.value=true; }
async function saveModel() { await dashboard.saveBillingModel({ instance_id: filters.site_id, model_name:modelForm.model_name, max_context_tokens:Number(modelForm.max_context_tokens) }); modelOpen.value=false; ElMessage.success("最大上下文长度已保存"); await state.reload(); }
async function syncModels(){syncing.value=true;try{const result=await dashboard.syncBillingModels(filters.site_id);ElMessage.success(`已同步 ${result.models} 个模型，更新 ${result.prices_changed} 个价格`);await state.reload();}finally{syncing.value=false;}}
function editPrice(item: BillingModelItem) { selected.value=item; priceForm.model_name=item.model_name; priceForm.effective_from=new Date().toISOString().slice(0,10); priceForm.tiers=[{tier_from:0,input_price:item.input_price||"0",output_price:item.output_price||"0",cache_price:item.cache_price||"0"}]; priceOpen.value=true; }
function addTier(){const last=priceForm.tiers.at(-1)!;priceForm.tiers.push({tier_from:Number(last.tier_from)+1,input_price:last.input_price,output_price:last.output_price,cache_price:last.cache_price});}
function removeTier(index:number){if(priceForm.tiers.length>1)priceForm.tiers.splice(index,1);}
async function savePrice(){if(priceForm.tiers[0]?.tier_from!==0||priceForm.tiers.some((v,i)=>i>0&&v.tier_from<=priceForm.tiers[i-1]!.tier_from)){ElMessage.error("首档必须为 0，后续档位必须严格递增");return;}await dashboard.saveBillingPrice({instance_id:filters.site_id,model_name:priceForm.model_name,effective_from:priceForm.effective_from,tiers:priceForm.tiers});priceOpen.value=false;ElMessage.success("新价格已保存");await state.reload();}
watch(()=>filters.site_id,()=>void state.reload());
void state.reload();
</script>

<template>
  <AppShell title="模型管理">
    <template #tools><el-input v-model="query" clearable placeholder="搜索模型" style="width:240px"/><el-button :loading="syncing" @click="syncModels">刷新模型</el-button></template>
    <div class="summary"><div><small>站点模型</small><b>{{ state.data.value?.items.length || 0 }}</b></div><div><small>已配置 CT 价格</small><b>{{ configured }}</b></div><div><small>待补充最大长度</small><b>{{ (state.data.value?.items || []).filter(x=>!x.max_context_tokens).length }}</b></div></div>
    <el-alert v-if="state.data.value?.warning" type="warning" :closable="false" title="当前无法从 new-api 刷新模型，以下仍展示 CT 已保存的数据" show-icon/>
    <AsyncPanel :loading="state.loading.value" :error="state.error.value" :empty="!visible.length" @retry="state.reload">
      <el-table :data="visible" row-key="model_name">
        <el-table-column prop="model_name" label="模型" min-width="230"><template #default="s"><b>{{ s.row.model_name }}</b></template></el-table-column>
        <el-table-column label="最大上下文长度" min-width="160"><template #default="s"><span :class="{muted:!s.row.max_context_tokens}">{{ formatTokens(s.row.max_context_tokens) }}</span></template></el-table-column>
        <el-table-column label="输入价格" min-width="125"><template #default="s">{{ formatPrice(s.row.input_price) }}</template></el-table-column>
        <el-table-column label="缓存价格" min-width="125"><template #default="s">{{ formatPrice(s.row.cache_price) }}</template></el-table-column>
        <el-table-column label="输出价格" min-width="125"><template #default="s">{{ formatPrice(s.row.output_price) }}</template></el-table-column>
        <el-table-column label="价格来源" width="125"><template #default="s"><el-tag v-if="s.row.price_source" :type="s.row.price_source==='ct'?'success':'info'">{{ s.row.price_source==='ct'?'CT 配置':'new-api' }}</el-tag><el-tag v-else type="warning">未配置</el-tag></template></el-table-column>
        <el-table-column label="生效日期" width="120"><template #default="s">{{ s.row.effective_from || '—' }}</template></el-table-column>
        <el-table-column label="操作" width="170" fixed="right"><template #default="s"><el-button link type="primary" @click="editModel(s.row)">设置长度</el-button><el-button link type="primary" @click="editPrice(s.row)">设置价格</el-button></template></el-table-column>
      </el-table>
    </AsyncPanel>
    <el-dialog v-model="modelOpen" title="模型信息" width="480px"><el-form label-width="130px"><el-form-item label="模型"><el-input :model-value="modelForm.model_name" disabled/></el-form-item><el-form-item label="最大上下文 Token"><el-input-number v-model="modelForm.max_context_tokens" :min="0" :step="1024" :controls="false" style="width:100%"/><div class="help">填 0 表示未知或暂不限制。</div></el-form-item></el-form><template #footer><el-button @click="modelOpen=false">取消</el-button><el-button type="primary" @click="saveModel">保存</el-button></template></el-dialog>
    <el-dialog v-model="priceOpen" title="设置模型价格" width="780px"><el-form label-width="100px"><el-form-item label="模型"><el-input :model-value="priceForm.model_name" disabled/></el-form-item><el-form-item label="生效日期"><el-date-picker v-model="priceForm.effective_from" type="date" value-format="YYYY-MM-DD"/></el-form-item><el-form-item label="阶梯价格"><div class="tiers"><div v-for="(tier,index) in priceForm.tiers" :key="index" class="tier"><el-input-number v-model="tier.tier_from" :min="0" :controls="false" placeholder="档位下限"/><el-input v-model="tier.input_price" placeholder="输入/1M"/><el-input v-model="tier.cache_price" placeholder="缓存/1M"/><el-input v-model="tier.output_price" placeholder="输出/1M"/><el-button text type="danger" :disabled="priceForm.tiers.length===1" @click="removeTier(index)">删除</el-button></div><el-button @click="addTier">添加阶梯</el-button></div></el-form-item></el-form><template #footer><el-button @click="priceOpen=false">取消</el-button><el-button type="primary" @click="savePrice">保存新价格</el-button></template></el-dialog>
  </AppShell>
</template>
<style scoped>.summary{display:grid;grid-template-columns:repeat(3,minmax(180px,240px));gap:12px;margin-bottom:12px}.summary>div{display:flex;flex-direction:column;gap:5px;padding:14px 18px;border:1px solid var(--el-border-color-light);border-radius:8px;background:#fff}.summary small,.muted,.help{color:var(--el-text-color-secondary)}.summary b{font-size:24px}.tiers{width:100%}.tier{display:grid;grid-template-columns:130px 1fr 1fr 1fr 60px;gap:8px;margin-bottom:8px}.help{margin-top:6px;font-size:12px}</style>
