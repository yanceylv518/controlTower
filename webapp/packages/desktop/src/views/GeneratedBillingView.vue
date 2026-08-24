<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useRouter } from "vue-router";
import { siteOf, type BillingDailyOverview } from "@ct/shared";
import { dashboard } from "../api";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import { useAsyncData } from "../composables/useAsyncData";
import { useFiltersStore } from "../stores/filters";
import { formatDateTime } from "../utils/billingRange";

const filters = useFiltersStore();
const router = useRouter();
const now = new Date();
const month = ref(`${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}`);
const selectedSite = ref("");
const state = useAsyncData(async () => {
  await filters.loadInstances();
  return dashboard.billingOverview({ instance_id: selectedSite.value || undefined, month: month.value });
});
const items = computed(() => state.data.value?.items || []);
const availableDays = computed(() => new Set(items.value.map((item) => dayText(item.day))).size);
const siteOptions = computed(() => filters.instances.filter((item) => item.enabled).map((item) => ({ value: siteOf(item), label: item.name || siteOf(item) })));
const totals = computed(() => items.value.reduce((sum, item) => ({
  users: sum.users + item.user_count,
  requests: sum.requests + item.request_count,
  amount: sum.amount + Number(item.amount || 0),
}), { users: 0, requests: 0, amount: 0 }));

function dayText(value: string) { return value.slice(0, 10); }
function nextDay(value: string) {
  const [year, monthValue, day] = dayText(value).split("-").map(Number);
  const date = new Date(year, monthValue - 1, day + 1);
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`;
}
function local(value?: string) { return value ? formatDateTime(new Date(value)) : "—"; }
function amount(value: string | number) { return `$${Number(value || 0).toFixed(6)}`; }
function rowKey(item: BillingDailyOverview) { return `${item.instance_id}:${dayText(item.day)}`; }
async function open(item: BillingDailyOverview) {
  filters.selectSite(item.instance_id);
  const day = dayText(item.day);
  await router.push({ path: "/billing", query: { site: item.instance_id, from: `${day} 00:00:00`, to: `${nextDay(day)} 00:00:00` } });
}

watch([month, selectedSite], () => void state.reload());
void state.reload();
</script>

<template>
  <AppShell title="账单总览">
    <template #tools>
      <el-select v-model="selectedSite" clearable placeholder="全部站点" style="width: 180px">
        <el-option v-for="site in siteOptions" :key="site.value" :label="site.label" :value="site.value" />
      </el-select>
      <el-date-picker v-model="month" type="month" value-format="YYYY-MM" placeholder="选择月份" style="width: 150px" />
      <el-button @click="state.reload">刷新</el-button>
    </template>
    <div class="summary">
      <div><span>可查看日期</span><strong>{{ availableDays }}</strong></div>
      <div><span>用户账单</span><strong>{{ totals.users }}</strong></div>
      <div><span>请求数量</span><strong>{{ totals.requests.toLocaleString() }}</strong></div>
      <div><span>原始计费金额</span><strong>{{ amount(totals.amount) }}</strong></div>
    </div>
    <AsyncPanel :loading="state.loading.value" :error="state.error.value" :empty="!items.length" empty-text="该月份暂无可查看账单" @retry="state.reload">
      <el-table :data="items" :row-key="rowKey" @row-click="open">
        <el-table-column label="账单日期" min-width="130"><template #default="s"><strong>{{ dayText(s.row.day) }}</strong></template></el-table-column>
        <el-table-column prop="instance_id" label="站点" min-width="150" />
        <el-table-column label="状态" width="100"><template #default><el-tag type="success">可查看</el-tag></template></el-table-column>
        <el-table-column prop="user_count" label="用户数" width="100" />
        <el-table-column prop="request_count" label="请求数" min-width="120"><template #default="s">{{ s.row.request_count.toLocaleString() }}</template></el-table-column>
        <el-table-column label="原始金额" min-width="140"><template #default="s">{{ amount(s.row.amount) }}</template></el-table-column>
        <el-table-column label="异常" width="90"><template #default="s"><el-tag v-if="s.row.anomaly_rows" type="warning">{{ s.row.anomaly_rows }}</el-tag><span v-else>0</span></template></el-table-column>
        <el-table-column prop="file_count" label="明细文件" width="100" />
        <el-table-column label="最近生成时间" min-width="180"><template #default="s">{{ local(s.row.activated_at) }}</template></el-table-column>
        <el-table-column label="操作" width="100"><template #default="s"><el-button link type="primary" @click.stop="open(s.row)">查看账单</el-button></template></el-table-column>
      </el-table>
    </AsyncPanel>
  </AppShell>
</template>

<style scoped>
.summary{display:grid;grid-template-columns:repeat(4,minmax(150px,1fr));gap:12px;margin-bottom:16px}.summary>div{display:flex;flex-direction:column;gap:8px;padding:16px;border:1px solid var(--el-border-color-light);border-radius:10px;background:var(--el-bg-color)}.summary span{color:var(--el-text-color-secondary);font-size:13px}.summary strong{font-size:22px}:deep(.el-table__row){cursor:pointer}@media(max-width:900px){.summary{grid-template-columns:repeat(2,1fr)}}
</style>
