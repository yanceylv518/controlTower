<script setup lang="ts">
import { reactive, ref, watch } from "vue";
import { ElMessage } from "element-plus";
import { Plus } from "@element-plus/icons-vue";
import type { NotificationChannelInput } from "@ct/shared";
import { dashboard } from "../api";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import ListPager from "../components/ListPager.vue";
import StatusTag from "../components/StatusTag.vue";
import { useAsyncData } from "../composables/useAsyncData";
import { useAutoRefresh } from "../composables/useAutoRefresh";
import { formatTime } from "../utils/format";

const typeLabels: Record<string, string> = {
  webhook: "通用 Webhook",
  dingtalk: "钉钉机器人",
  wecom: "企业微信机器人",
};
const dialogOpen = ref(false);
const form = reactive<NotificationChannelInput>({
  id: "",
  name: "",
  channel_type: "webhook",
  webhook_url: "",
  enabled: true,
  secret: "",
});
const saving = ref(false);
const deliveryPage = ref(1);
const deliveryPageSize = ref(20);
const channels = useAsyncData(
  async () => (await dashboard.notificationChannels()).items,
);
const deliveries = useAsyncData(
  async () =>
    (
      await dashboard.notificationDeliveries({
        limit: deliveryPageSize.value,
        offset: (deliveryPage.value - 1) * deliveryPageSize.value,
      })
    ).items,
);
watch([deliveryPage, deliveryPageSize], () => void deliveries.reload());
async function save() {
  saving.value = true;
  try {
    await dashboard.saveNotificationChannel({
      ...form,
      secret: form.channel_type === "dingtalk" ? form.secret : undefined,
    });
    form.secret = "";
    dialogOpen.value = false;
    ElMessage.success("通知渠道已保存");
    await channels.reload();
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : "保存失败");
  } finally {
    saving.value = false;
  }
}
async function resend(id: string) {
  try {
    await dashboard.resendDelivery(id);
    ElMessage.success("已安排重发");
    await deliveries.reload();
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : "重发失败");
  }
}
useAutoRefresh(deliveries.reload);
</script>
<template>
  <AppShell title="通知设置">
    <template #tools>
      <el-button type="primary" :icon="Plus" @click="dialogOpen = true"
        >添加渠道</el-button
      >
    </template>
    <section class="panel sub-panel">
      <h2>通知渠道</h2>
      <AsyncPanel
        :loading="channels.loading.value"
        :error="channels.error.value"
        :empty="!channels.data.value?.length"
        @retry="channels.reload"
      >
        <template #empty>
          <el-empty
            description="尚无通知渠道——添加企业微信/钉钉机器人后，Server 侧告警（离线/资源/错误率/P95）将推送到群"
          />
        </template>
        <el-table :data="channels.data.value">
          <el-table-column prop="name" label="名称" min-width="140" />
          <el-table-column label="类型" width="150">
            <template #default="s">
              <el-tag size="small">{{
                typeLabels[s.row.channel_type] || s.row.channel_type
              }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column
            prop="webhook_url_masked"
            label="Webhook 地址"
            min-width="220"
            show-overflow-tooltip
          />
          <el-table-column label="加签" width="90">
            <template #default="s">{{
              s.row.has_secret ? "已加签" : "—"
            }}</template>
          </el-table-column>
          <el-table-column label="状态" width="90">
            <template #default="s">
              <StatusTag :value="s.row.enabled ? 'enabled' : 'disabled'" />
            </template>
          </el-table-column>
        </el-table>
      </AsyncPanel>
    </section>
    <section class="panel sub-panel">
      <h2>投递记录</h2>
      <AsyncPanel
        :loading="deliveries.loading.value"
        :error="deliveries.error.value"
        :empty="!deliveries.data.value?.length"
        @retry="deliveries.reload"
      >
        <el-table :data="deliveries.data.value">
          <el-table-column label="时间" width="160">
            <template #default="s">{{ formatTime(s.row.attempted_at) }}</template>
          </el-table-column>
          <el-table-column label="状态" width="100">
            <template #default="s"><StatusTag :value="s.row.status" /></template>
          </el-table-column>
          <el-table-column prop="status_code" label="HTTP" width="70" align="right" />
          <el-table-column prop="attempts" label="次数" width="60" align="right" />
          <el-table-column label="下次重试" width="160">
            <template #default="s">{{
              formatTime(s.row.next_attempt_at)
            }}</template>
          </el-table-column>
          <el-table-column
            prop="alert_id"
            label="告警"
            min-width="160"
            show-overflow-tooltip
          />
          <el-table-column
            prop="error_summary"
            label="错误摘要"
            min-width="180"
            show-overflow-tooltip
          />
          <el-table-column label="操作" width="80">
            <template #default="s">
              <el-button
                v-if="['failed', 'exhausted'].includes(s.row.status)"
                size="small"
                @click="resend(s.row.id)"
                >重发</el-button
              >
            </template>
          </el-table-column>
        </el-table>
        <ListPager
          v-model:page="deliveryPage"
          v-model:page-size="deliveryPageSize"
          :item-count="deliveries.data.value?.length || 0"
        />
      </AsyncPanel>
    </section>
    <el-dialog v-model="dialogOpen" title="添加 / 更新通知渠道" width="520px">
      <el-form :model="form" label-width="90px">
        <el-form-item label="ID">
          <el-input v-model="form.id" placeholder="唯一标识，如 wecom-ops" />
        </el-form-item>
        <el-form-item label="名称">
          <el-input v-model="form.name" placeholder="如 运维企微群" />
        </el-form-item>
        <el-form-item label="类型">
          <el-select v-model="form.channel_type">
            <el-option
              v-for="(label, value) in typeLabels"
              :key="value"
              :label="label"
              :value="value"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="URL">
          <el-input
            v-model="form.webhook_url"
            placeholder="机器人 Webhook 地址"
          />
        </el-form-item>
        <el-form-item v-if="form.channel_type === 'dingtalk'" label="Secret">
          <el-input
            v-model="form.secret"
            type="password"
            show-password
            placeholder="加签密钥，留空为关键词模式"
            autocomplete="new-password"
          />
        </el-form-item>
        <el-form-item label="启用">
          <el-switch v-model="form.enabled" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogOpen = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save"
          >保存</el-button
        >
      </template>
    </el-dialog>
  </AppShell>
</template>
