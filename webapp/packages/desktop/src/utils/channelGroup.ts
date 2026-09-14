export const MAX_CHANNEL_GROUP_LENGTH = 128;
export const MAX_VISIBLE_CHANNEL_GROUPS = 3;

// 将 New API 的逗号字符串拆成去空格、去重后的标签，便于编辑器稳定渲染。
export function splitChannelGroups(value: string | null | undefined): string[] {
  const seen = new Set<string>();
  const groups: string[] = [];
  for (const part of String(value ?? "").split(",")) {
    const group = part.trim();
    if (group && !seen.has(group)) {
      seen.add(group);
      groups.push(group);
    }
  }
  return groups;
}

// 表格需要保持紧凑，只展示前三个分组；编辑器仍使用 splitChannelGroups 获取完整值。
export function visibleChannelGroups(value: string | null | undefined): string[] {
  return splitChannelGroups(value).slice(0, MAX_VISIBLE_CHANNEL_GROUPS);
}

// 返回被折叠到“+N”中的分组数量，避免页面把长组合撑开。
export function hiddenChannelGroupCount(value: string | null | undefined): number {
  return Math.max(0, splitChannelGroups(value).length - MAX_VISIBLE_CHANNEL_GROUPS);
}

// 前端只负责改善输入体验，服务端仍会对最终字符串再次校验。
export function normalizeChannelGroups(values: readonly string[]): string {
  const groups = splitChannelGroups(values.join(","));
  if (groups.some((group) => /[\u0000-\u001f\u007f]/u.test(group))) {
    throw new Error("分组名称不能包含控制字符");
  }
  const result = groups.join(",");
  if (Array.from(result).length > MAX_CHANNEL_GROUP_LENGTH) {
    throw new Error(`分组组合不能超过 ${MAX_CHANNEL_GROUP_LENGTH} 个字符`);
  }
  return result;
}
