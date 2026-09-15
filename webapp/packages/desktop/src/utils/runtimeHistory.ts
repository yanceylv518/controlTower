import type { ServerMetricItem } from "@ct/shared";

interface MetricQuery {
  instance_id: string;
  start_time: string;
  end_time: string;
  limit: number;
  offset: number;
}

// The runtime API caps each page at 200 samples, regardless of the time range.
export async function loadRuntimeHistory(
  fetchPage: (query: MetricQuery) => Promise<{ items: ServerMetricItem[] }>,
  instanceIDs: string[],
  startTime: string,
  endTime: string,
): Promise<ServerMetricItem[]> {
  const items: ServerMetricItem[] = [];
  for (const instanceID of instanceIDs) {
    for (let offset = 0; ; offset += 200) {
      const page = await fetchPage({
        instance_id: instanceID,
        start_time: startTime,
        end_time: endTime,
        limit: 200,
        offset,
      });
      items.push(...page.items);
      if (page.items.length < 200) break;
    }
  }
  return items;
}
