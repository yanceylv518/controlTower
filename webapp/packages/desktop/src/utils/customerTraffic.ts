import type { MetricItem } from "@ct/shared";

export type TrafficDimension = "model" | "channel" | "user";
export type TrafficMetric = "tpm" | "rpm";
export interface TrafficSeries {
  key: string;
  name: string;
  color: string;
  tokens: number;
  requests: number;
  data: Array<[number, number | null]>;
}

// Monitoring shows the latest closed interval immediately. Later Agent batches
// may revise it; operational capacity guards use their own settling rules.
const compareKey = (a: string, b: string) => a < b ? -1 : a > b ? 1 : 0;
const trafficFormatter = new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 1 });

export function formatTrafficTPM(value: number) {
  if (value >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(1)}B`;
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`;
  return trafficFormatter.format(value);
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

// 客户缺行仅在站点与客户计数完全对齐时补零，RPM 还须核对独立消费计数。
export function verifiedCustomerBuckets(instanceID: string, instances: MetricItem[], users: MetricItem[], metric: TrafficMetric = "tpm"): number[] {
  const sums = new Map<number, { tokens: number; requests: number; consumes: number | null }>();
  for (const point of users) {
    if (point.instance_id !== instanceID || point.dimension_type !== "instance_user" || !point.dimension_key.startsWith(`${instanceID}:user:`)) continue;
    const time = Date.parse(point.bucket_time), sum = sums.get(time) || { tokens: 0, requests: 0, consumes: 0 };
    sum.tokens += point.tpm; sum.requests += point.request_count;
    sum.consumes = sum.consumes === null || point.consume_count == null ? null : sum.consumes + point.consume_count;
    sums.set(time, sum);
  }
  return instances.filter(point => {
    if (point.instance_id !== instanceID || point.dimension_type !== "instance" || point.dimension_key !== instanceID) return false;
    const sum = sums.get(Date.parse(point.bucket_time));
    return sum !== undefined && point.tpm === sum.tokens && point.request_count === sum.requests && sum.requests > 0
      && (metric !== "rpm" || point.consume_count != null && point.consume_count === sum.consumes);
  }).map(point => Date.parse(point.bucket_time));
}

export function latestCustomerMinute(points: MetricItem[], now: number, verifiedBuckets: number[] = [], metric: TrafficMetric = "tpm") {
  const cutoff = now - 60_000;
  const point = points.reduce<MetricItem | undefined>((last, row) => {
    const time = Date.parse(row.bucket_time);
    return time <= cutoff && (!last || time > Date.parse(last.bucket_time)) ? row : last;
  }, undefined);
  const verifiedTime = verifiedBuckets.reduce((last, time) => time <= cutoff ? Math.max(last, time) : last, -Infinity);
  if (Number.isFinite(verifiedTime) && (!point || verifiedTime > Date.parse(point.bucket_time))) {
    return { tpm: 0, ...(metric === "rpm" ? { rpm: 0 } : {}), time: verifiedTime, stale: now - verifiedTime - 60_000 > 2 * 60_000 };
  }
  return point ? { tpm: point.tpm, ...(metric === "rpm" ? { rpm: point.consume_count ?? null } : {}), time: Date.parse(point.bucket_time), stale: now - Date.parse(point.bucket_time) - 60_000 > 2 * 60_000 } : null;
}

interface TrafficOptions {
  instanceID: string;
  points: MetricItem[];
  totals: MetricItem[];
  bucketMinutes: number;
  hours: number;
  now: number;
  metric?: TrafficMetric;
}

export function buildCustomerTraffic(options: TrafficOptions & {
  customerKey: string;
  dimension: TrafficDimension;
  customerParent?: "channel" | "model";
  verifiedBuckets?: number[];
}) {
  const parentType = options.dimension === "user" ? `instance_${options.customerParent || "channel"}` : "instance_user";
  return buildTraffic({ ...options, rootKey: options.customerKey, strictChannelIDs: options.dimension === "user",
    totalDimensionType: parentType, crossDimensionType: `${parentType}_${options.dimension}` });
}

export function buildModelChannelTraffic(options: TrafficOptions & { modelKey: string }) {
  return buildModelTraffic({ ...options, dimension: "channel" });
}

export function buildModelTraffic(options: TrafficOptions & { modelKey: string; dimension: "user" | "channel" }) {
  return buildTraffic({ ...options, rootKey: options.modelKey,
    totalDimensionType: "instance_model", crossDimensionType: `instance_model_${options.dimension}`, strictChannelIDs: true });
}

function buildTraffic(options: TrafficOptions & {
  rootKey: string;
  dimension: TrafficDimension | "user";
  totalDimensionType: string;
  crossDimensionType: string;
  verifiedBuckets?: number[];
  strictChannelIDs?: boolean;
}) {
  const { rootKey, instanceID, dimension, points, totals, bucketMinutes, hours, now } = options;
  const metric = options.metric ?? "tpm";
  const field = metric === "rpm" ? "consume_count" : "tpm";
  const counter = metric === "rpm" ? "requests" : "tokens";
  const bucketMs = bucketMinutes * 60_000;
  const start = Math.ceil((now - hours * 3_600_000) / bucketMs) * bucketMs;
  const end = Math.floor(now / bucketMs) * bucketMs;
  const prefix = `${rootKey}:${dimension}:`;
  const verified = new Set(options.verifiedBuckets || []);
  const totalByTime = new Map<number, number | null>();
  for (const point of totals) {
    if (point.instance_id === instanceID && point.dimension_type === options.totalDimensionType && point.dimension_key === rootKey) {
      totalByTime.set(Date.parse(point.bucket_time), point[field] ?? null);
    }
  }
  const catalog = new Map<string, { name: string; values: Map<number, { tokens: number; requests: number | null }> }>();
  const sumByTime = new Map<number, number | null>();
  for (const point of points) {
    const time = Date.parse(point.bucket_time);
    if (point.instance_id !== instanceID || point.dimension_type !== options.crossDimensionType || !point.dimension_key.startsWith(prefix) || time < start || time >= end) continue;
    const id = point.dimension_key.slice(prefix.length);
    if (!id) continue;
    // A model name can itself contain ':channel:'. Require a complete channel ID
    // after the exact model prefix so neighboring models cannot enter this stack.
    if (options.strictChannelIDs && !/^[1-9]\d*$/.test(id)) continue;
    let series = catalog.get(id);
    if (!series) {
      const label = point.display_name && point.display_name !== point.dimension_key ? point.display_name : `${dimension === "user" ? "客户" : "渠道"} ${id}`;
      series = { name: dimension === "model" ? id : `${label} · #${id}`, values: new Map() };
      catalog.set(id, series);
    }
    const counts = series.values.get(time) || { tokens: 0, requests: 0 };
    counts.tokens += point.tpm;
    counts.requests = counts.requests === null || point.consume_count == null ? null : counts.requests + point.consume_count;
    series.values.set(time, counts);
    const sum = sumByTime.get(time), value = point[field];
    sumByTime.set(time, sum === null || value == null ? null : (sum ?? 0) + value);
  }
  const times: number[] = [], complete = new Set<number>();
  let lastCompleteTime: number | null = null;
  let incompleteBuckets = 0;
  for (let time = start; time < end; time += bucketMs) {
    times.push(time);
    const total = totalByTime.has(time) ? totalByTime.get(time) : verified.has(time) ? 0 : undefined;
    const sum = sumByTime.has(time) ? sumByTime.get(time) : 0;
    if (total != null && sum != null && total === sum) { complete.add(time); lastCompleteTime = time; }
    else if ((total ?? 0) > 0 || (sum ?? 0) > 0 || metric === "rpm" && (totalByTime.has(time) || sumByTime.has(time))) incompleteBuckets++;
  }
  const series: TrafficSeries[] = [...catalog.entries()]
    .sort(([a], [b]) => dimension !== "model"
      ? (options.strictChannelIDs ? a.length - b.length || compareKey(a, b) : Number(a) - Number(b) || compareKey(a, b))
      : compareKey(a, b))
    .map(([key, item]) => ({
      key, name: item.name,
      color: trafficColor(dimension === "model" ? `model:${key}` : `${instanceID}:${dimension}:${key}`),
      tokens: times.reduce((sum, time) => sum + (complete.has(time) ? item.values.get(time)?.tokens || 0 : 0), 0),
      requests: times.reduce((sum, time) => sum + (complete.has(time) ? item.values.get(time)?.requests || 0 : 0), 0),
      data: times.map(time => [time, complete.has(time) ? (item.values.get(time)?.[counter] || 0) / bucketMinutes : null]),
    }));
  const ranked = [...series].sort((a, b) => b[counter] - a[counter] || compareKey(a.key, b.key));
  return { metric, series, ranked, incompleteBuckets, completeTimes: complete,
    lastCompleteTime,
    coveredMinutes: complete.size * bucketMinutes, totalTokens: series.reduce((sum, item) => sum + item.tokens, 0),
    totalRequests: series.reduce((sum, item) => sum + item.requests, 0) };
}

export function escapeChartText(value: string) {
  return value.replace(/[&<>"']/g, char => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[char]!));
}
