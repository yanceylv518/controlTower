<script setup lang="ts">
import { computed, reactive, ref } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { ApiError, type InstanceItem } from "@ct/shared";
import { client, dashboard } from "../api";
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
const readonlyOpen = ref(false);
const readonlyTarget = ref<InstanceItem>();
const readonlyDSN = ref("");
type ReadonlyInstanceItem = InstanceItem & { logs_readonly_configured: boolean };
const state = useAsyncData(async () => (await dashboard.instances()).items as ReadonlyInstanceItem[]);
const activeInstances = computed(() => state.data.value?.filter(item => item.enabled) || []);

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
function configureReadonly(item: InstanceItem) {
  readonlyTarget.value = item;
  readonlyDSN.value = "";
  readonlyOpen.value = true;
}
function siteID(item: InstanceItem) {
  return item.site_id || item.instance_id;
}
function isFirstSiteRow(item: InstanceItem) {
  return activeInstances.value.find(candidate => siteID(candidate) === siteID(item))?.instance_id === item.instance_id;
}
async function saveReadonly() {
  if (!readonlyTarget.value) return;
  try {
    await client.request(`/api/dashboard/instances/${encodeURIComponent(readonlyTarget.value.instance_id)}`, {
      method: "PUT",
      body: JSON.stringify({ logs_readonly_dsn: readonlyDSN.value.trim() }),
    });
    readonlyOpen.value = false;
    await state.reload();
    ElMessage.success(readonlyDSN.value.trim() ? "只读连接已加密保存" : "只读连接已清除");
  } catch (error) {
    ElMessage.error(message(error));
  }
}
useAutoRefresh(state.reload);
</script>

<template>
  <AppShell title="实例管理">
    <template #tools><el-button type="primary" @click="createOpen = true">创建实例</el-button></template>
    <AsyncPanel :loading="state.loading.value" :error="state.error.value" :empty="!activeInstances.length" @retry="state.reload">
      <el-table :data="activeInstances" table-layout="fixed">
        <el-table-column prop="instance_id" label="实例 ID" min-width="160" show-overflow-tooltip />
        <el-table-column prop="site_id" label="站点" min-width="150" show-overflow-tooltip />
        <el-table-column prop="name" label="名称" min-width="150" show-overflow-tooltip />
        <el-table-column label="启用" width="90"><template #default="scope"><el-switch :model-value="scope.row.enabled" @change="toggle(scope.row, Boolean($event))" /></template></el-table-column>
        <el-table-column prop="created_at" label="创建时间" width="180"><template #default="scope">{{ new Date(scope.row.created_at).toLocaleString() }}</template></el-table-column>
        <el-table-column label="Agent" min-width="230"><template #default="scope"><div v-for="agent in scope.row.agents" :key="agent.id" class="agent-line"><StatusTag :value="agent.online ? 'online' : 'offline'" /><span>{{ agent.version }} · 积压 {{ agent.backlog_estimate }}</span></div><span v-if="!scope.row.agents.length">—</span></template></el-table-column>
        <el-table-column label="只读查询" width="170"><template #default="scope"><div v-if="isFirstSiteRow(scope.row)" class="status-line"><StatusTag :value="scope.row.logs_readonly_configured ? 'online' : 'offline'" /><span>{{ scope.row.logs_readonly_configured ? '已配置' : '未配置' }}</span></div><span v-else class="shared-hint">随站点共享</span></template></el-table-column>
        <el-table-column label="操作" width="290" align="right" header-align="right"><template #default="scope"><div class="action-line"><el-button size="small" @click="rotate(scope.row)">轮换 Token</el-button><el-button v-if="isFirstSiteRow(scope.row)" size="small" @click="configureReadonly(scope.row)">配置站点连接</el-button></div></template></el-table-column>
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
    <el-dialog v-model="readonlyOpen" :title="`配置站点只读查询：${readonlyTarget ? siteID(readonlyTarget) : ''}`" width="620px">
      <el-alert type="warning" :closable="false" title="请使用仅允许 SELECT users、logs 表的 new-api 数据库账号。连接串只会加密保存，不会返回或展示。" />
      <el-form label-position="top" style="margin-top: 16px">
        <el-form-item label="MySQL DSN">
          <el-input v-model="readonlyDSN" type="password" show-password autocomplete="new-password" placeholder="readonly_user:password@tcp(host:3306)/newapi?parseTime=true&loc=UTC" />
          <div class="form-tip">同一站点的所有实例共用此连接；留空保存将清除配置。</div>
        </el-form-item>
      </el-form>
      <template #footer><el-button @click="readonlyOpen = false">取消</el-button><el-button type="primary" @click="saveReadonly">加密保存</el-button></template>
    </el-dialog>
    <TokenDialog v-model="tokenOpen" :token="token" :grace-until="grace" />
  </AppShell>
</template>

<style scoped>
.agent-line,
.status-line,
.action-line {
  display: flex;
  align-items: center;
  gap: 6px;
  white-space: nowrap;
}
.action-line { justify-content: flex-end; gap: 8px; }
.shared-hint { color: var(--ct-ink-3); font-size: 12px; }
</style>
