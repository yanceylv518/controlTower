<script setup lang="ts">
import { computed, ref, watch } from "vue";
import type { BillingUserSummary } from "@ct/shared";
import { dashboard } from "../api";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import ListPager from "../components/ListPager.vue";
import { useAsyncData } from "../composables/useAsyncData";
import { useFiltersStore } from "../stores/filters";
import { usePrefsStore } from "../stores/prefs";
import { useAuthStore } from "../stores/auth";

const filters = useFiltersStore();
const prefs = usePrefsStore();
const auth = useAuthStore();
const month = ref(new Date().toISOString().slice(0, 7));
const search = ref("");
const page = ref(1);
const pageSize = ref(50);
const detailOpen = ref(false);
const selected = ref<BillingUserSummary>();
void prefs.load();
const state = useAsyncData(async () => { await filters.loadInstances(); if (!filters.site_id) return undefined; return dashboard.billingSummary({ instance_id: filters.site_id, month: month.value, page: page.value, page_size: pageSize.value, search: search.value.trim() || undefined }); });
const detail = useAsyncData(async () => selected.value ? dashboard.billingDetail({ instance_id: filters.site_id, user_id: selected.value.user_id, month: month.value }) : undefined);
const currency = computed(() => prefs.currencySymbol || "$");
const money = (value: string | number | undefined) => `${currency.value}${Number(value || 0).toFixed(4)}`;
const exportURL = computed(() => `/api/dashboard/billing/summary?instance_id=${encodeURIComponent(filters.site_id)}&month=${month.value}&search=${encodeURIComponent(search.value)}&format=csv`);
const detailExportURL = computed(() => selected.value ? `/api/dashboard/billing/detail?instance_id=${encodeURIComponent(filters.site_id)}&user_id=${selected.value.user_id}&month=${month.value}&format=csv` : "#");
function openDetail(row: BillingUserSummary) { selected.value = row; detailOpen.value = true; void detail.reload(); }
watch([month, search, () => filters.site_id, pageSize], () => { page.value = 1; void state.reload(); });
watch(page, () => void state.reload());
void state.reload();
</script>
<template>
  <AppShell title="用户账单">
    <template #tools><el-date-picker v-model="month" type="month" value-format="YYYY-MM" format="YYYY-MM" :clearable="false" /><el-input v-model="search" clearable placeholder="搜索用户名或用户 ID" style="width:220px" /><el-button tag="a" :href="exportURL">导出汇总 CSV</el-button><el-button v-if="auth.user?.role === 'admin'" @click="$router.push('/billing/pricing')">计价配置</el-button></template>
    <div class="billing-note">账单每日凌晨更新，数据截至 {{ state.data.value?.data_through?.slice(0, 10) || "昨日" }}。金额按配置价格计算，Quota 为 new-api 实扣对照。</div>
    <AsyncPanel :loading="state.loading.value" :error="state.error.value" :empty="!state.data.value?.items.length" @retry="state.reload">
      <div class="billing-total" v-if="state.data.value?.summary"><span>用户 <b>{{ state.data.value.summary.users }}</b></span><span>请求 <b>{{ state.data.value.summary.request_count }}</b></span><span>合计 <b>{{ money(state.data.value.summary.amount) }}</b></span></div>
      <el-table :data="state.data.value?.items || []" @row-click="openDetail">
        <el-table-column prop="username" label="用户" min-width="150"><template #default="s"><b>{{ s.row.username || `用户 ${s.row.user_id}` }}</b><small class="sub">ID {{ s.row.user_id }}</small></template></el-table-column>
        <el-table-column prop="amount" label="消费金额" min-width="120"><template #default="s">{{ money(s.row.amount) }}</template></el-table-column><el-table-column prop="request_count" label="请求数" min-width="100" /><el-table-column prop="prompt_tokens" label="输入 Token" min-width="120" /><el-table-column prop="completion_tokens" label="输出 Token" min-width="120" /><el-table-column prop="cache_tokens" label="缓存 Token" min-width="120" /><el-table-column prop="quota" label="Quota 对照" min-width="110" /><el-table-column prop="balance" label="当前余额" min-width="110" />
        <el-table-column label="计价状态" min-width="150"><template #default="s"><el-tag v-if="s.row.unpriced_models?.length" type="warning">{{ s.row.unpriced_models.length }} 个模型无法取价</el-tag><el-tag v-else type="success">已计价</el-tag></template></el-table-column><el-table-column label="操作" width="90"><template #default="s"><el-button link type="primary" @click.stop="openDetail(s.row)">查看</el-button></template></el-table-column>
      </el-table><ListPager v-model:page="page" v-model:page-size="pageSize" :item-count="state.data.value?.items.length || 0" />
    </AsyncPanel>
    <el-drawer v-model="detailOpen" :title="`${selected?.username || '用户'} · ${month}`" size="72%"><div class="drawer-actions"><el-button tag="a" :href="detailExportURL">导出明细 CSV</el-button><router-link :to="`/readonly-logs?user_id=${selected?.user_id || ''}&month=${month}`"><el-button>查看使用日志</el-button></router-link></div><AsyncPanel :loading="detail.loading.value" :error="detail.error.value" :empty="!detail.data.value?.items.length" @retry="detail.reload"><el-table :data="detail.data.value?.items || []"><el-table-column prop="day" label="日期" width="110" /><el-table-column prop="model_name" label="模型" min-width="180" /><el-table-column prop="group_name" label="分组" min-width="100" /><el-table-column prop="request_count" label="请求" width="80" /><el-table-column prop="prompt_tokens" label="输入" min-width="100" /><el-table-column prop="completion_tokens" label="输出" min-width="100" /><el-table-column prop="amount" label="金额" width="110"><template #default="s">{{ s.row.unpriced ? "无法取价" : money(s.row.amount) }}</template></el-table-column><el-table-column prop="price_source" label="价格来源" width="100" /></el-table></AsyncPanel></el-drawer>
  </AppShell>
</template>
<style scoped>.billing-note{padding:10px 14px;margin-bottom:10px;border:1px solid var(--el-border-color-lighter);border-radius:8px;color:var(--el-text-color-secondary);background:var(--el-fill-color-lighter)}.billing-total{display:flex;gap:28px;padding:12px 4px}.billing-total b{font-size:18px;color:var(--el-color-primary)}.sub{display:block;color:var(--el-text-color-secondary);margin-top:3px}.drawer-actions{display:flex;gap:8px;justify-content:flex-end;margin-bottom:12px}</style>
