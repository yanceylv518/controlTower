export const alertCategories = [
  { key: "balance", label: "余额告警", rules: ["user_low_balance"] },
  { key: "system", label: "系统告警", rules: ["instance_offline", "high_cpu", "high_memory", "high_disk", "health_down", "docker_stopped", "agent_backlog"] },
  { key: "request", label: "请求告警", rules: ["high_error_rate", "high_p95_latency", "recent_errors"] },
];
export const categoriesForRules = (rules: string[]) =>
  alertCategories.filter(category => category.rules.some(rule => rules.includes(rule))).map(category => category.key);
export const rulesForCategories = (keys: string[]) =>
  alertCategories.filter(category => keys.includes(category.key)).flatMap(category => category.rules);
export function categorySummary(rules: string[]) {
  if (!rules.length) return "全部类别";
  return alertCategories.filter(category => category.rules.some(rule => rules.includes(rule)))
    .map(category => category.label + (category.rules.every(rule => rules.includes(rule)) ? "" : "（部分规则）")).join("、");
}
