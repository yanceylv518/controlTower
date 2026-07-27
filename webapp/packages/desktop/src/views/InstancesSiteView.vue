<script setup lang="ts">
import { reactive, ref } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { ApiError, type InstanceItem } from "@ct/shared";
import { dashboard } from "../api";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import StatusTag from "../components/StatusTag.vue";
import TokenDialog from "../components/TokenDialog.vue";
import { useAsyncData } from "../composables/useAsyncData";
import { useAutoRefresh } from "../composables/useAutoRefresh";
import { useFiltersStore } from "../stores/filters";

const filters = useFiltersStore();
const createOpen = ref(false);
const form = reactive({ instance_id: "", site_id: "", name: "" });
const tokenOpen = ref(false);
const token = ref("");
const grace = ref<string>();
const state = useAsyncData(async () => (await dashboard.instances()).items);

function message(error: unknown) {
  if (error instanceof ApiError) {
    return {
      instance_exists: "实例 ID 已存在",
      instance_not_found: "实例不存在",
      instance_disabled: "实例已停用",
      invalid_site_id: "站点 ID 格式不正确",
    }[error.code] || error.code;
  }
  return error instanceof Error ? error.message : "操作失败";
}
function showToken(value: string, graceUntil?: string) {
  token.value = value;
  grace.value = graceUntil;
  tokenOpen.value = true;
}
async function create() {
  if (!/^[a-z0-9_-]{1,64}$/.test(form.instance_id)) {
    ElMessage.error("实例 ID 格式不正确");
    return;
  }
  if (form.site_id && !/^[a-z0-9_-]{1,64}$/.test(form.site_id)) {
    ElMessage.error("站点 ID 格式不正确");
    return;
  }
  try {
    const result = await dashboard.createInstance({ ...form });
    createOpen.value = false;
    showToken(result.token);
    Object.assign(form, { instance_id: "", site_id: "", name: "" });
    filters.loaded = false;
    await Promise.all([state.reload(), filters.loadInstances()]);
  } catch (error) {
    ElMessage.error(message(error));
  }
}
async function rotate(item: InstanceItem) {
  try {
    await ElMessageBox.confirm("轮换后旧 Token 仍有 24 小时宽限，是否继续？", "轮换 Token", { type: "warning" });
    const result = await dashboard.rotateInstanceToken(item.instance_id);
    showToken(result.token, result.grace_until);
  } catch (error) {
    if ((error as string) !== "cancel") ElMessage.error(message(error));
  }
}
async function toggle(item: InstanceItem, value: boolean) {
  try {
    await ElMessageBox.confirm(value ? "确认启用该实例？" : "停用后该实例全部 Agent token 立即失效", value ? "启用实例" : "停用实例", { type: "warning" });
    await dashboard.updateInstance(item.instance_id, { enabled: value });
    filters.loaded = false;
    await Promise.all([state.reload(), filters.loadInstances()]);
  } catch (error) {
    if ((error as string) !== "cancel") ElMessage.error(message(error));
    await state.reload();
  }
}
useAutoRefresh(state.reload);
</script>

<template>
  <AppShell title="实例管理">
    <template #tools><el-button type="primary" @click="createOpen = true">创建实例</el-button></template>
    <AsyncPanel :loading="state.loading.value" :error="state.error.value" :empty="!state.data.value?.length" @retry="state.reload">
      <el-table :data="state.data.value">
        <el-table-column prop="instance_id" label="实例 ID" />
        <el-table-column prop="site_id" label="站点" />
        <el-table-column prop="name" label="名称" />
        <el-table-column label="启用"><template #default="scope"><el-switch :model-value="scope.row.enabled" @change="toggle(scope.row, Boolean($event))" /></template></el-table-column>
        <el-table-column prop="created_at" label="创建时间"><template #default="scope">{{ new Date(scope.row.created_at).toLocaleString() }}</template></el-table-column>
        <el-table-column label="Agent"><template #default="scope"><div v-for="agent in scope.row.agents" :key="agent.id"><StatusTag :value="agent.online ? 'online' : 'offline'" /> {{ agent.version }} · 积压 {{ agent.backlog_estimate }}</div><span v-if="!scope.row.agents.length">—</span></template></el-table-column>
        <el-table-column label="操作"><template #default="scope"><el-button size="small" @click="rotate(scope.row)">轮换 Token</el-button></template></el-table-column>
      </el-table>
    </AsyncPanel>
    <el-dialog v-model="createOpen" title="创建实例" width="480px">
      <el-form :model="form" label-width="90px">
        <el-form-item label="实例 ID"><el-input v-model="form.instance_id" placeholder="如 pinducloud_cn_1" /></el-form-item>
        <el-form-item label="站点"><el-input v-model="form.site_id" placeholder="如 pinducloud_cn；留空则等于实例 ID" /><div class="form-tip">同一套共享 new-api 数据库的实例填写相同站点。</div></el-form-item>
        <el-form-item label="名称"><el-input v-model="form.name" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="createOpen = false">取消</el-button><el-button type="primary" @click="create">创建</el-button></template>
    </el-dialog>
    <TokenDialog v-model="tokenOpen" :token="token" :grace-until="grace" />
  </AppShell>
</template>
