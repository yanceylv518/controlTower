<script setup lang="ts">
import { ref, watch } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { ApiError, type AlertEvent, type AlertItem } from "@ct/shared";
import { dashboard } from "../api";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import ListPager from "../components/ListPager.vue";
import StatusTag from "../components/StatusTag.vue";
import { useAsyncData } from "../composables/useAsyncData";
import { useAutoRefresh } from "../composables/useAutoRefresh";
import { useFiltersStore } from "../stores/filters";
import { formatTime } from "../utils/format";
const filters = useFiltersStore();
const status = ref("");
const severity = ref("");
const activeOnly = ref(false);
const page = ref(1);
const pageSize = ref(20);
const drawer = ref(false);
const selected = ref<AlertItem>();
const displayName = (value: string) => value.replace(/ \(ID \d+\)$/, "");
const events = ref<AlertEvent[]>([]);
const state = useAsyncData(
  async () =>
    (
      await dashboard.alerts({
        instance_id: filters.instance_id || undefined,
        status: status.value || undefined,
        severity: severity.value || undefined,
        active_only: activeOnly.value || undefined,
        limit: pageSize.value,
        offset: (page.value - 1) * pageSize.value,
      })
    ).items,
);
const dimensionRoute = (item: AlertItem) => ({
  path:
    item.dimension_type === "instance_user"
      ? "/customers"
      : item.dimension_type === "instance_channel"
        ? "/channels"
        : item.dimension_type === "instance_model"
          ? "/models"
          : "/",
  query: item.dimension_key ? { key: item.dimension_key } : {},
});
function code(e: unknown) {
  return e instanceof ApiError ? e.code : "操作失败";
}
async function timeline(item: AlertItem) {
  selected.value = item;
  drawer.value = true;
  events.value = (await dashboard.alertEvents(item.id)).items.sort((a, b) =>
    a.created_at.localeCompare(b.created_at),
  );
}
async function action(item: AlertItem, kind: "acknowledge" | "silence") {
  try {
    let note = "";
    let silence_minutes: number | undefined;
    if (kind === "acknowledge") {
      note = await ElMessageBox.prompt("可填写确认备注", "确认告警", {
        inputType: "textarea",
      }).then((x) => x.value);
    } else {
      const result = await ElMessageBox.prompt(
        "输入静默分钟数：30、60 或 240；可在数字后填写备注",
        "静默告警",
        { inputValue: "30" },
      );
      const parts = result.value.trim().split(/\s+/, 2);
      silence_minutes = Number(parts[0]);
      if (![30, 60, 240].includes(silence_minutes))
        throw new Error("静默时长仅支持 30、60、240 分钟");
      note = parts[1] || "";
    }
    await dashboard.alertAction({
      id: item.id,
      action: kind,
      note,
      silence_minutes,
    });
    ElMessage.success("操作成功");
    await state.reload();
    if (selected.value?.id === item.id) await timeline(item);
  } catch (e) {
    if (e === "cancel" || (e as { action?: string })?.action === "cancel")
      return;
    ElMessage.error(
      e instanceof Error && !(e instanceof ApiError) ? e.message : code(e),
    );
  }
}
watch([() => filters.instance_id, status, severity, activeOnly], () => {
  page.value = 1;
  void state.reload();
});
watch([page, pageSize], () => void state.reload());
useAutoRefresh(state.reload);
</script>
<template>
  <AppShell title="告警中心">
    <template #tools>
      <el-select v-model="status" placeholder="全部状态" clearable style="width: 130px"
        ><el-option
          v-for="v in ['firing', 'acknowledged', 'silenced', 'resolved']"
          :key="v"
          :label="v"
          :value="v" /></el-select
      ><el-select v-model="severity" placeholder="全部级别" clearable style="width: 130px"
        ><el-option
          v-for="v in ['critical', 'warning', 'info']"
          :key="v"
          :label="v"
          :value="v" /></el-select
      ><el-checkbox v-model="activeOnly">仅活动告警</el-checkbox>
    </template>
    <AsyncPanel
      :loading="state.loading.value"
      :error="state.error.value"
      :empty="!state.data.value?.length"
      @retry="state.reload"
      ><div class="action-list">
        <article
          v-for="item in state.data.value"
          :key="item.id"
          class="alert-card"
          :data-severity="item.severity"
          @click="timeline(item)"
        >
          <StatusTag :value="item.severity" />
          <div>
            <strong>{{ item.title }}</strong>
            <p>{{ item.summary }}</p>
            <small
              ><el-tooltip :content="item.instance_id"
                ><span>{{ item.instance_name }}</span></el-tooltip
              >
              ·
              <span :title="item.display_key">{{
                displayName(item.display_key)
              }}</span>
              · {{ formatTime(item.last_seen_at) }}</small
            >
          </div>
          <div class="row-actions" @click.stop>
            <router-link v-if="item.dimension_key" :to="dimensionRoute(item)"
              ><el-button size="small" type="primary" plain
                >查看维度</el-button
              ></router-link
            ><template v-if="item.status !== 'resolved'"
              ><el-button size="small" @click="action(item, 'acknowledge')"
                >确认</el-button
              ><el-button
                size="small"
                type="warning"
                @click="action(item, 'silence')"
                >静默</el-button
              ></template
            >
          </div>
        </article>
      </div>
      <ListPager
        v-model:page="page"
        v-model:page-size="pageSize"
        :item-count="state.data.value?.length || 0" /></AsyncPanel
    ><el-drawer
      v-model="drawer"
      :title="
        selected
          ? `${displayName(selected.display_key)} · 告警时间线`
          : '告警时间线'
      "
      size="480px"
      ><p v-if="selected">
        <el-tooltip :content="selected.instance_id"
          ><span>{{ selected.instance_name }}</span></el-tooltip
        >
        · {{ selected.summary }}
      </p>
      <el-timeline
        ><el-timeline-item
          v-for="event in events"
          :key="event.id"
          :timestamp="formatTime(event.created_at)"
          :type="
            event.event_type === 'resolved'
              ? 'success'
              : event.event_type === 'firing' || event.event_type === 'refired'
                ? 'danger'
                : 'primary'
          "
          ><strong>{{ event.event_type }}</strong>
          <p>
            {{ event.actor }}<span v-if="event.note">：{{ event.note }}</span>
          </p></el-timeline-item
        ></el-timeline
      ></el-drawer
    ></AppShell
  >
</template>
