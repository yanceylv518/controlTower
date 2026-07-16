<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { ElMessage } from "element-plus";
import { dashboard } from "../api";
import { useAsyncData } from "../composables/useAsyncData";
import { useAutoRefresh } from "../composables/useAutoRefresh";
import { useFiltersStore } from "../stores/filters";
import AsyncPanel from "./AsyncPanel.vue";
import ListPager from "./ListPager.vue";
import StatusTag from "./StatusTag.vue";
const props = defineProps<{ channelId: number }>();
const filters = useFiltersStore();
const open = ref(false);
const confirmed = ref(false);
const resultId = ref("");
const page = ref(1);
const pageSize = ref(20);
const form = reactive<{
  instance_id: string;
  status?: number;
  weight?: number;
  priority?: number;
}>({ instance_id: "" });
watch(open, (v) => {
  if (v) {
    form.instance_id = filters.instance_id;
    confirmed.value = false;
    resultId.value = "";
  }
});
const valid = computed(
  () =>
    Boolean(form.instance_id) &&
    [form.status, form.weight, form.priority].some(
      (v) => v !== undefined && v !== null,
    ) &&
    confirmed.value,
);
const state = useAsyncData(async () =>
  (
    await dashboard.channelCommands({
      instance_id: filters.instance_id || undefined,
      limit: 100,
    })
  ).items.filter((x) => x.channel_id === props.channelId),
);
const commands = computed(() =>
  (state.data.value || []).slice(
    (page.value - 1) * pageSize.value,
    page.value * pageSize.value,
  ),
);
async function submit() {
  if (!valid.value) return;
  try {
    const item = await dashboard.createChannelCommand(props.channelId, {
      ...form,
      confirm: true,
    });
    resultId.value = item.id;
    ElMessage.success("命令已下发");
    await state.reload();
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : "下发失败");
  }
}
watch([() => filters.instance_id, () => props.channelId], () => {
  page.value = 1;
  void state.reload();
});
useAutoRefresh(state.reload);
</script>
<template>
  <section class="channel-ops">
    <div class="panel-title">
      <h3>渠道操作</h3>
      <el-button
        class="channel-command-button"
        type="danger"
        @click="open = true"
        >下发命令</el-button
      >
    </div>
    <AsyncPanel
      :loading="state.loading.value"
      :error="state.error.value"
      :empty="!state.data.value?.length"
      @retry="state.reload"
      ><el-table :data="commands"
        ><el-table-column prop="created_at" label="时间" /><el-table-column
          prop="id"
          label="命令 ID" /><el-table-column label="状态"
          ><template #default="s"
            ><StatusTag :value="s.row.status" /></template></el-table-column
        ><el-table-column prop="error_summary" label="错误" /></el-table
      ><ListPager
        v-model:page="page"
        v-model:page-size="pageSize"
        :item-count="commands.length" /></AsyncPanel
    ><el-dialog v-model="open" title="下发渠道命令" width="560px"
      ><el-alert
        title="该操作将通过 Agent 调用 new-api 管理接口，直接影响线上渠道"
        type="error"
        show-icon
        :closable="false"
      /><el-form :model="form" label-width="90px"
        ><el-form-item label="目标实例"
          ><el-select v-model="form.instance_id"
            ><el-option
              v-for="item in filters.instances"
              :key="item.instance_id"
              :label="item.name || item.instance_id"
              :value="item.instance_id" /></el-select></el-form-item
        ><el-form-item label="状态"
          ><el-select v-model="form.status" clearable
            ><el-option label="启用" :value="1" /><el-option
              label="禁用"
              :value="2" /></el-select></el-form-item
        ><el-form-item label="权重"
          ><el-input-number v-model="form.weight" :min="0" /></el-form-item
        ><el-form-item label="优先级"
          ><el-input-number v-model="form.priority" /></el-form-item></el-form
      ><el-checkbox v-model="confirmed"
        >我确认要对渠道 #{{ channelId }} 执行此变更</el-checkbox
      ><el-alert
        v-if="resultId"
        :title="`命令 ${resultId} 已创建，等待 Agent 心跳认领（通常 ≤30 秒）`"
        type="success"
      /><template #footer
        ><el-button @click="open = false">关闭</el-button
        ><el-button type="danger" :disabled="!valid" @click="submit"
          >确认下发</el-button
        ></template
      ></el-dialog
    >
  </section>
</template>
