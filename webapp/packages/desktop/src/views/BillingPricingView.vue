<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { ElMessage } from "element-plus";
import type { BillingPriceItem } from "@ct/shared";
import { dashboard } from "../api";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import { useAsyncData } from "../composables/useAsyncData";
import { useFiltersStore } from "../stores/filters";

const filters = useFiltersStore();
const tab = ref("prices");
const priceOpen = ref(false);
const ratioOpen = ref(false);
const importing = ref(false);
const quotaPerUnit = ref("");
const suggestions = ref<BillingPriceItem[]>([]);
const priceForm = reactive({ model_name: "", effective_from: new Date().toISOString().slice(0, 10), tiers: [{ tier_from: 0, input_price: "0", output_price: "0", cache_price: "0" }] });
const ratioForm = reactive({ group_name: "", ratio: "1" });
const state = useAsyncData(async () => { await filters.loadInstances(); if (!filters.site_id) return { prices: [], ratios: [] }; const [prices, ratios] = await Promise.all([dashboard.billingPrices(filters.site_id), dashboard.billingGroupRatios(filters.site_id)]); return { prices: prices.items, ratios: ratios.items }; });
const currentModels = computed(() => new Set((state.data.value?.prices || []).map((item) => item.model_name)));
const unconfiguredSuggestions = computed(() => suggestions.value.filter((item) => !currentModels.value.has(item.model_name)));

function resetPrice(item?: BillingPriceItem) {
  priceForm.model_name = item?.model_name || "";
  priceForm.effective_from = new Date().toISOString().slice(0, 10);
  priceForm.tiers = [{ tier_from: 0, input_price: item?.input_price || "0", output_price: item?.output_price || "0", cache_price: item?.cache_price || "0" }];
  priceOpen.value = true;
}
function addTier() { const last = priceForm.tiers.at(-1); priceForm.tiers.push({ tier_from: Number(last?.tier_from || 0) + 1, input_price: last?.input_price || "0", output_price: last?.output_price || "0", cache_price: last?.cache_price || "0" }); }
function removeTier(index: number) { if (priceForm.tiers.length > 1) priceForm.tiers.splice(index, 1); }
async function savePrice() {
  if (!priceForm.model_name.trim() || priceForm.tiers[0]?.tier_from !== 0 || priceForm.tiers.some((tier, index) => index > 0 && tier.tier_from <= priceForm.tiers[index - 1]!.tier_from)) { ElMessage.error("模型不能为空；首档必须为 0，后续档位必须严格递增"); return; }
  await dashboard.saveBillingPrice({ instance_id: filters.site_id, model_name: priceForm.model_name.trim(), effective_from: priceForm.effective_from, tiers: priceForm.tiers.map((tier) => ({ ...tier, tier_from: Number(tier.tier_from) })) });
  priceOpen.value = false; ElMessage.success("价格已按新生效日期保存，历史价格未修改"); await state.reload();
}
async function importPrices() { importing.value = true; try { const response = await dashboard.importBillingPrices(filters.site_id); suggestions.value = response.items; quotaPerUnit.value = response.quota_per_unit; ElMessage.success(`已读取 ${response.items.length} 个模型的价格建议`); } finally { importing.value = false; } }
function editRatio(group_name = "", ratio = "1") { ratioForm.group_name = group_name; ratioForm.ratio = ratio; ratioOpen.value = true; }
async function saveRatio() { if (!ratioForm.group_name.trim()) return; await dashboard.saveBillingGroupRatio({ instance_id: filters.site_id, group_name: ratioForm.group_name.trim(), ratio: ratioForm.ratio }); ratioOpen.value = false; ElMessage.success("分组倍率已保存"); await state.reload(); }
watch(() => filters.site_id, () => { suggestions.value = []; void state.reload(); });
void state.reload();
</script>

