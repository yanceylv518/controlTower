import type { OverviewPoint } from '@ct/shared';

export function overviewFresh(value: string | null | undefined, now: number, seconds = 120) {
  const at = value ? Date.parse(value) : NaN;
  return Number.isFinite(at) && at <= now + 5000 && now - at <= seconds * 1000;
}
export function overviewTrend(point: OverviewPoint) {
  const minutes = (Date.parse(point.to) - Date.parse(point.from)) / 60000;
  if (!point.complete || !point.metrics || minutes <= 0) return { tpm: null, success: null };
  return { tpm: point.metrics.tpm / minutes, success: point.metrics.success_rate == null ? null : point.metrics.success_rate * 100 };
}
export function overviewWindow(from: string, to: string) {
  const time = (v: string) => new Date(v).toLocaleTimeString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false, hour: '2-digit', minute: '2-digit' });
  return `${time(from)}—${time(to)}`;
}
// One generation owns all section writes. Aborting a fetch alone is insufficient:
// a response already parsed before the abort can otherwise overwrite a new site.
export function overviewLoader(timeoutMs = 12000) {
  let generation = 0;
  let active: AbortController | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const cancel = () => { generation++; active?.abort(); active = undefined; clearTimeout(timer); };
  return {
    get busy() { return !!active; },
    cancel,
    start() {
      cancel();
      const controller = new AbortController();
      active = controller;
      const id = generation;
      timer = setTimeout(() => controller.abort(), timeoutMs);
      return { signal: controller.signal, current: () => id === generation,
        finish() { if (id === generation) { active = undefined; clearTimeout(timer); } },
      };
    },
  };
}
