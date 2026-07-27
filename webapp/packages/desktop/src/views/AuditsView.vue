<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { dashboard } from "../api";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import ListPager from "../components/ListPager.vue";
import { useAsyncData } from "../composables/useAsyncData";
import { useAutoRefresh } from "../composables/useAutoRefresh";
import { useFiltersStore } from "../stores/filters";
import { formatTime } from "../utils/format";
const filters = useFiltersStore();
const page = ref(1);
const pageSize = ref(20);
const siteInstanceIDs = computed(
  () =>
    new Set(
      filters.instances
        .filter((item) => item.site_id === filters.site_id)
        .map((item) => item.instance_id),
    ),
);
const state = useAsyncData(
  async () => {
    await filters.loadInstances();
    const items = (await dashboard.operationAudits({ limit: 200 })).items;
    return items.filter((item) => siteInstanceIDs.value.has(item.instance_id));
  },
);
const pageItems = computed(() =>
  (state.data.value || []).slice(
    (page.value - 1) * pageSize.value,
    page.value * pageSize.value,
  ),
);
watch(
  () => filters.site_id,
  () => {
    page.value = 1;
    void state.reload();
  },
);
watch([page, pageSize], () => void state.reload());
useAutoRefresh(state.reload);
</script>
<template>
  <AppShell title="操作审计"
    ><AsyncPanel
      :loading="state.loading.value"
      :error="state.error.value"
      :empty="!state.data.value?.length"
      @retry="state.reload"
      ><el-table :data="pageItems"
        ><el-table-column label="时间"
          ><template #default="s">{{
            formatTime(s.row.created_at)
          }}</template></el-table-column
        ><el-table-column label="实例"
          ><template #default="s"
            ><el-tooltip :content="s.row.instance_id"
              ><span>{{ s.row.instance_name }}</span></el-tooltip
            ></template
          ></el-table-column
        ><el-table-column prop="operation_type" label="类型" /><el-table-column
          label="目标"
          ><template #default="s"
            >{{ s.row.target_type }} / {{ s.row.target_id }}</template
          ></el-table-column
        ><el-table-column prop="actor_id" label="操作人" /><el-table-column
          prop="after_summary"
          label="摘要" /></el-table
      ><ListPager
        v-model:page="page"
        v-model:page-size="pageSize"
        :item-count="state.data.value?.length || 0" /></AsyncPanel
  ></AppShell>
</template>
