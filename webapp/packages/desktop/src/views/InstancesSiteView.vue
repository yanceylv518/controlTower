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
const readonlyTarget = ref<ReadonlyInstanceItem>();
const readonlyDSN = ref("");
const controlOpen = ref(false);
const controlTarget = ref<ReadonlyInstanceItem>();
const control = reactive({ api_url: "", token: "", admin_user_id: undefined as number | undefined });
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
      logs_readonly_connection_test_failed: "连接测试失败，请检查地址、账号权限、数据库名称以及 users/logs 表是否可读",
      logs_readonly_config_not_persisted: "连接测试已通过，但配置未能写入数据库，请检查 Control Tower 数据库状态",
      invalid_control_api_url: "管理 API 地址必须以 http:// 或 https:// 开头",
      invalid_control_api_token: "请填写管理员 Access Token",
      invalid_control_admin_user_id: "请填写 Token 所属管理员的用户 ID（正整数）",
      control_connection_test_failed: "连接测试失败：无法用该地址与 Token 读取渠道列表，请检查地址、Token 与用户 ID",
      secret_key_not_configured: "服务端未配置 CT_SECRET_KEY，无法加密保存",
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
function configureReadonly(item: ReadonlyInstanceItem) {
  readonlyTarget.value = item;
  readonlyDSN.value = "";
  readonlyOpen.value = true;
}
function configureControl(item: ReadonlyInstanceItem) {
  controlTarget.value = item;
  control.api_url = item.control_api_url || "";
  control.token = "";
  control.admin_user_id = item.control_admin_user_id || undefined;
  controlOpen.value = true;
}
async function saveControl() {
  if (!controlTarget.value) return;
  if (!control.api_url.trim() || !control.token.trim() || !control.admin_user_id) {
    ElMessage.warning("请完整填写管理 API 地址、Access Token 与管理员用户 ID");
    return;
  }
  try {
    const saved = await dashboard.updateInstance(controlTarget.value.instance_id, {
      control_api_url: control.api_url.trim(),
      control_api_token: control.token.trim(),
      control_admin_user_id: control.admin_user_id,
    });
    if (!saved.control_configured) throw new Error("直连配置未生效");
    controlOpen.value = false;
    await state.reload();
    ElMessage.success("连接测试通过，调权直连已加密保存；该站点后续调权/探针/预检将直接调用 new-api");
  } catch (error) {
    ElMessage.error(message(error));
  }
}
async function clearControl() {
  if (!controlTarget.value || !controlTarget.value.control_configured) return;
  try {
    await ElMessageBox.confirm("清除后该站点调权回落为 Agent 命令通道执行。确认清除？", "清除调权直连", { type: "warning" });
    await dashboard.updateInstance(controlTarget.value.instance_id, { control_api_url: "" });
    controlOpen.value = false;
    await state.reload();
    ElMessage.success("调权直连已清除，站点回落为 Agent 命令通道");
  } catch (error) {
    if (error !== "cancel") ElMessage.error(message(error));
  }
}
function siteID(item: InstanceItem) {
  return item.site_id || item.instance_id;
}
function isFirstSiteRow(item: InstanceItem) {
  return activeInstances.value.find(candidate => siteID(candidate) === siteID(item))?.instance_id === item.instance_id;
}
async function saveReadonly() {
  if (!readonlyTarget.value) return;
  if (!readonlyDSN.value.trim()) {
    ElMessage.warning(readonlyTarget.value.logs_readonly_configured ? "请输入新的 DSN 以重新配置；原配置不会被清除" : "请输入 MySQL DSN");
    return;
  }
  try {
    const saved = await client.request<ReadonlyInstanceItem>(`/api/dashboard/instances/${encodeURIComponent(readonlyTarget.value.instance_id)}`, {
      method: "PUT",
      body: JSON.stringify({ logs_readonly_dsn: readonlyDSN.value.trim() }),
    });
    if (!saved.logs_readonly_configured) throw new Error("连接配置未生效");
    readonlyOpen.value = false;
    await state.reload();
    ElMessage.success("连接测试通过，只读连接已加密保存");
  } catch (error) {
    ElMessage.error(message(error));
  }
}
async function clearReadonly() {
  if (!readonlyTarget.value || !readonlyTarget.value.logs_readonly_configured) return;
  try {
    await ElMessageBox.confirm("清除后，使用日志和用户管理将无法查询该站点数据。确认清除？", "清除只读连接", { type: "warning" });
    await client.request(`/api/dashboard/instances/${encodeURIComponent(readonlyTarget.value.instance_id)}`, { method: "PUT", body: JSON.stringify({ logs_readonly_dsn: "" }) });
    readonlyOpen.value = false;
    await state.reload();
    ElMessage.success("只读连接已清除");
  } catch (error) {
    if (error !== "cancel") ElMessage.error(message(error));
  }
}
useAutoRefresh(state.reload);
</script>

