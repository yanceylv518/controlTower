import { defineStore } from 'pinia'
import type { InstanceItem } from '@ct/shared'
import { dashboard } from '../api'
export const useFiltersStore = defineStore('filters', { state: () => ({ instance_id: '', instances: [] as InstanceItem[], loaded: false }), actions: { async loadInstances() { if (this.loaded) return; const response = await dashboard.instances(); this.instances = response.items; this.loaded = true } } })
