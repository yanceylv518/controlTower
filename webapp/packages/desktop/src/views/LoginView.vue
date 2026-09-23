<script setup lang="ts">
import ThemeSwitch from '../components/ThemeSwitch.vue'
import BrandMark from '../components/BrandMark.vue'
import { reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ApiError } from '@ct/shared'
import { useAuthStore } from '../stores/auth'
const form = reactive({ username: '', password: '' }); const loading = ref(false); const errorMessage = ref('')
const store = useAuthStore(); const route = useRoute(); const router = useRouter()
async function submit() { if (!form.username || !form.password || loading.value) return; loading.value = true; errorMessage.value = ''; try { await store.login(form.username, form.password); const redirect = typeof route.query.redirect === 'string' && route.query.redirect.startsWith('/') ? route.query.redirect : '/'; await router.replace(redirect) } catch (error) { errorMessage.value = error instanceof ApiError && error.status === 429 ? '已锁定，请稍后再试' : '用户名或密码错误' } finally { loading.value = false } }
</script>
<template>
  <main class="login-page">
    <div class="login-theme"><ThemeSwitch /></div>
    <div class="login-layout">
      <section class="login-story" aria-label="Control Tower 智能调度平台">
        <div class="login-brand"><div class="brand-mark"><BrandMark /></div><div><strong>Control Tower</strong><span>智能调度平台</span></div></div>
        <div class="story-copy"><div class="story-eyebrow">OBSERVE · CONNECT · CONTROL</div><h2>统一监控。<br>从容调度。</h2><p>连接每一个渠道，洞察每一次请求。<br>让复杂的运行信息，成为清晰的决策依据。</p></div>
        <svg class="control-map" viewBox="0 0 400 200" fill="none" aria-hidden="true">
          <path d="M0 50H400M0 100H400M0 150H400M50 0V200M100 0V200M150 0V200M200 0V200M250 0V200M300 0V200M350 0V200" class="map-grid" />
          <ellipse cx="200" cy="100" rx="140" ry="70" class="map-orbit" /><ellipse cx="200" cy="100" rx="90" ry="44" class="map-orbit" />
          <path d="M60 100H155M245 100H340M200 30V66M200 134V170" class="map-connection" />
          <rect x="170" y="70" width="60" height="60" rx="18" class="map-center" />
          <path d="M213 86H197a14 14 0 0 0 0 28h16" stroke="currentColor" stroke-width="3" stroke-linecap="round" /><rect x="201" y="96" width="8" height="8" rx="2" fill="currentColor" />
          <circle v-for="point in [[60,100],[340,100],[200,30],[200,170]]" :key="point.join(',')" :cx="point[0]" :cy="point[1]" r="5" class="map-node" />
        </svg>
        <div class="story-footer"><span>运行监控</span><i></i><span>渠道调权</span><i></i><span>用量分析</span></div>
      </section>
      <section class="login-form-panel">
        <div class="login-form-content">
          <span class="login-eyebrow">工作空间 / WORKSPACE</span>
          <h1>登录控制台</h1>
          <p class="login-intro">欢迎回来，使用你的账号继续。</p>
          <el-form :model="form" label-position="top" @submit.prevent="submit">
            <el-form-item label="用户名" for="login-username"><el-input id="login-username" v-model="form.username" autocomplete="username" placeholder="请输入用户名" :disabled="loading" /></el-form-item>
            <el-form-item label="密码" for="login-password"><el-input id="login-password" v-model="form.password" type="password" autocomplete="current-password" placeholder="请输入密码" show-password :disabled="loading" /></el-form-item>
            <el-alert v-if="errorMessage" class="login-error" :title="errorMessage" type="error" :closable="false" show-icon role="alert" />
            <el-button class="login-button" type="primary" :loading="loading" :disabled="!form.username || !form.password" native-type="submit"><span>登录</span><span v-if="!loading" aria-hidden="true">→</span></el-button>
          </el-form>
          <div class="login-assistance">需要开通账号或重置密码？请联系管理员。</div>
        </div>
        <div class="login-form-footer">CONTROL TOWER<span>智能调度 · 清晰掌控</span></div>
      </section>
    </div>
  </main>
</template>

