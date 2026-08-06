export async function httpError(response: Response, fallback: string): Promise<Error> {
  let detail = "";
  try {
    const body = await response.clone().json() as { error?: string; message?: string };
    detail = body.error || body.message || "";
  } catch {
    try { detail = (await response.text()).trim(); } catch { /* ignore unreadable bodies */ }
  }
  if (detail === "unauthorized") detail = "登录状态已失效，请刷新页面后重新登录";
  return new Error(detail ? `${fallback}：${detail}` : `${fallback}（HTTP ${response.status}）`);
}
