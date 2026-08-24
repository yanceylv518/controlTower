<script setup lang="ts">
import { ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import {
  ArrowDown,
  Bell,
  Coin,
  Connection,
  DataAnalysis,
  DataLine,
  Document,
  HomeFilled,
  Management,
  Monitor,
  Notification,
  Operation,
  SetUp,
  TrendCharts,
  User,
} from "@element-plus/icons-vue";
import { useAuthStore } from "../stores/auth";
import SiteSelect from "./SiteSelect.vue";

defineProps<{ title: string }>();
const auth = useAuthStore();
const route = useRoute();
const router = useRouter();
const nav = [
  {
    group: "监控分析",
    items: [
      ["/customers", "客户监控", User],
      ["/channels", "渠道监控", Connection],
      ["/models", "模型监控", DataAnalysis],
      ["/alerts", "告警中心", Bell],
      ["/samples", "样本分析", Document],
      ["/latency", "延时分诊", DataLine],
      ["/runtime", "系统状态", Monitor],
    ],
  },
  {
    group: "数据查询",
    items: [
      ["/usage", "用量统计", Coin],
      ["/readonly-users", "用户管理", User],
      ["/readonly-logs", "使用日志", Document],
    ],
  },
  {
    group: "账单管理",
    items: [
      ["/billing", "用户账单", Coin],
      ["/billing/channels", "渠道账单", Coin],
      ["/billing/generated", "已生成账单", Document],
      ["/billing/tasks", "后台任务中心", Operation],
      ["/billing-reconciliation", "账单核对", DataAnalysis],
      ["/models/manage", "模型管理", SetUp],
    ],
  },
  {
    group: "系统管理",
    items: [
      ["/tuning", "调权中心", TrendCharts],
      ["/instances", "实例管理", Management],
      ["/notifications", "通知设置", Notification],
      ["/audits", "操作审计", Operation],
      ["/access-users", "访问账号", User],
      ["/settings", "系统设置", SetUp],
    ],
  },
] as const;
function groupForPath(path: string) {
  if (path === "/") return "";
  const exact = nav.find((section) => section.items.some((item) => item[0] === path));
  if (exact) return exact.group;
  const nested = nav.find((section) => section.items.some((item) => path.startsWith(`${item[0]}/`)));
  return nested?.group ?? "";
}
const activeGroup = ref<string>(groupForPath(route.path));
watch(() => route.path, (path) => { activeGroup.value = groupForPath(path); });
function toggleGroup(group: string) {
  activeGroup.value = activeGroup.value === group ? "" : group;
}
const viewerNav = [
  ["/customers", "客户监控", User],
  ["/readonly-users", "用户管理", User],
  ["/readonly-logs", "使用日志", Document],
] as const;
async function logout() {
  await auth.logout();
  await router.replace("/login");
}
</script>
<template>
  <div class="shell">
    <aside class="sidebar">
      <div class="logo"><span>CT</span> Control Tower</div>
      <nav>
        <template v-if="auth.user?.role === 'viewer'">
          <router-link v-for="item in viewerNav" :key="item[0]" :to="item[0]">
            <el-icon><component :is="item[2]" /></el-icon>
            <span>{{ item[1] }}</span>
          </router-link>
        </template>
        <template v-else>
          <router-link to="/" class="nav-home">
            <el-icon><HomeFilled /></el-icon>
            <span>总览</span>
          </router-link>
        <section v-for="section in nav" :key="section.group" class="nav-section">
          <button class="nav-group" type="button" :aria-expanded="activeGroup === section.group" @click="toggleGroup(section.group)">
            <span>{{ section.group }}</span>
            <el-icon :class="{ expanded: activeGroup === section.group }"><ArrowDown /></el-icon>
          </button>
          <div v-show="activeGroup === section.group" class="nav-items">
            <router-link
              v-for="item in section.items"
              :key="item[0]"
              :to="item[0]"
            >
              <el-icon><component :is="item[2]" /></el-icon>
              <span>{{ item[1] }}</span>
            </router-link>
          </div>
        </section>
        </template>
      </nav>
    </aside>
    <main class="workspace">
      <!-- 单行工具栏：页标题 + 页级控件（#tools）+ 实例/用户。页面内不再有第二行工具条。 -->
      <header class="topbar">
        <h1>{{ title }}</h1>
        <div class="topbar-tools"><slot name="tools" /></div>
        <div class="topbar-spacer"></div>
        <div class="user">
          <SiteSelect />
          <span>{{ auth.user?.username }}</span>
          <el-button text @click="logout">退出</el-button>
        </div>
      </header>
      <section class="content"><slot /></section>
    </main>
  </div>
</template>
