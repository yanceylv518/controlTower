<script setup lang="ts">
import { reactive, ref, watch, onBeforeUnmount } from "vue";
import { ElMessage } from "element-plus";
import { Plus } from "@element-plus/icons-vue";
import type { NotificationChannelInput, NotificationChannelItem, NotificationDeliveryItem } from "@ct/shared";
import { dashboard } from "../api";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import ListPager from "../components/ListPager.vue";
import { deliveryStatus, deliveryTone, retryTime } from "../utils/notificationDelivery";
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
const currentChannel = ref<NotificationChannelItem>();
const resending = ref(false);
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
const activeTab = ref('channels');
const selectedDelivery = ref<NotificationDeliveryItem | null>(null);
const advancedSupported = ref(false);
const searchDraft = reactive({channel:'',status:'',search:'',range:[] as string[]});
const applied = ref({channel:'',status:'',search:'',range:[] as string[]});
const channelName = (id:string) => channels.data.value?.items.find(item=>item.id===id)?.name || '渠道已不可用';
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
  const query=applied.value;
  const start=query.range[0] ? new Date(query.range[0]+'T00:00:00') : undefined;
  const end=query.range[1] ? new Date(query.range[1]+'T00:00:00') : undefined;
  if(end) end.setDate(end.getDate()+1);
  const result = await dashboard.notificationDeliveries({
    site_id: site, limit: size, offset: (page - 1) * size,
    channel_id:query.channel || undefined,status:query.status || undefined,
    search:query.search || undefined,
    start_time:start?.toISOString(),
    end_time:end?.toISOString(),
  });
  if (site !== filters.site_id || page !== deliveryPage.value || size !== deliveryPageSize.value || query !== applied.value) return loadDeliveries();
  advancedSupported.value=result.filters_supported === true;
  return result.items;
}
const deliveries = useAsyncData(loadDeliveries);
watch([deliveryPage, deliveryPageSize], () => void deliveries.reload());
watch(() => filters.site_id, () => {
  dialogOpen.value = false;
  selectedDelivery.value=null;advancedSupported.value=false;
  resetSearch(false);
  channels.data.value = undefined;
  deliveries.data.value = undefined;
  deliveryPage.value = 1;
  void channels.reload();
  if(activeTab.value==='deliveries') void deliveries.reload();
});
function openChannel(channel?: NotificationChannelItem) {
  editing.value = !!channel;
  currentChannel.value=channel;
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
  if(resending.value) return;
  resending.value=true;
  try {
    await dashboard.resendDelivery(id, filters.site_id);
    ElMessage.success("已安排重发");
    selectedDelivery.value=null;
    await deliveries.reload();
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : "重发失败");
  } finally {
    resending.value=false;
  }
}
useAutoRefresh(channels.reload);
useAutoRefresh((silent)=>activeTab.value==='deliveries'?deliveries.reload(silent):undefined);
watch(activeTab,tab=>{if(tab==='deliveries')void deliveries.reload()});
function searchDeliveries() {
  applied.value={...searchDraft,search:searchDraft.search.trim(),range:[...(searchDraft.range || [])]};
  if(deliveryPage.value!==1)deliveryPage.value=1;else void deliveries.reload();
}
function resetSearch(load=true) {
  Object.assign(searchDraft,{channel:'',status:'',search:'',range:[]});
  applied.value={channel:'',status:'',search:'',range:[]};
  if(load){if(deliveryPage.value!==1)deliveryPage.value=1;else void deliveries.reload()}
}
onBeforeUnmount(()=>{channels.cancel();deliveries.cancel()});
</script>
<template>
  <AppShell title="通知中心">
    <section class="notification-center">
      <div class="notification-actions">
        <el-button v-if="activeTab==='channels'" type="primary" :icon="Plus" :disabled="!filters.site_id" @click="openChannel()">添加渠道</el-button>
        <el-button v-else :loading="deliveries.loading.value" @click="deliveries.reload()">刷新</el-button>
      </div>
      <el-tabs v-model="activeTab">
        <el-tab-pane label="通知渠道" name="channels">
          <p class="notification-caption">配置通知方式及接收的告警类型。多个渠道匹配时，各发送一份。</p>
          <section v-if="channels.data.value?.unassigned.length" class="legacy-channels">
            <el-alert title="旧渠道尚未绑定站点，已暂停投递。请确认归属后分配。" type="warning" :closable="false" />
            <el-table v-mobile-cards :data="channels.data.value.unassigned"><el-table-column prop="name" label="待分配渠道"/><el-table-column label="操作" width="220"><template #default="{row}"><el-button :disabled="!filters.site_id" @click="openChannel(row)">分配到当前站点</el-button></template></el-table-column></el-table>
          </section>
          <AsyncPanel :loading="channels.loading.value" :error="channels.error.value" :empty="!channels.data.value?.items.length" empty-text="当前站点尚无通知渠道" @retry="channels.reload">
            <el-table v-mobile-cards :data="channels.data.value?.items" class="notification-table">
              <el-table-column prop="name" label="渠道名称" min-width="200"/>
              <el-table-column label="通知方式" min-width="170"><template #default="{row}">{{ typeLabels[row.channel_type] || row.channel_type }}</template></el-table-column>
              <el-table-column label="接收告警" min-width="260"><template #default="{row}">{{ categorySummary(row.rule_keys || []) }}</template></el-table-column>
              <el-table-column label="状态" width="110"><template #default="{row}"><el-tag :type="row.enabled?'success':'info'" effect="plain">{{ row.enabled?'已启用':'已停用' }}</el-tag></template></el-table-column>
              <el-table-column label="操作" width="100"><template #default="{row}"><el-button link type="primary" @click="openChannel(row)">编辑</el-button></template></el-table-column>
            </el-table>
            <p class="notification-caption">共 {{ channels.data.value?.items.length || 0 }} 个渠道</p>
          </AsyncPanel>
        </el-tab-pane>
        <el-tab-pane label="投递记录" name="deliveries">
          <form class="delivery-filters" @submit.prevent="searchDeliveries">
            <el-date-picker v-model="searchDraft.range" type="daterange" value-format="YYYY-MM-DD" range-separator="至" start-placeholder="开始日期" end-placeholder="结束日期" :disabled="!advancedSupported"/>
            <el-select v-model="searchDraft.channel" placeholder="全部渠道" aria-label="通知渠道" clearable><el-option v-for="channel in channels.data.value?.items || []" :key="channel.id" :label="channel.name" :value="channel.id"/></el-select>
            <el-select v-model="searchDraft.status" placeholder="全部状态" aria-label="投递状态" clearable><el-option v-for="status in ['sent','failed','exhausted','expired','pending']" :key="status" :label="deliveryStatus(status)" :value="status"/></el-select>
            <el-input v-model="searchDraft.search" placeholder="搜索告警内容" aria-label="告警内容" :disabled="!advancedSupported" :maxlength="120" clearable/>
            <el-button type="primary" native-type="submit">查询</el-button><el-button @click="resetSearch()">重置</el-button>
          </form>
          <p v-if="!advancedSupported && !deliveries.loading.value" class="notification-caption">当前服务版本暂不支持日期、内容筛选及告警摘要；渠道和状态筛选仍可使用。</p>
          <AsyncPanel :loading="deliveries.loading.value" :error="deliveries.error.value" :empty="!deliveries.data.value?.length" empty-text="暂无匹配的投递记录" @retry="deliveries.reload">
            <el-table v-mobile-cards :data="deliveries.data.value" class="notification-table" max-height="calc(100vh - 310px)">
              <el-table-column label="时间" width="165"><template #default="{row}">{{ formatTime(row.attempted_at) }}</template></el-table-column>
              <el-table-column label="通知渠道" min-width="160"><template #default="{row}">{{ channelName(row.channel_id) }}</template></el-table-column>
              <el-table-column label="告警摘要" min-width="260" show-overflow-tooltip><template #default="{row}">{{ row.alert_title || row.alert_summary || '告警摘要暂不可用' }}</template></el-table-column>
              <el-table-column label="投递状态" min-width="160"><template #default="{row}"><el-tag :type="deliveryTone(row.status)" effect="plain">{{ deliveryStatus(row.status) }}</el-tag><div v-if="['failed','exhausted'].includes(row.status) && row.error_summary" class="delivery-error" :title="row.error_summary">{{ row.error_summary }}</div></template></el-table-column>
              <el-table-column prop="attempts" label="尝试次数" width="90" align="center"/>
              <el-table-column label="下次重试" width="165"><template #default="{row}">{{ retryTime(row) ? formatTime(retryTime(row)!) : '—' }}</template></el-table-column>
              <el-table-column label="操作" width="105" fixed="right"><template #default="{row}"><el-button link type="primary" @click="selectedDelivery=row">查看详情</el-button></template></el-table-column>
            </el-table>
          </AsyncPanel>
          <ListPager v-model:page="deliveryPage" v-model:page-size="deliveryPageSize" :item-count="deliveries.data.value?.length || 0"/>
        </el-tab-pane>
      </el-tabs>
    </section>
    <el-drawer :model-value="!!selectedDelivery" title="投递详情" size="min(520px, 100vw)" @close="selectedDelivery=null">
      <template v-if="selectedDelivery"><el-descriptions :column="1" border>
        <el-descriptions-item label="通知渠道">{{ channelName(selectedDelivery.channel_id) }}</el-descriptions-item>
        <el-descriptions-item label="告警">{{ selectedDelivery.alert_title || '告警摘要暂不可用' }}</el-descriptions-item>
        <el-descriptions-item label="告警内容">{{ selectedDelivery.alert_summary || '—' }}</el-descriptions-item>
        <el-descriptions-item label="状态">{{ deliveryStatus(selectedDelivery.status) }}</el-descriptions-item>
        <el-descriptions-item label="投递时间">{{ formatTime(selectedDelivery.attempted_at) }}</el-descriptions-item>
        <el-descriptions-item label="尝试次数">{{ selectedDelivery.attempts }}</el-descriptions-item>
        <el-descriptions-item label="下次重试">{{ retryTime(selectedDelivery) ? formatTime(retryTime(selectedDelivery)!) : '—' }}</el-descriptions-item>
        <el-descriptions-item label="HTTP 状态码">{{ selectedDelivery.status_code || '—' }}</el-descriptions-item>
        <el-descriptions-item label="错误详情"><span class="detail-wrap">{{ selectedDelivery.error_summary || '—' }}</span></el-descriptions-item>
        <el-descriptions-item label="告警 ID"><span class="detail-wrap">{{ selectedDelivery.alert_id }}</span></el-descriptions-item>
      </el-descriptions><el-button v-if="['failed','exhausted'].includes(selectedDelivery.status)" :loading="resending" class="resend-action" @click="resend(selectedDelivery.id)">重新投递</el-button></template>
    </el-drawer>
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
        <el-form-item v-if="currentChannel" label="当前地址"><span class="detail-wrap">{{ currentChannel.webhook_url_masked }}</span></el-form-item>
        <el-form-item v-if="currentChannel?.has_secret" label="加签状态">已配置密钥</el-form-item>
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
            <p>渠道熔断包含实际熔断和恢复通知，不推送观察模式事件。熔断阈值在调权中心调整，系统告警阈值在系统设置中调整。</p>
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