<template>
  <AppShell title="账单计价">
    <template #tools><el-button @click="$router.push('/billing')">返回用户账单</el-button><el-button :loading="importing" @click="importPrices">从 new-api 读取价格建议</el-button><el-button type="primary" @click="resetPrice()">新增模型价格</el-button></template>
    <el-alert title="计价规则" type="info" :closable="false" show-icon description="单价均为每 100 万 Token。保存会新增一套生效日期，不会覆盖历史账单；分组倍率未配置时按 1 计算。" />
    <el-tabs v-model="tab" class="pricing-tabs">
      <el-tab-pane label="模型价格" name="prices"><AsyncPanel :loading="state.loading.value" :error="state.error.value" :empty="!state.data.value?.prices.length" @retry="state.reload"><el-table :data="state.data.value?.prices || []"><el-table-column prop="model_name" label="模型" min-width="190" /><el-table-column prop="effective_from" label="生效日期" min-width="150"><template #default="s">{{ String(s.row.effective_from).slice(0,10) }}</template></el-table-column><el-table-column prop="tier_from" label="档位下限 Token" min-width="130" /><el-table-column prop="input_price" label="输入单价" /><el-table-column prop="cache_price" label="缓存单价" /><el-table-column prop="output_price" label="输出单价" /><el-table-column label="操作" width="90"><template #default="s"><el-button link type="primary" @click="resetPrice(s.row)">新价格</el-button></template></el-table-column></el-table></AsyncPanel>
        <section v-if="suggestions.length" class="suggestions"><h3>new-api 当前价格建议 <small>QuotaPerUnit {{ quotaPerUnit }}；仅展示尚未配置 CT 价格的模型</small></h3><el-table :data="unconfiguredSuggestions" max-height="360"><el-table-column prop="model_name" label="模型" min-width="200" /><el-table-column prop="input_price" label="输入单价" /><el-table-column prop="cache_price" label="缓存单价" /><el-table-column prop="output_price" label="输出单价" /><el-table-column label="操作" width="100"><template #default="s"><el-button link type="primary" @click="resetPrice(s.row)">采用并编辑</el-button></template></el-table-column></el-table></section>
      </el-tab-pane>
      <el-tab-pane label="分组倍率" name="ratios"><div class="section-actions"><el-button type="primary" @click="editRatio()">新增分组倍率</el-button></div><el-table :data="state.data.value?.ratios || []"><el-table-column prop="group_name" label="分组" /><el-table-column prop="ratio" label="倍率" /><el-table-column label="操作" width="100"><template #default="s"><el-button link type="primary" @click="editRatio(s.row.group_name, s.row.ratio)">编辑</el-button></template></el-table-column></el-table></el-tab-pane>
    </el-tabs>
    <el-dialog v-model="priceOpen" title="新增生效价格" width="760px"><el-form label-width="110px"><el-form-item label="模型"><el-input v-model="priceForm.model_name" /></el-form-item><el-form-item label="生效日期"><el-date-picker v-model="priceForm.effective_from" type="date" value-format="YYYY-MM-DD" /></el-form-item><el-form-item label="阶梯价格"><div class="tier-list"><div v-for="(tier,index) in priceForm.tiers" :key="index" class="tier-row"><el-input-number v-model="tier.tier_from" :min="0" :controls="false" placeholder="档位下限" /><el-input v-model="tier.input_price" placeholder="输入/1M" /><el-input v-model="tier.cache_price" placeholder="缓存/1M" /><el-input v-model="tier.output_price" placeholder="输出/1M" /><el-button text type="danger" :disabled="priceForm.tiers.length===1" @click="removeTier(index)">删除</el-button></div><el-button @click="addTier">添加阶梯</el-button></div></el-form-item></el-form><template #footer><el-button @click="priceOpen=false">取消</el-button><el-button type="primary" @click="savePrice">保存新价格</el-button></template></el-dialog>
    <el-dialog v-model="ratioOpen" title="分组倍率" width="460px"><el-form label-width="80px"><el-form-item label="分组"><el-input v-model="ratioForm.group_name" /></el-form-item><el-form-item label="倍率"><el-input v-model="ratioForm.ratio" /></el-form-item></el-form><template #footer><el-button @click="ratioOpen=false">取消</el-button><el-button type="primary" @click="saveRatio">保存</el-button></template></el-dialog>
  </AppShell>
</template>

<style scoped>.pricing-tabs{margin-top:14px}.suggestions{margin-top:22px;padding-top:10px;border-top:1px solid var(--el-border-color-lighter)}.suggestions h3{font-size:15px}.suggestions small{font-weight:400;color:var(--el-text-color-secondary);margin-left:8px}.section-actions{display:flex;justify-content:flex-end;margin-bottom:10px}.tier-list{width:100%}.tier-row{display:grid;grid-template-columns:130px 1fr 1fr 1fr 60px;gap:8px;margin-bottom:8px}</style>
