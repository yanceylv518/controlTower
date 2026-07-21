import { defineStore } from "pinia";
import { dashboard } from "../api";

// Display preferences sourced from system settings; safe defaults apply
// before the settings request resolves or when it fails.
export const usePrefsStore = defineStore("prefs", {
  state: () => ({ quotaPerUnit: 500000, currencySymbol: "¥", ttftP50Threshold: 3, ttftP90Threshold: 30, ttftP95Threshold: 60, loaded: false }),
  actions: {
    async load(force = false) {
      if (this.loaded && !force) return;
      this.loaded = true;
      try {
        const response = await dashboard.settings();
        const per = Number(response.items.CT_QUOTA_PER_UNIT?.value);
        if (Number.isFinite(per) && per > 0) this.quotaPerUnit = per;
        const symbol = response.items.CT_CURRENCY_SYMBOL?.value?.trim();
        if (symbol) this.currencySymbol = symbol;
        const p50 = Number(response.items.CT_TTFT_P50_THRESHOLD_SECONDS?.value);
        const p90 = Number(response.items.CT_TTFT_P90_THRESHOLD_SECONDS?.value);
        const p95 = Number(response.items.CT_TTFT_P95_THRESHOLD_SECONDS?.value);
        if (Number.isFinite(p50) && p50 > 0) this.ttftP50Threshold = p50;
        if (Number.isFinite(p90) && p90 > p50) this.ttftP90Threshold = p90;
        if (Number.isFinite(p95) && p95 > p90) this.ttftP95Threshold = p95;
      } catch {
        this.loaded = false;
      }
    },
  },
});