<style scoped>
:global(body:has(.login-page)) { min-width: 0; }
.login-page {
  min-height:100svh; padding:64px 32px; display:flex; align-items:center; justify-content:center;
  background:radial-gradient(ellipse at 12% 18%,color-mix(in srgb,var(--ct-accent) 14%,transparent),transparent 55%),radial-gradient(ellipse at 88% 82%,color-mix(in srgb,var(--ct-primary-solid) 12%,transparent),transparent 55%),var(--ct-bg);
}
.login-theme { z-index:2; top:20px; right:24px; }
.login-layout { width:100%; max-width:1080px; min-height:620px; display:grid; grid-template-columns:1fr 1.05fr; border:1px solid color-mix(in srgb,var(--ct-accent) 25%,var(--ct-line)); border-radius:20px; overflow:hidden; background:var(--ct-surface); box-shadow:0 24px 80px color-mix(in srgb,var(--ct-ink) 12%,transparent); }
.login-story {
  padding:36px 40px 28px; display:flex; flex-direction:column; position:relative; overflow:hidden;
  /* Only darken the solid theme color so white story text retains its contrast. */
  background:radial-gradient(ellipse at 12% 8%,var(--ct-primary-solid),transparent 70%),linear-gradient(145deg,color-mix(in srgb,var(--ct-primary-solid) 85%,#000),color-mix(in srgb,var(--ct-primary-solid) 58%,#000));
  color:var(--ct-on-solid);
}
.login-brand { display:flex; align-items:center; gap:12px; }
.login-brand .brand-mark { width:38px; height:38px; border-radius:12px; background:rgb(255 255 255 / 12%); color:var(--ct-on-solid); box-shadow:inset 0 0 0 1px rgb(255 255 255 / 30%); }
.brand-mark svg { width:34px; height:34px; }
.login-brand strong { display:block; font-family:'Segoe UI',sans-serif; font-size:17px; font-weight:600; letter-spacing:-.4px; }
.login-brand span { display:block; margin-top:4px; font-size:12px; letter-spacing:.1em; color:var(--ct-on-solid); }
.story-copy { margin-top:64px; position:relative; z-index:1; }
.story-eyebrow { color:var(--ct-on-solid); font:11px/1.5 'Segoe UI',sans-serif; letter-spacing:.18em; }
.story-copy h2 { font-size:38px; line-height:1.4; font-weight:500; letter-spacing:.025em; margin:18px 0; }
.story-copy p { margin:0; font-size:14px; line-height:1.9; color:var(--ct-on-solid); }
.control-map { width:100%; max-width:380px; margin:20px auto 12px; color:var(--ct-on-solid); }
.map-grid { stroke:currentColor; stroke-opacity:.1; }
.map-orbit { stroke:currentColor; stroke-opacity:.3; }
.map-connection { stroke:currentColor; stroke-opacity:.65; stroke-dasharray:3 5; }
.map-center { fill:color-mix(in srgb,var(--ct-primary-solid) 70%,#000); stroke:currentColor; stroke-opacity:.65; }
.map-node { fill:currentColor; stroke:var(--ct-primary-solid); stroke-width:3; }
.story-footer { display:flex; align-items:center; gap:14px; margin-top:auto; font-size:12px; color:var(--ct-on-solid); }
.story-footer i { height:3px; width:3px; border-radius:50%; background:var(--ct-on-solid); }
.login-form-panel { padding:64px 56px 28px; display:flex; flex-direction:column; justify-content:center; background:var(--ct-surface); }
.login-form-content { width:100%; max-width:340px; margin:auto; }
.login-eyebrow { display:flex; align-items:center; gap:8px; color:var(--ct-accent); font-size:12px; font-weight:600; letter-spacing:.08em; }
.login-eyebrow::before { content:''; width:6px; height:6px; flex:none; border-radius:50%; background:currentColor; }
h1 { margin:16px 0 10px; font-size:28px; font-weight:600; letter-spacing:-.025em; color:var(--ct-ink); }
.login-intro { font-size:14px; color:var(--ct-ink-2); margin:0 0 34px; }
.login-form-content :deep(.el-form-item) { margin-bottom:22px; }
.login-form-content :deep(.el-form-item__label) { color:var(--ct-ink); line-height:20px; margin-bottom:8px; font-size:13px; font-weight:500; }
.login-form-content :deep(.el-input__wrapper) { min-height:44px; padding:0 14px; border-radius:8px; background:var(--ct-bg); box-shadow:0 0 0 1px var(--ct-line-strong) inset; }
.login-form-content :deep(.el-input__wrapper:hover) { box-shadow:0 0 0 1px var(--ct-accent) inset; }
.login-form-content :deep(.el-input__wrapper.is-focus) { box-shadow:0 0 0 1px var(--ct-accent) inset,0 0 0 3px var(--ct-accent-weak); }
.login-button { margin-top:6px; width:100%; min-height:44px; padding:0 16px; border-radius:8px; font-size:14px; font-weight:600; }
.login-button.is-disabled, .login-button.is-disabled:hover {
  background:var(--ct-accent-weak); color:var(--ct-accent);
  border-color:color-mix(in srgb,var(--ct-accent) 45%,var(--ct-line)); opacity:1;
}
.login-button :deep(> span) { display:flex; justify-content:center; align-items:center; gap:14px; }
.login-error { margin-bottom:16px; }
.login-assistance { margin-top:24px; color:var(--ct-ink-3); font-size:12px; line-height:1.8; text-align:center; }
.login-form-footer { margin-top:64px; padding-top:18px; border-top:1px solid var(--ct-line); display:flex; justify-content:space-between; gap:12px; color:var(--ct-ink-3); font-size:11px; letter-spacing:.05em; }
@media (max-width:850px) {
  .login-page { padding:64px 20px 24px; }
  .login-layout { max-width:480px; grid-template-columns:1fr; min-height:0; border-radius:16px; }
  .login-story { padding:24px 28px; }
  .story-copy,.control-map,.story-footer { display:none; }
  .login-form-panel { padding:36px 28px 24px; }
  .login-form-footer { margin-top:40px; }
}
@media (max-width:380px) { .login-page { padding-inline:12px; } .login-form-panel { padding-inline:22px; } .login-form-footer { letter-spacing:0; } }
</style>
