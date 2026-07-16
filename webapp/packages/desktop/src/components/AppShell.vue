<script setup lang="ts">
import { useRouter } from "vue-router";
import {
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
  User,
} from "@element-plus/icons-vue";
import { useAuthStore } from "../stores/auth";
import InstanceSelect from "./InstanceSelect.vue";

defineProps<{ title: string }>();
const auth = useAuthStore();
const router = useRouter();
const nav = [
  {
    group: "监控",
    items: [
      ["/", "总览", HomeFilled],
      ["/customers", "客户监控", User],
      ["/channels", "渠道监控", Connection],
      ["/models", "模型监控", DataAnalysis],
      ["/alerts", "告警中心", Bell],
    ],
  },
  {
    group: "分析",
    items: [
      ["/samples", "样本分析", Document],
      ["/usage", "用量统计", Coin],
      ["/latency", "延时分诊", DataLine],
    ],
  },
  {
    group: "系统",
    items: [
      ["/runtime", "系统状态", Monitor],
      ["/notifications", "通知设置", Notification],
      ["/instances", "实例管理", Management],
      ["/audits", "操作审计", Operation],
      ["/settings", "设置", SetUp],
    ],
  },
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
        <section v-for="section in nav" :key="section.group">
          <div class="nav-group">{{ section.group }}</div>
          <router-link
            v-for="item in section.items"
            :key="item[0]"
            :to="item[0]"
          >
            <el-icon><component :is="item[2]" /></el-icon>
            <span>{{ item[1] }}</span>
          </router-link>
        </section>
      </nav>
    </aside>
    <main class="workspace">
      <!-- 单行工具栏：页标题 + 页级控件（#tools）+ 实例/用户。页面内不再有第二行工具条。 -->
      <header class="topbar">
        <h1>{{ title }}</h1>
        <div class="topbar-tools"><slot name="tools" /></div>
        <div class="topbar-spacer"></div>
        <div class="user">
          <InstanceSelect />
          <span>{{ auth.user?.username }}</span>
          <el-button text @click="logout">退出</el-button>
        </div>
      </header>
      <section class="content"><slot /></section>
    </main>
  </div>
</template>
