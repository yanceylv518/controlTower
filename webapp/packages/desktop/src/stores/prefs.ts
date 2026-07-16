import { defineStore } from "pinia";
import { dashboard } from "../api";

// Display preferences sourced from system settings; safe defaults apply
// before the settings request resolves or when it fails.
export const usePrefsStore = defineStore("prefs", {
  state: () => ({ quotaPerUnit: 500000, currencySymbol: "¥", loaded: false }),
  actions: {
    async load() {
      if (this.loaded) return;
      this.loaded = true;
      try {
        const response = await dashboard.settings();
        const per = Number(response.items.CT_QUOTA_PER_UNIT?.value);
        if (Number.isFinite(per) && per > 0) this.quotaPerUnit = per;
        const symbol = response.items.CT_CURRENCY_SYMBOL?.value?.trim();
        if (symbol) this.currencySymbol = symbol;
      } catch {
        this.loaded = false;
      }
    },
  },
});