<style scoped>
.notification-center{position:relative;min-width:0;background:var(--ct-surface);border:1px solid var(--ct-line);border-radius:8px;padding:0 16px 12px}
.notification-actions{position:absolute;right:16px;top:8px;z-index:2}.notification-center :deep(.el-tabs__header){padding-right:130px;margin-bottom:12px}.notification-center :deep(.el-tabs__item){height:48px}
.notification-caption{font-size:12px;color:var(--ct-ink-3);line-height:1.7;margin:12px 0}.legacy-channels{margin:12px 0 20px}
.notification-table :deep(td.el-table__cell){padding:14px 0}.delivery-filters{display:flex;flex-wrap:wrap;align-items:center;gap:10px;margin:14px 0}.delivery-filters :deep(.el-date-editor){width:280px;flex:none}.delivery-filters :deep(.el-select){width:150px}.delivery-filters :deep(.el-input){width:200px}.delivery-filters .el-button+.el-button{margin-left:0}
.delivery-error{font-size:11px;color:var(--ct-ink-3);max-width:220px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;margin-top:5px}.detail-wrap{overflow-wrap:anywhere}.resend-action{margin-top:20px}
@media(max-width:760px){.notification-center{padding-inline:10px}.delivery-filters :deep(.el-date-editor){max-width:100%}}
</style>
