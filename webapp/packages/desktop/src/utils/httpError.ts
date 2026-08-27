import { ApiError } from "@ct/shared";

function billingErrorText(code: string, progress?: unknown): string | undefined {
  if (code === "billing_generating") {
    const value = Number(progress);
    return `账单生成中（${Number.isFinite(value) ? Math.max(0, Math.min(100, Math.round(value))) : 0}%），完成后重试`;
  }
  if (code === "billing_not_generated") return "账单尚未生成，请先生成账单";
  if (code === "billing_channel_daily_file_not_found" || code === "billing_channel_daily_file_missing") return "该日渠道明细尚未生成，请到账单任务重新生成当天整站账单";
  return undefined;
}

export function billingReadErrorMessage(error: unknown, fallback = "数据加载失败，请稍后重试"): string {
  if (error instanceof ApiError) return billingErrorText(error.code, error.details.progress) || fallback;
  if (error instanceof Error) return billingErrorText(error.message) || error.message;
  return fallback;
}

export function billingTaskErrorMessage(error: unknown, fallback = "创建后台任务失败"): string {
  if (error instanceof ApiError && error.code === "billing_statement_duplicate") return "相同站点、账单类型、对象和账期的任务或账单已经存在，不能重复创建";
  if (error instanceof ApiError && error.code === "billing_statement_queue_full") return "等待队列已满，最多允许 5 个任务排队";
  if (error instanceof ApiError && error.code === "upstream_channels_missing") return "所选上游没有关联渠道，无法生成账单";
  if (error instanceof ApiError && error.code === "billing_job_busy") {
    const active = error.details.active_job as { completed_steps?: number; total_steps?: number } | undefined;
    const done = Number(active?.completed_steps || 0), total = Number(active?.total_steps || 0);
    const progress = total > 0 ? Math.round(done * 100 / total) : 0;
    return `当前已有后台任务正在执行（${progress}%），请等待当前任务结束后再创建新任务`;
  }
  if (error instanceof ApiError && error.code === "billing_range_already_covered") return "所选时间段已包含在已生成账单中，请直接点击查看账单，无需重新生成";
  if (error instanceof ApiError) return error.code || fallback;
  if (error instanceof Error) return error.message || fallback;
  return fallback;
}

export async function httpError(response: Response, fallback: string): Promise<Error> {
  let detail = "";
  let progress: unknown;
  try {
    const body = await response.clone().json() as { error?: string; message?: string; progress?: unknown };
    detail = body.error || body.message || "";
    progress = body.progress;
  } catch {
    try { detail = (await response.text()).trim(); } catch { /* ignore unreadable bodies */ }
  }
  if (detail === "unauthorized") detail = "登录状态已失效，请刷新页面后重新登录";
  detail = billingErrorText(detail, progress) || detail;
  return new Error(detail ? `${fallback}：${detail}` : `${fallback}（HTTP ${response.status}）`);
}

export async function downloadBillingFile(url: string, fallback: string, filename?: string): Promise<void> {
  const response = await fetch(url, { credentials: "same-origin" });
  if (!response.ok) throw await httpError(response, fallback);
  const blob = await response.blob();
  const href = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = href;
  if (filename) link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  // Revoking synchronously can cancel a large download before the browser has
  // taken ownership of the blob URL. Keep it alive briefly; the timeout only
  // releases the in-memory URL and does not affect the downloaded file.
  window.setTimeout(() => URL.revokeObjectURL(href), 60_000);
}

// Large generated CSV exports should be streamed by the browser instead of
// being fully buffered into a JavaScript Blob. The server supplies the final
// Content-Disposition filename; `filename` is a fallback for older browsers.
export function startBillingFileDownload(url: string, filename?: string): void {
  const link = document.createElement("a");
  link.href = url;
  if (filename) link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
}
