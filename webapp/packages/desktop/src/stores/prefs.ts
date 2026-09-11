import { defineStore } from "pinia";
import { dashboard } from "../api";

// Currency is read from the selected NewAPI site, never global settings.
export const usePrefsStore = defineStore("prefs", {
  state: () => ({ quotaPerUnit: Number.NaN, priceMultiplier: Number.NaN, currencySymbol: "", currencySite: "", currencyError: "", ttftP50Threshold: 3, ttftP90Threshold: 30, ttftP95Threshold: 60, loaded: false }),
  actions: {
    async loadCurrency(site: string) {
      this.currencySite = site;
      this.quotaPerUnit = Number.NaN;
      this.priceMultiplier = Number.NaN;
      this.currencySymbol = "";
      this.currencyError = "";
      if (!site) return;
      try {
        const result = await dashboard.siteCurrency(site);
        if (this.currencySite !== site) return;
        if (!Number.isFinite(result.quota_per_unit) || result.quota_per_unit <= 0) throw new Error("invalid currency");
        this.quotaPerUnit = result.quota_per_unit;
        this.priceMultiplier = result.price_multiplier;
        this.currencySymbol = result.symbol;
      } catch {
        if (this.currencySite === site) this.currencyError = "未能读取当前站点的金额显示配置";
      }
    },
    async load(force = false) {
      if (this.loaded && !force) return;
      this.loaded = true;
      try {
        const response = await dashboard.settings();
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
