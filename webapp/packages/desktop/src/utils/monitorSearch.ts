import type { MetricItem } from "@ct/shared";

export type MonitorSearchOption = {
  key: string;
  value: string;
  detail: string;
  keywords: string;
};

export function monitorSearchOptions(items: MetricItem[], kind: "customers" | "channels" | "models"): MonitorSearchOption[] {
  const options = new Map<string, MonitorSearchOption>();
  for (const item of items) {
    const id = item.dimension_key.split(":").pop() || item.dimension_key;
    const value = item.display_name || item.display_key || (kind === "customers" ? `客户 ${id}` : id);
    const instance = item.instance_name || item.instance_id;
    options.set(item.dimension_key, {
      key: item.dimension_key,
      value,
      detail: kind === "models" ? instance : `ID ${id} · ${instance}`,
      keywords: `${value} ${item.display_key || ""} ${item.dimension_key} ${instance}`.toLowerCase(),
    });
  }
  return [...options.values()];
}

export function filterMonitorOptions(options: MonitorSearchOption[], query: string, selectedKey = ""): MonitorSearchOption[] {
  if (selectedKey) return options.filter(option => option.key === selectedKey);
  const words = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
  return options.filter(option => words.every(word => option.keywords.includes(word)));
}
