import { defineStore } from "pinia";
import type { InstanceItem } from "@ct/shared";
import { dashboard } from "../api";

const DEFAULT_INSTANCE_KEY = "CT_DEFAULT_INSTANCE_ID";

export const useFiltersStore = defineStore("filters", {
  state: () => ({
    instance_id: "",
    instances: [] as InstanceItem[],
    loaded: false,
  }),
  actions: {
    async loadInstances(force = false) {
      if (this.loaded && !force) return;
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
      }
      this.loaded = true;
    },
  },
});
