<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import {
  ArrowDown,
  Coin,
  Connection,
  DataAnalysis,
  Document,
  HomeFilled,
  Management,
  Monitor,
  Operation,
  SetUp,
  TrendCharts,
  User,
  Fold,
  Expand,
} from "@element-plus/icons-vue";
import { useAuthStore } from "../stores/auth";
import SiteSelect from "./SiteSelect.vue";

defineProps<{ title: string }>();
const auth = useAuthStore();
const route = useRoute();
const router = useRouter();
const sidebarCollapsed = ref(localStorage.getItem("ct.sidebar.collapsed") === "1");
function toggleSidebar() {
  sidebarCollapsed.value = !sidebarCollapsed.value;
  localStorage.setItem("ct.sidebar.collapsed", sidebarCollapsed.value ? "1" : "0");
}
const nav = [
  {
    group: "监控分析",
    items: [
      ["/customers", "客户监控", User],
      ["/channels", "渠道监控", Connection],
      ["/models", "模型监控", DataAnalysis],
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
      ["/billing/channels", "上游账单", Coin],
      ["/billing/tasks", "账单任务", Operation],
      ["/billing/discounts", "渠道折扣", SetUp],
    ],
  },
  {
    group: "系统管理",
    items: [
      ["/tuning", "调权中心", TrendCharts],
      ["/alerts", "告警中心", Monitor],
      ["/notifications", "通知设置", Connection],
      ["/instances", "实例管理", Management],
      ["/access-users", "访问账号", User],
      ["/models/manage", "模型管理", SetUp],
      ["/billing/upstreams", "上游管理", Connection],
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
const readonlySiteRequired = computed(() => ["数据查询", "账单管理"].includes(groupForPath(route.path)));
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
  <div class="shell" :class="{ 'sidebar-collapsed': sidebarCollapsed }">
    <aside class="sidebar">
      <div class="logo-row">
        <div class="logo"><span>CT</span><b>Control Tower</b></div>
        <button class="sidebar-toggle" type="button" :title="sidebarCollapsed ? '展开菜单' : '收起菜单'" :aria-label="sidebarCollapsed ? '展开菜单' : '收起菜单'" @click="toggleSidebar">
          <el-icon><component :is="sidebarCollapsed ? Expand : Fold" /></el-icon>
        </button>
      </div>
      <nav>
        <template v-if="auth.user?.role === 'viewer'">
          <router-link v-for="item in viewerNav" :key="item[0]" :to="item[0]" :title="sidebarCollapsed ? item[1] : undefined">
            <el-icon><component :is="item[2]" /></el-icon>
            <span>{{ item[1] }}</span>
          </router-link>
        </template>
        <template v-else>
          <router-link to="/" class="nav-home" :title="sidebarCollapsed ? '总览' : undefined">
            <el-icon><HomeFilled /></el-icon>
            <span>总览</span>
          </router-link>
        <section v-for="section in nav" :key="section.group" class="nav-section">
          <button class="nav-group" type="button" :aria-expanded="activeGroup === section.group" @click="toggleGroup(section.group)">
            <span>{{ section.group }}</span>
            <el-icon :class="{ expanded: activeGroup === section.group }"><ArrowDown /></el-icon>
          </button>
          <div v-show="sidebarCollapsed || activeGroup === section.group" class="nav-items">
            <router-link
              v-for="item in section.items"
              :key="item[0]"
              :to="item[0]"
              :title="sidebarCollapsed ? item[1] : undefined"
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
          <SiteSelect :readonly-only="readonlySiteRequired" />
          <span>{{ auth.user?.username }}</span>
          <el-button text @click="logout">退出</el-button>
        </div>
      </header>
      <section class="content"><slot /></section>
    </main>
  </div>
</template>
