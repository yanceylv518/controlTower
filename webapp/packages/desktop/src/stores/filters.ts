import { defineStore } from "pinia";
import { siteOf, type InstanceItem } from "@ct/shared";
import { dashboard } from "../api";

const DEFAULT_INSTANCE_KEY = "CT_DEFAULT_INSTANCE_ID";
const SITE_KEY = "CT_SELECTED_SITE_ID";
let instanceLoadPromise: Promise<void> | null = null;

export const useFiltersStore = defineStore("filters", {
  state: () => ({
    instance_id: "",
    site_id: "",
    instances: [] as InstanceItem[],
    loaded: false,
  }),
  actions: {
    async loadInstances(force = false) {
      if (this.loaded && !force) return;
      if (instanceLoadPromise && !force) return instanceLoadPromise;
      instanceLoadPromise = (async () => {
        const instanceResponse = await dashboard.instances();
        this.instances = instanceResponse.items;
        let configured = "";
        try {
          const settingsResponse = await dashboard.settings();
          configured = settingsResponse.items[DEFAULT_INSTANCE_KEY]?.value?.trim() || "";
        } catch {
          // Instance filtering remains usable when the settings endpoint is unavailable.
        }
        const available = this.instances.filter((item) => item.enabled);
        const selected = available.find((item) => item.instance_id === configured);
        if (!this.loaded || force) {
          this.instance_id = selected?.instance_id || available[0]?.instance_id || "";
          const sites = [...new Set(available.map(siteOf))];
          const savedSite = localStorage.getItem(SITE_KEY) || "";
          this.site_id = sites.includes(savedSite)
            ? savedSite
            : siteOf(selected || available[0] || { instance_id: "" });
        }
        this.loaded = true;
      })();
      try {
        await instanceLoadPromise;
      } finally {
        instanceLoadPromise = null;
      }
    },
    selectSite(siteID: string) {
      this.site_id = siteID;
      localStorage.setItem(SITE_KEY, siteID);
    },
  },
});
