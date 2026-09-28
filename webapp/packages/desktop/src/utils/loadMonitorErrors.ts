import type { ErrorCodeResult } from './monitorErrorTrend';

// Keep source queries sequential and bounded. Only publish a complete window.
export async function loadMonitorErrors(
  query: URLSearchParams,
  signal: AbortSignal,
  request: (url: string, options: { signal: AbortSignal }) => Promise<ErrorCodeResult>,
): Promise<ErrorCodeResult> {
  const start = Date.parse(query.get('start_time')!);
  const end = Date.parse(query.get('end_time')!);
  const step = query.get('bucket') === '5m' ? 300_000 : 60_000;
  const deadline = Date.now() + 60_000;
  let requests = 0;
  async function fetchRange(from: number, until: number): Promise<ErrorCodeResult> {
    signal.throwIfAborted();
    if (++requests > 63 || Date.now() >= deadline) throw { code: 'error_statistics_limit' };
    const params = new URLSearchParams(query);
    params.set('start_time', new Date(from).toISOString());
    params.set('end_time', new Date(until).toISOString());
    try {
      const result = await request(`/api/dashboard/monitor-error-codes?${params}`, { signal });
      signal.throwIfAborted();
      return result;
    } catch (error) {
      signal.throwIfAborted();
      if ((error as { code?: string }).code !== 'error_statistics_limit' || until - from <= 60_000) throw error;
      const middle = Math.floor((from + until) / 2 / 60_000) * 60_000;
      if (middle <= from || middle >= until) throw error;
      const left = await fetchRange(from, middle);
      const right = await fetchRange(middle, until);
      if (!left.configured || !right.configured) throw { code: 'readonly_query_failed' };
      const counts = new Map<string, number>();
      const buckets = new Map<number, Map<string, number>>();
      for (const part of [left, right]) {
        for (const item of part.items) counts.set(item.code, (counts.get(item.code) ?? 0) + item.count);
        for (const bucket of part.buckets) {
          const time = Date.parse(bucket.time);
          const target = buckets.get(time) ?? new Map<string, number>();
          for (const [code, count] of Object.entries(bucket.counts)) target.set(code, (target.get(code) ?? 0) + count);
          buckets.set(time, target);
        }
      }
      return {
        configured: true, total: left.total + right.total,
        items: [...counts].map(([code, count]) => ({ code, count })).sort((a, b) => b.count - a.count || a.code.localeCompare(b.code)),
        buckets: [...buckets].sort(([a], [b]) => a - b).map(([time, values]) => ({ time: new Date(time).toISOString(), counts: Object.fromEntries(values) })),
        bucket_seconds: step / 1000, start_time: new Date(from).toISOString(), end_time: new Date(until).toISOString(),
      };
    }
  }
  return fetchRange(start, end);
}
