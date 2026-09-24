<script setup lang="ts">
defineProps<{
  reason: string
}>()
</script>

<template>
  <span
    class="stream-status-error"
    tabindex="0"
    role="img"
    :aria-label="reason ? `流状态：错误，${reason}` : '流状态：错误'"
  >
    <svg aria-hidden="true" viewBox="0 0 16 16" fill="none">
      <circle cx="8" cy="8" r="6.25" stroke="currentColor" stroke-width="1.5" />
      <path d="M8 4.5v4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
      <circle cx="8" cy="11.25" r="0.8" fill="currentColor" />
    </svg>
    <span class="stream-status-tooltip" aria-hidden="true">
      <strong>流状态：错误</strong>
      <span v-if="reason">{{ reason }}</span>
    </span>
  </span>
</template>

<style scoped>
.stream-status-error {
  position: relative;
  display: inline-flex;
  width: 14px;
  height: 14px;
  flex: 0 0 14px;
  align-items: center;
  justify-content: center;
  color: var(--ct-log-tooltip-danger);
  cursor: help;
  outline: none;
  vertical-align: middle;
}

.stream-status-error svg { display: block; width: 14px; height: 14px; }
.stream-status-error:focus-visible { border-radius: 50%; outline: 2px solid currentColor; outline-offset: 2px; }

.stream-status-tooltip {
  position: absolute;
  z-index: 4100;
  bottom: calc(100% + 6px);
  left: 50%;
  display: flex;
  min-width: max-content;
  max-width: min(260px, calc(100vw - 24px));
  flex-direction: column;
  gap: 2px;
  padding: 6px 10px;
  border: 1px solid var(--ct-log-tooltip-border);
  border-radius: 6px;
  background: var(--ct-log-tooltip-bg);
  box-shadow: var(--ct-log-tooltip-shadow);
  color: var(--ct-log-tooltip-ink);
  font-size: 12px;
  line-height: 18px;
  opacity: 0;
  pointer-events: none;
  transform: translate(-50%, 3px);
  transition: opacity 100ms ease, transform 100ms ease, visibility 100ms ease;
  visibility: hidden;
  white-space: normal;
}

.stream-status-tooltip strong { color: var(--ct-log-tooltip-ink); font-weight: 600; white-space: nowrap; }
.stream-status-tooltip > span { overflow-wrap: anywhere; color: var(--ct-log-tooltip-muted); font-family: ui-monospace, SFMono-Regular, Consolas, monospace; }
.stream-status-error:hover .stream-status-tooltip,
.stream-status-error:focus-visible .stream-status-tooltip {
  opacity: 1;
  transform: translate(-50%, 0);
  visibility: visible;
}

@media (prefers-reduced-motion: reduce) {
  .stream-status-tooltip { transition: none; }
}
</style>
