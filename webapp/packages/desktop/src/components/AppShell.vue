<script setup lang="ts">
import { computed, ref, watch, onMounted, onUnmounted } from "vue";
import { useRoute } from "vue-router";
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
import { canVisit } from "../permissions";
import { useMobileViewport } from "../composables/useMobileViewport";
import SiteSelect from "./SiteSelect.vue";
import ThemeSwitch from "./ThemeSwitch.vue";
import AccountMenu from "./AccountMenu.vue";
import BrandMark from "./BrandMark.vue";
import { useMenuVisibilityStore } from '../stores/menuVisibility';

defineProps<{ title: string }>();
const auth = useAuthStore();
const route = useRoute();
const menuVisibility = useMenuVisibilityStore();
let menuTimer: ReturnType<typeof setInterval> | undefined;
const refreshMenus = () => { if (document.visibilityState === 'visible') void menuVisibility.load(); };
onMounted(() => { void menuVisibility.load(); menuTimer = setInterval(refreshMenus, 30000); document.addEventListener('visibilitychange', refreshMenus); window.addEventListener('focus', refreshMenus); });
onUnmounted(() => { clearInterval(menuTimer); document.removeEventListener('visibilitychange', refreshMenus); window.removeEventListener('focus', refreshMenus); });
const mobile = useMobileViewport();
const moreOpen = ref(false), menuSearch = ref('');
const monitorPaths = ['/customers', '/trial-followup', '/channels', '/models', '/runtime'];
const mobileMonitor = computed(() => monitorPaths.find(path => menuVisibility.visible(path) && canVisit(auth.user, path)));
const mobileNav = computed(() => [
  { path:'/', label:'总览', icon:HomeFilled },
  ...(mobileMonitor.value ? [{ path:mobileMonitor.value, label:'监控', icon:Monitor }] : []),
  { path:'/tuning', label:'调权', icon:TrendCharts },
  { path:'/readonly-logs', label:'日志', icon:Document },
].filter(item => menuVisibility.visible(item.path) && canVisit(auth.user, item.path)));
const mobileMenu = computed(() => visibleNav.value.map(section => ({ ...section, items:section.items.filter(item => item[1].includes(menuSearch.value.trim())) })).filter(section => section.items.length));
const navActive = (path: string) => path === mobileMonitor.value ? route.path !== '/models/manage' && monitorPaths.some(base => route.path === base || route.path.startsWith(base + '/')) : path === '/' ? route.path === '/' : route.path === path || route.path.startsWith(path + '/');
watch(() => route.path, () => { moreOpen.value = false; menuSearch.value = ''; });
watch(mobile, value => { if (!value) moreOpen.value = false; });
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
      ["/trial-followup", "测试跟进", User],
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
      ["/container-logs", "容器日志", Document],
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
      ["/notifications", "通知中心", Connection],
      ["/instances", "实例管理", Management],
      ["/log-archives", "日志归档", Document],
      ["/access-users", "账号管理", User],
      ["/audits", "操作审计", Document],
      ["/models/manage", "模型广场", SetUp],
      ["/billing/upstreams", "上游管理", Connection],
      ["/settings", "系统设置", SetUp],
    ],
  },
] as const;
const visibleNav = computed(() => nav.map(section => ({ ...section, items: section.items.filter(item => menuVisibility.visible(item[0]) && canVisit(auth.user, item[0])) })).filter(section => section.items.length));
function groupForPath(path: string) {
  if (path === "/") return "";
  const exact = nav.find((section) => section.items.some((item) => item[0] === path));
  if (exact) return exact.group;
  const nested = nav.find((section) => section.items.some((item) => path.startsWith(`${item[0]}/`)));
  return nested?.group ?? "";
}
const activeGroup = ref<string>(groupForPath(route.path));
const readonlySiteRequired = computed(() => route.path !== "/container-logs" && ["数据查询", "账单管理"].includes(groupForPath(route.path)));
watch(() => route.path, (path) => { activeGroup.value = groupForPath(path); });
function toggleGroup(group: string) {
  activeGroup.value = activeGroup.value === group ? "" : group;
}
const viewerNav = [
  ["/customers", "客户监控", User],
  ["/readonly-users", "用户管理", User],
  ["/readonly-logs", "使用日志", Document],
] as const;
const visibleViewerNav = computed(() => viewerNav.filter(item => menuVisibility.visible(item[0])));
</script>
<template>
  <div class="shell" :class="{ 'sidebar-collapsed': sidebarCollapsed, 'viewer-shell': auth.user?.role === 'viewer', 'admin-shell': auth.user?.role === 'admin' }">
    <aside class="sidebar">
      <div class="logo-row">
        <div class="logo" title="Control Tower · 智能调度平台"><span><BrandMark /></span><b>Control Tower<small>智能调度平台</small></b></div>
        <button class="sidebar-toggle" type="button" :title="sidebarCollapsed ? '展开菜单' : '收起菜单'" :aria-label="sidebarCollapsed ? '展开菜单' : '收起菜单'" @click="toggleSidebar">
          <el-icon><component :is="sidebarCollapsed ? Expand : Fold" /></el-icon>
        </button>
      </div>
      <nav aria-label="主导航">
        <button v-if="!menuVisibility.ready && menuVisibility.error" class="nav-group" @click="menuVisibility.load()">菜单加载失败，点击重试</button>
        <template v-if="auth.user?.role === 'viewer'">
          <router-link v-for="item in visibleViewerNav" :key="item[0]" :to="item[0]" :title="sidebarCollapsed ? item[1] : undefined">
            <el-icon><component :is="item[2]" /></el-icon>
            <span>{{ item[1] }}</span>
          </router-link>
        </template>
        <template v-else>
          <router-link v-if="menuVisibility.visible('/') && canVisit(auth.user, '/')" to="/" class="nav-home" :title="sidebarCollapsed ? '总览' : undefined">
            <el-icon><HomeFilled /></el-icon>
            <span>总览</span>
          </router-link>
        <section v-for="section in visibleNav" :key="section.group" class="nav-section">
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
        <span v-if="!mobile && groupForPath(route.path)" class="topbar-section">{{ groupForPath(route.path) }}<span aria-hidden="true">/</span></span>
        <span class="mobile-brand" aria-hidden="true">CT</span>
        <h1>{{ title }}</h1>
        <div class="topbar-tools"><slot name="tools" /></div>
        <div class="topbar-spacer"></div>
        <div class="user">
          <SiteSelect :readonly-only="readonlySiteRequired" />
          <ThemeSwitch />
          <AccountMenu />
        </div>
      </header>
      <section class="content">
        <nav v-if="mobile && auth.user?.role === 'admin' && monitorPaths.includes(route.path)" class="mobile-monitor-tabs" aria-label="监控分类">
          <router-link v-for="item in visibleNav.find(section => section.group === '监控分析')?.items || []" :key="item[0]" :to="item[0]">{{ item[1].replace('监控','') }}</router-link>
        </nav>
        <slot />
      </section>
    </main>
    <nav v-if="auth.user?.role === 'viewer'" class="viewer-bottom-nav" aria-label="主要导航">
      <router-link v-for="item in visibleViewerNav" :key="item[0]" :to="item[0]">
        <el-icon><component :is="item[2]" /></el-icon><span>{{ item[1] }}</span>
      </router-link>
    </nav>
    <nav v-if="mobile && auth.user?.role === 'admin'" class="admin-bottom-nav" aria-label="管理员主要导航" :style="{ gridTemplateColumns: `repeat(${mobileNav.length + 1}, minmax(0,1fr))` }">
      <router-link v-for="item in mobileNav" :key="item.path" :to="item.path" :class="{ selected:!moreOpen && navActive(item.path) }" :aria-current="!moreOpen && navActive(item.path) ? 'page' : undefined"><el-icon><component :is="item.icon" /></el-icon><span>{{ item.label }}</span></router-link>
      <button type="button" :class="{ selected:moreOpen || !mobileNav.some(item => navActive(item.path)) }" :aria-expanded="moreOpen" @click="moreOpen = true"><el-icon><Management /></el-icon><span>更多</span></button>
    </nav>
    <el-drawer v-if="mobile && auth.user?.role === 'admin'" v-model="moreOpen" title="更多功能" direction="btt" size="92%" class="admin-more-drawer" append-to-body>
      <el-input v-model="menuSearch" clearable placeholder="搜索功能" aria-label="搜索功能" />
      <section v-for="section in mobileMenu" :key="section.group" class="mobile-menu-section"><h3>{{ section.group }}</h3><router-link v-for="item in section.items" :key="item[0]" :to="item[0]" @click="moreOpen = false"><el-icon><component :is="item[2]" /></el-icon>{{ item[1] }}<span>›</span></router-link></section>
      <el-empty v-if="!mobileMenu.length" description="没有匹配功能" />
    </el-drawer>
  </div>
