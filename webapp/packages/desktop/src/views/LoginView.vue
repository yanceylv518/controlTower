<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ApiError } from '@ct/shared'
import { useAuthStore } from '../stores/auth'
const form = reactive({ username: '', password: '' }); const loading = ref(false); const errorMessage = ref('')
const store = useAuthStore(); const route = useRoute(); const router = useRouter()
async function submit() { if (!form.username || !form.password || loading.value) return; loading.value = true; errorMessage.value = ''; try { await store.login(form.username, form.password); const redirect = typeof route.query.redirect === 'string' && route.query.redirect.startsWith('/') ? route.query.redirect : '/'; await router.replace(redirect) } catch (error) { errorMessage.value = error instanceof ApiError && error.status === 429 ? '已锁定，请稍后再试' : '用户名或密码错误' } finally { loading.value = false } }
</script>
<template><main class="login-page"><section class="login-card"><div class="brand-mark">CT</div><h1>Control Tower</h1><p>登录监控管理台</p><el-form :model="form" @submit.prevent="submit"><el-form-item><el-input v-model="form.username" autocomplete="username" placeholder="用户名" /></el-form-item><el-form-item><el-input v-model="form.password" type="password" autocomplete="current-password" placeholder="密码" show-password @keyup.enter="submit" /></el-form-item><el-alert v-if="errorMessage" :title="errorMessage" type="error" :closable="false" show-icon /><el-button class="login-button" type="primary" :loading="loading" native-type="submit">登录</el-button></el-form></section></main></template>
