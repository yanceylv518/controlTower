<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { dashboard } from "../api";
import { useAsyncData } from "../composables/useAsyncData";
import { useAutoRefresh } from "../composables/useAutoRefresh";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import HoursSelect from "../components/HoursSelect.vue";
import ListPager from "../components/ListPager.vue";
import { formatNumber, formatQuota, formatTokens } from "../utils/format";
import { usePrefsStore } from "../stores/prefs";
import { useFiltersStore } from "../stores/filters";
const hours = ref(24);
const prefs = usePrefsStore();
const filters = useFiltersStore();
void prefs.load();
const quotaFmt = (v: number | null | undefined) =>
  formatQuota(v, prefs.quotaPerUnit, prefs.currencySymbol);
const page = ref(1);
const pageSize = ref(20);
const state = useAsyncData(
  async () =>
    (await dashboard.usage(hours.value, filters.instance_id || undefined)).items,
);
const groups = computed(() =>
  ["instance_user", "instance_channel", "instance_model"].map((type, i) => ({
    title: ["客户排行", "渠道排行", "模型排行"][i],
    items: (state.data.value || [])
      .filter((x) => x.dimension_type === type)
      .slice((page.value - 1) * pageSize.value, page.value * pageSize.value),
  })),
);
watch([hours, () => filters.instance_id], () => {
  page.value = 1;
  void state.reload();
});
useAutoRefresh(state.reload);
</script>
<template>
  <AppShell title="用量统计">
    <template #tools>
      <HoursSelect v-model="hours" :options="[24, 72, 168]" />
    </template>
    <AsyncPanel
      :loading="state.loading.value"
      :error="state.error.value"
      :empty="!state.data.value?.length"
      @retry="state.reload"
      ><div class="usage-grid">
        <section v-for="group in groups" :key="group.title" class="panel">
          <h2>{{ group.title }}</h2>
          <el-table :data="group.items"
            ><el-table-column prop="display_key" label="名称" /><el-table-column
              label="请求"
              ><template #default="s">{{
                formatNumber(s.row.request_count)
              }}</template></el-table-column
            ><el-table-column label="Token In"
              ><template #default="s">{{
                formatTokens(s.row.prompt_tokens)
              }}</template></el-table-column
            ><el-table-column label="Token Out"
              ><template #default="s">{{
                formatTokens(s.row.completion_tokens)
              }}</template></el-table-column
            ><el-table-column label="Quota"
              ><template #default="s">{{
                quotaFmt(s.row.quota)
              }}</template></el-table-column
            ></el-table
          >
        </section>
      </div>
      <ListPager
        v-model:page="page"
        v-model:page-size="pageSize"
        :item-count="
          Math.max(0, ...groups.map((group) => group.items.length))
        " /></AsyncPanel
  ></AppShell>
</template>
