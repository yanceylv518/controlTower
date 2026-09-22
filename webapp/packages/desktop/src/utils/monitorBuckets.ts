// bucket_time is the interval start. Compare only elapsed intervals, using
// the request start time so a response crossing a boundary cannot close a
// bucket that was still being collected when it was requested.
export function closedMonitorBuckets<T extends { bucket_time: string }>(points: T[], bucketMinutes: number, requestedAt: number): T[] {
  return points.filter(point => Date.parse(point.bucket_time) + bucketMinutes * 60_000 <= requestedAt);
}
