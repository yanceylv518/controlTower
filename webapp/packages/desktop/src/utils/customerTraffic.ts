import type { MetricItem } from "@ct/shared";

export type TrafficDimension = "model" | "channel" | "user";
export interface TrafficSeries {
  key: string;
  name: string;
  color: string;
  tokens: number;
  data: Array<[number, number | null]>;
}

// Monitoring shows the latest closed interval immediately. Later Agent batches
// may revise it; operational capacity guards use their own settling rules.
const compareKey = (a: string, b: string) => a < b ? -1 : a > b ? 1 : 0;

export function formatTrafficTPM(value: number) {
  if (value >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(1)}B`;
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`;
  return new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 1 }).format(value);
}

export function trafficColor(identity: string): string {
  let hash = 2166136261;
  for (const char of identity) hash = Math.imul(hash ^ char.charCodeAt(0), 16777619);
  // Restrict hues to the preview's blue/teal/violet/rose/amber family.
  // Identity hashing keeps colors independent of rankings and page membership.
  const hues = [220, 185, 263, 332, 30, 155];
  const hue = hues[(hash >>> 0) % hues.length] + ((hash >>> 8) % 19) - 9;
  const saturation = .48, lightness = .60;
  const a = saturation * Math.min(lightness, 1 - lightness);
  const channel = (n: number) => {
    const k = (n + hue / 30) % 12;
    return Math.round(255 * (lightness - a * Math.max(-1, Math.min(k - 3, 9 - k, 1)))).toString(16).padStart(2, "0");
  };
  return `#${channel(0)}${channel(8)}${channel(4)}`;
}

export function trafficRGBA(color: string, alpha: number) {
  const parts = [1, 3, 5].map(offset => parseInt(color.slice(offset, offset + 2), 16));
  return `rgba(${parts.join(",")},${alpha})`;
}

// Missing user rows mean zero only when all user counters reconcile with the
// instance bucket. Both request count and tokens must match; partial responses,
// unknown-user requests and restricted views cannot certify coverage.
export function verifiedCustomerBuckets(instanceID: string, instances: MetricItem[], users: MetricItem[]): number[] {
  const sums = new Map<number, { tokens: number; requests: number }>();
  for (const point of users) {
    if (point.instance_id !== instanceID || point.dimension_type !== "instance_user" || !point.dimension_key.startsWith(`${instanceID}:user:`)) continue;
    const time = Date.parse(point.bucket_time), sum = sums.get(time) || { tokens: 0, requests: 0 };
    sum.tokens += point.tpm; sum.requests += point.request_count;
    sums.set(time, sum);
  }
  return instances.filter(point => {
    if (point.instance_id !== instanceID || point.dimension_type !== "instance" || point.dimension_key !== instanceID) return false;
    const sum = sums.get(Date.parse(point.bucket_time));
    return sum !== undefined && point.tpm === sum.tokens && point.request_count === sum.requests && sum.requests > 0;
  }).map(point => Date.parse(point.bucket_time));
}

export function latestCustomerMinute(points: MetricItem[], now: number, verifiedBuckets: number[] = []) {
  const cutoff = now - 60_000;
  const point = points.reduce<MetricItem | undefined>((last, row) => {
    const time = Date.parse(row.bucket_time);
    return time <= cutoff && (!last || time > Date.parse(last.bucket_time)) ? row : last;
  }, undefined);
  const verifiedTime = verifiedBuckets.reduce((last, time) => time <= cutoff ? Math.max(last, time) : last, -Infinity);
  if (Number.isFinite(verifiedTime) && (!point || verifiedTime > Date.parse(point.bucket_time))) {
    return { tpm: 0, time: verifiedTime, stale: now - verifiedTime - 60_000 > 2 * 60_000 };
  }
  return point ? { tpm: point.tpm, time: Date.parse(point.bucket_time), stale: now - Date.parse(point.bucket_time) - 60_000 > 2 * 60_000 } : null;
}

export function buildCustomerTraffic(options: {
  customerKey: string;
  instanceID: string;
  dimension: TrafficDimension;
  customerParent?: "channel" | "model";
  points: MetricItem[];
  totals: MetricItem[];
  bucketMinutes: number;
  hours: number;
  now: number;
  verifiedBuckets?: number[];
}) {
  const { customerKey, instanceID, dimension, points, totals, bucketMinutes, hours, now } = options;
  const parentType = dimension === "user" ? `instance_${options.customerParent || "channel"}` : "instance_user";
  const bucketMs = bucketMinutes * 60_000;
  const start = Math.ceil((now - hours * 3_600_000) / bucketMs) * bucketMs;
  const end = Math.floor(now / bucketMs) * bucketMs;
  const prefix = `${customerKey}:${dimension}:`;
  const verified = new Set(options.verifiedBuckets || []);
  const totalByTime = new Map<number, number>();
  for (const point of totals) {
    if (point.instance_id === instanceID && point.dimension_type === parentType && point.dimension_key === customerKey) {
      totalByTime.set(Date.parse(point.bucket_time), point.tpm);
    }
  }
  const catalog = new Map<string, { name: string; values: Map<number, number> }>();
  const sumByTime = new Map<number, number>();
  for (const point of points) {
    const time = Date.parse(point.bucket_time);
    if (point.instance_id !== instanceID || point.dimension_type !== `${parentType}_${dimension}` || !point.dimension_key.startsWith(prefix) || time < start || time >= end) continue;
    const id = point.dimension_key.slice(prefix.length);
    if (!id || (dimension === "user" && !/^[1-9]\d*$/.test(id))) continue;
    let series = catalog.get(id);
    if (!series) {
      const label = point.display_name && point.display_name !== point.dimension_key ? point.display_name : `${dimension === "user" ? "客户" : "渠道"} ${id}`;
      series = { name: dimension === "model" ? id : `${label} · #${id}`, values: new Map() };
      catalog.set(id, series);
    }
    series.values.set(time, (series.values.get(time) || 0) + point.tpm);
    sumByTime.set(time, (sumByTime.get(time) || 0) + point.tpm);
  }
  const times: number[] = [], complete = new Set<number>();
  let lastCompleteTime: number | null = null;
  let incompleteBuckets = 0;
  for (let time = start; time < end; time += bucketMs) {
    times.push(time);
    const total = totalByTime.get(time) ?? (verified.has(time) ? 0 : undefined), sum = sumByTime.get(time) || 0;
    if (total !== undefined && total === sum) { complete.add(time); lastCompleteTime = time; }
    else if ((total || 0) > 0 || sum > 0) incompleteBuckets++;
  }
  const series: TrafficSeries[] = [...catalog.entries()]
    .sort(([a], [b]) => dimension === "channel" ? Number(a) - Number(b) || compareKey(a, b) : compareKey(a, b))
    .map(([key, item]) => ({
      key, name: item.name,
      color: trafficColor(dimension === "model" ? `model:${key}` : `${instanceID}:${dimension}:${key}`),
      tokens: times.reduce((sum, time) => sum + (complete.has(time) ? item.values.get(time) || 0 : 0), 0),
      data: times.map(time => [time, complete.has(time) ? (item.values.get(time) || 0) / bucketMinutes : null]),
    }));
  const ranked = [...series].sort((a, b) => b.tokens - a.tokens || compareKey(a.key, b.key));
  return { series, ranked, incompleteBuckets, completeTimes: complete,
    lastCompleteTime,
    coveredMinutes: complete.size * bucketMinutes, totalTokens: series.reduce((sum, item) => sum + item.tokens, 0) };
}

export function escapeChartText(value: string) {
  return value.replace(/[&<>"']/g, char => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[char]!));
}
