<script setup lang="ts">
import { reactive, ref } from "vue";
import { useRouter } from "vue-router";
import { ArrowDown, Lock, SwitchButton, User } from "@element-plus/icons-vue";
import { ElMessage } from "element-plus";
import { ApiClient, ApiError, authApi } from "@ct/shared";
import { useAuthStore } from "../stores/auth";

const auth = useAuthStore();
const router = useRouter();
// 密码校验失败也返回 401，在弹窗内提示，避免触发全局登录跳转。
const passwordApi = authApi(new ApiClient());
const visible = ref(false);
const saving = ref(false);
const error = ref("");
const password = reactive({ old: "", next: "", confirm: "" });
function reset() {
  password.old = password.next = password.confirm = "";
  error.value = "";
}
async function command(value: string) {
  if (value === "password") {
    reset();
    visible.value = true;
    return;
  }
  try {
    await auth.logout();
  } finally {
    await router.replace("/login");
  }
}
async function submit() {
  if (saving.value) return;
  error.value = "";
  if (!password.old) error.value = "请输入当前密码";
  else if (password.next.length < 8) error.value = "新密码至少需要 8 位";
  else if (password.next !== password.confirm) error.value = "两次输入的新密码不一致";
  if (error.value) return;
  saving.value = true;
  try {
    await passwordApi.changePassword(password.old, password.next);
    auth.user = null;
    visible.value = false;
    reset();
    ElMessage.success("密码已修改，请使用新密码重新登录");
    await router.replace("/login");
  } catch (e) {
    error.value = e instanceof ApiError && e.code === "invalid_credentials"
      ? "当前密码不正确，请重新输入"
      : e instanceof ApiError && e.status === 401
        ? "登录已过期，请重新登录后修改"
        : "修改失败，请稍后重试";
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <el-dropdown trigger="click" @command="command">
    <el-button text class="account-trigger">
      <el-icon><User /></el-icon>
      <span>{{ auth.user?.username }}</span>
      <el-icon><ArrowDown /></el-icon>
    </el-button>
    <template #dropdown>
      <el-dropdown-menu>
        <el-dropdown-item command="password" :icon="Lock">修改密码</el-dropdown-item>
        <el-dropdown-item command="logout" :icon="SwitchButton" divided>退出登录</el-dropdown-item>
      </el-dropdown-menu>
    </template>
  </el-dropdown>
  <el-dialog v-model="visible" title="修改密码" width="440px" style="max-width: calc(100vw - 32px)" append-to-body destroy-on-close :close-on-click-modal="false" :close-on-press-escape="!saving" :show-close="!saving" @closed="reset">
    <p class="password-note">修改后，此账号在所有设备上的登录都会失效，需要使用新密码重新登录。</p>
    <el-form label-position="top" :disabled="saving" @submit.prevent="submit">
      <el-form-item label="当前密码">
        <el-input v-model="password.old" type="password" show-password autocomplete="current-password" placeholder="请输入当前密码" />
      </el-form-item>
      <el-form-item label="新密码">
        <el-input v-model="password.next" type="password" show-password autocomplete="new-password" placeholder="至少 8 位" />
      </el-form-item>
      <el-form-item label="确认新密码">
        <el-input v-model="password.confirm" type="password" show-password autocomplete="new-password" placeholder="再次输入新密码" @keydown.enter.prevent="submit" />
      </el-form-item>
      <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon role="alert" />
    </el-form>
    <template #footer>
      <el-button :disabled="saving" @click="visible = false">取消</el-button>
      <el-button type="primary" :loading="saving" @click="submit">保存并重新登录</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.account-trigger :deep(> span) { display: flex; align-items: center; gap: 8px; }
.password-note { margin: 0 0 22px; color: var(--el-text-color-secondary); font-size: 13px; line-height: 1.6; }
</style>
