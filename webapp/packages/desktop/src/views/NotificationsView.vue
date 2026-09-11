<script setup lang="ts">
import { reactive, ref, watch } from "vue";
import { ElMessage } from "element-plus";
import { Plus } from "@element-plus/icons-vue";
import type { NotificationChannelInput, NotificationChannelItem } from "@ct/shared";
import { dashboard } from "../api";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import ListPager from "../components/ListPager.vue";
import StatusTag from "../components/StatusTag.vue";
import { useAsyncData } from "../composables/useAsyncData";
import { useAutoRefresh } from "../composables/useAutoRefresh";
import { formatTime } from "../utils/format";
import { useFiltersStore } from "../stores/filters";
import { alertCategories, categoriesForRules, rulesForCategories, categorySummary } from "../utils/alertCategories";

const filters = useFiltersStore();
const typeLabels: Record<string, string> = {
  webhook: "通用 Webhook",
  dingtalk: "钉钉机器人",
  wecom: "企业微信机器人",
};
const dialogOpen = ref(false);
const editing = ref(false);
const allTypes = ref(false);
const selectedCategories = ref<string[]>([]);
const form = reactive<NotificationChannelInput>({
  site_id: "",
  rule_keys: [],
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
async function loadChannels(): Promise<{ items: NotificationChannelItem[]; unassigned: NotificationChannelItem[] }> {
  await filters.loadInstances();
  const site = filters.site_id;
  if (!site) return { items: [], unassigned: [] };
  const [assigned, legacy] = await Promise.all([
    dashboard.notificationChannels({ site_id: site }),
    dashboard.notificationChannels({ unassigned: true }),
  ]);
  if (site !== filters.site_id) return loadChannels();
  return { items: assigned.items, unassigned: legacy.items };
}
const channels = useAsyncData(loadChannels);
async function loadDeliveries(): Promise<Awaited<ReturnType<typeof dashboard.notificationDeliveries>>["items"]> {
  await filters.loadInstances();
  const site = filters.site_id;
  const page = deliveryPage.value;
  const size = deliveryPageSize.value;
  if (!site) return [];
  const result = await dashboard.notificationDeliveries({
    site_id: site, limit: size, offset: (page - 1) * size,
  });
  if (site !== filters.site_id || page !== deliveryPage.value || size !== deliveryPageSize.value) return loadDeliveries();
  return result.items;
}
const deliveries = useAsyncData(loadDeliveries);
watch([deliveryPage, deliveryPageSize], () => void deliveries.reload());
watch(() => filters.site_id, () => {
  dialogOpen.value = false;
  channels.data.value = undefined;
  deliveries.data.value = undefined;
  deliveryPage.value = 1;
  void channels.reload();
  void deliveries.reload();
});
function openChannel(channel?: NotificationChannelItem) {
  editing.value = !!channel;
  allTypes.value = !!channel && !channel.rule_keys?.length;
  selectedCategories.value = categoriesForRules(channel?.rule_keys || []);
  Object.assign(form, {
    id: channel?.id || "", site_id: filters.site_id,
    name: channel?.name || "", channel_type: channel?.channel_type || "webhook",
    webhook_url: "", secret: "", enabled: channel?.enabled ?? true,
    rule_keys: [...(channel?.rule_keys || [])],
  });
  dialogOpen.value = true;
}
async function save() {
  if (!form.site_id || !form.name.trim() || (!editing.value && !form.webhook_url.trim())) {
    ElMessage.error("请选择站点并填写名称和 Webhook 地址");
    return;
  }
  if (!allTypes.value && !selectedCategories.value.length) {
    ElMessage.error("请选择至少一种告警类型，或勾选全部类型");
    return;
  }
  saving.value = true;
  try {
    await dashboard.saveNotificationChannel({
      ...form,
      rule_keys: allTypes.value ? [] : rulesForCategories(selectedCategories.value),
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
    await dashboard.resendDelivery(id, filters.site_id);
    ElMessage.success("已安排重发");
    await deliveries.reload();
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : "重发失败");
  }
}
useAutoRefresh(channels.reload);
useAutoRefresh(deliveries.reload);
</script>
<template>
  <AppShell title="通知设置">
    <template #tools>
      <el-button type="primary" :icon="Plus" :disabled="!filters.site_id" @click="openChannel()"
        >添加渠道</el-button
      >
    </template>
    <el-alert
      title="通知渠道按站点独立配置，只有站点和告警类型都匹配才会投递。多个渠道匹配时，各发送一份。"
      type="info" :closable="false" show-icon
    />
    <section v-if="channels.data.value?.unassigned.length" class="panel sub-panel">
      <h2>旧渠道待分配</h2>
      <el-alert title="这些旧渠道尚未绑定站点，已暂停投递。确认归属后，分配到对应站点并选择告警类型。" type="warning" :closable="false" />
      <el-table :data="channels.data.value.unassigned">
        <el-table-column prop="name" label="名称" />
        <el-table-column prop="webhook_url_masked" label="Webhook 地址" />
        <el-table-column label="操作" width="240">
          <template #default="s">
            <el-button :disabled="!filters.site_id" @click="openChannel(s.row)">分配到 {{ filters.site_id }}</el-button>
          </template>
        </el-table-column>
      </el-table>
    </section>
    <section class="panel sub-panel">
      <h2>{{ filters.site_id }} · 通知渠道</h2>
      <AsyncPanel
        :loading="channels.loading.value"
        :error="channels.error.value"
        :empty="!channels.data.value?.items.length"
        empty-text="当前站点尚无通知渠道，请添加渠道或分配旧渠道"
        @retry="channels.reload"
      >
        <el-table :data="channels.data.value?.items">
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
          <el-table-column label="接收告警类型" min-width="240">
            <template #default="s">
              {{ categorySummary(s.row.rule_keys || []) }}
            </template>
          </el-table-column>
          <el-table-column label="操作" width="90">
            <template #default="s"><el-button @click="openChannel(s.row)">编辑</el-button></template>
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
          <el-table-column label="通知渠道" min-width="140">
            <template #default="s">{{ channels.data.value?.items.find((item) => item.id === s.row.channel_id)?.name || s.row.channel_id }}</template>
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
    <el-dialog v-model="dialogOpen" :title="editing ? '编辑 / 分配通知渠道' : '添加通知渠道'" width="600px">
      <el-form :model="form" label-width="90px">
        <el-form-item label="所属站点">
          <el-input :model-value="form.site_id" disabled />
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
            :placeholder="editing ? '留空保留原 Webhook 地址' : '机器人 Webhook 地址'"
          />
        </el-form-item>
        <el-form-item v-if="form.channel_type === 'dingtalk'" label="Secret">
          <el-input
            v-model="form.secret"
            type="password"
            show-password
            :placeholder="editing ? '留空保留原加签密钥' : '加签密钥，留空为关键词模式'"
            autocomplete="new-password"
          />
        </el-form-item>
        <el-form-item label="告警类型">
          <div>
            <el-checkbox v-model="allTypes">全部类型</el-checkbox>
            <el-select v-model="selectedCategories" multiple :disabled="allTypes" placeholder="选择该渠道接收的告警类别" style="width: 100%">
              <el-option v-for="category in alertCategories" :key="category.key" :label="category.label" :value="category.key" />
            </el-select>
            <p>保存后接收所选类别下的全部告警，具体阈值在系统设置中调整。</p>
          </div>
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
