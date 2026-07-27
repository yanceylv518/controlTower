import { defineStore } from "pinia";
import { siteOf, type InstanceItem } from "@ct/shared";
import { dashboard } from "../api";

const SITE_KEY = "CT_SELECTED_SITE_ID";
let instanceLoadPromise: Promise<void> | null = null;

export const useFiltersStore = defineStore("filters", {
  state: () => ({
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
        const available = this.instances.filter((item) => item.enabled);
        if (!this.loaded || force) {
          const sites = [...new Set(available.map(siteOf))];
          const savedSite = localStorage.getItem(SITE_KEY) || "";
          this.site_id = sites.includes(savedSite)
            ? savedSite
            : siteOf(available[0] || { instance_id: "" });
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