</template>

<style>
.mobile-brand,.viewer-bottom-nav { display:none; }
@media(max-width:900px) {
  body:has(.viewer-shell),body:has(.login-page) { min-width:0; }
  .login-card { width:min(400px,calc(100vw - 32px));padding:28px; }
  .shell.viewer-shell .sidebar { display:none!important; }
  .shell.viewer-shell .workspace { width:100%!important;margin-left:0!important;min-width:0; }
  .shell.viewer-shell .content { min-width:0;width:100%;padding:12px 12px calc(84px + env(safe-area-inset-bottom))!important; }
  .shell.viewer-shell .topbar { height:auto;min-height:60px;gap:8px;padding:8px 12px!important;flex-wrap:wrap; }
  .shell.viewer-shell .topbar h1 { font-size:16px!important;margin:0; }
  .shell.viewer-shell .mobile-brand { display:block;color:var(--ct-accent);font-size:20px;font-weight:800;font-style:italic; }
  .shell.viewer-shell .topbar-tools { order:5;flex:1 0 100%;flex-wrap:wrap; }
  .shell.viewer-shell .topbar-tools:empty { display:none; }
  .shell.viewer-shell .user { margin-left:auto;gap:4px;min-width:0; }
  .shell.viewer-shell .user .fixed-site,.shell.viewer-shell .user>.el-select { width:clamp(80px,28vw,160px)!important; }
  .shell.viewer-shell .account-trigger { min-width:40px;min-height:44px;padding:8px; }
  .shell.viewer-shell .account-trigger>span>span,.shell.viewer-shell .account-trigger>span>.el-icon:last-child { display:none; }
  .shell.viewer-shell .viewer-bottom-nav { position:fixed;inset:auto 0 0;z-index:100;display:grid;grid-template-columns:repeat(3,minmax(0,1fr));padding:6px 8px calc(6px + env(safe-area-inset-bottom));background:var(--ct-surface);border-top:1px solid var(--ct-line); }
  .viewer-bottom-nav a { display:flex;align-items:center;justify-content:center;flex-direction:column;gap:4px;min-height:52px;border-radius:8px;color:var(--ct-ink-2);text-decoration:none;font-size:12px; }
  .viewer-bottom-nav .el-icon { font-size:22px; }
  .viewer-bottom-nav a.router-link-active { color:var(--ct-accent);background:var(--ct-accent-weak);font-weight:600; }
}
</style>