<template>
  <AppShell title="实例管理">
    <template #tools><el-button type="primary" @click="createOpen = true">创建实例</el-button></template>
    <AsyncPanel :loading="state.loading.value" :error="state.error.value" :empty="!state.data.value?.length" @retry="state.reload">
      <el-table :data="state.data.value" table-layout="fixed">
        <el-table-column prop="instance_id" label="实例 ID" min-width="160" show-overflow-tooltip />
        <el-table-column prop="site_id" label="站点" min-width="150" show-overflow-tooltip />
        <el-table-column prop="name" label="名称" min-width="150" show-overflow-tooltip />
        <el-table-column label="启用" width="90"><template #default="scope"><el-switch :model-value="scope.row.enabled" @change="toggle(scope.row, Boolean($event))" /></template></el-table-column>
        <el-table-column prop="created_at" label="创建时间" width="180"><template #default="scope">{{ new Date(scope.row.created_at).toLocaleString() }}</template></el-table-column>
        <el-table-column label="Agent" min-width="230"><template #default="scope"><div v-for="agent in scope.row.agents" :key="agent.id" class="agent-line"><StatusTag :value="agent.online ? 'online' : 'offline'" /><span>{{ agent.version }} · 积压 {{ agent.backlog_estimate }}</span></div><span v-if="!scope.row.agents.length">—</span></template></el-table-column>
        <el-table-column label="站点连接" width="190"><template #default="scope"><div v-if="isFirstSiteRow(scope.row)"><div class="status-line"><StatusTag :value="scope.row.logs_readonly_configured ? 'online' : 'offline'" /><span>只读查询{{ scope.row.logs_readonly_configured ? '已配置' : '未配置' }}</span></div><div class="status-line"><StatusTag :value="scope.row.control_configured ? 'online' : 'offline'" /><span>调权直连{{ scope.row.control_configured ? '已配置' : '未配置' }}</span></div></div><span v-else class="shared-hint">随站点共享</span></template></el-table-column>
        <el-table-column label="操作" width="360" align="right" header-align="right"><template #default="scope"><div class="action-line"><el-button size="small" @click="rotate(scope.row)">轮换 Token</el-button><el-button v-if="isFirstSiteRow(scope.row)" size="small" @click="configureReadonly(scope.row)">只读查询</el-button><el-button v-if="isFirstSiteRow(scope.row)" size="small" @click="configureControl(scope.row)">调权直连</el-button></div></template></el-table-column>
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
      <el-alert v-if="readonlyTarget?.logs_readonly_configured" type="success" :closable="false" title="当前站点已配置只读连接。出于安全不会回显原连接；填写新 DSN 可测试并替换。" />
      <el-alert type="warning" :closable="false" title="请使用仅允许 SELECT users、logs 表的 new-api 数据库账号。连接串只会加密保存，不会返回或展示。" />
      <el-form label-position="top" style="margin-top: 16px">
        <el-form-item label="MySQL DSN">
          <el-input v-model="readonlyDSN" type="password" show-password autocomplete="new-password" placeholder="readonly_user:password@tcp(host:3306)/newapi?parseTime=true&loc=UTC" />
          <div class="form-tip">同一站点的所有实例共用此连接；留空不会修改现有配置。</div>
        </el-form-item>
      </el-form>
      <template #footer><el-button v-if="readonlyTarget?.logs_readonly_configured" type="danger" plain @click="clearReadonly">清除配置</el-button><el-button @click="readonlyOpen = false">取消</el-button><el-button type="primary" @click="saveReadonly">测试连接并保存</el-button></template>
    </el-dialog>
    <el-dialog v-model="controlOpen" :title="`配置调权直连：${controlTarget ? siteID(controlTarget) : ''}`" width="620px">
      <el-alert v-if="controlTarget?.control_configured" type="success" :closable="false" title="当前站点已配置调权直连。出于安全不会回显 Token；重新保存需再次填写。" />
      <el-alert type="warning" :closable="false" title="配置后该站点的自动调权、熔断探针与预检将由服务端直接调用 new-api 管理 API，不再经 Agent 命令通道。Token 只会加密保存。" />
      <el-form label-position="top" style="margin-top: 16px">
        <el-form-item label="管理 API 地址">
          <el-input v-model="control.api_url" placeholder="https://your-alb-domain.example.com（与浏览器访问 new-api 控制台相同的地址）" />
          <div class="form-tip">走 ALB 域名即可，与站点业务接口同一入口；同一站点的所有实例共用此配置。</div>
        </el-form-item>
        <el-form-item label="管理员 Access Token">
          <el-input v-model="control.token" type="password" show-password autocomplete="new-password" placeholder="new-api 控制台「个人设置」中生成的系统访问令牌" />
        </el-form-item>
        <el-form-item label="管理员用户 ID">
          <el-input-number v-model="control.admin_user_id" :min="1" controls-position="right" />
          <div class="form-tip">Token 所属管理员账号的用户 ID，new-api 校验 New-Api-User 请求头时使用。</div>
        </el-form-item>
      </el-form>
      <template #footer><el-button v-if="controlTarget?.control_configured" type="danger" plain @click="clearControl">清除配置</el-button><el-button @click="controlOpen = false">取消</el-button><el-button type="primary" @click="saveControl">测试连接并保存</el-button></template>
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
