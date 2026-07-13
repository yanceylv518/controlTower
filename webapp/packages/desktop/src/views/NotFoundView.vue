<script setup lang="ts">import AppShell from '../components/AppShell.vue';</script>
<template><AppShell title="页面不存在"><el-result icon="warning" title="404" sub-title="你访问的页面不存在"><template #extra><router-link to="/"><el-button type="primary">返回总览</el-button></router-link></template></el-result></AppShell></template>
