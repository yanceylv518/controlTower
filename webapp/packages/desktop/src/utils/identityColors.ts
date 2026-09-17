// 身份标识颜色沿用 New API rc35 的稳定哈希规则，保证同一名称跨行、跨刷新保持一致。
export type UserAvatarStyle = Readonly<{
  backgroundColor: string
  color: string
}>

// 令牌颜色采用 New API 的语义色顺序，CSS 层再把语义色映射到浅色主题的前景和底色。
const TOKEN_COLOR_NAMES = [
  'amber',
  'blue',
  'cyan',
  'green',
  'grey',
  'indigo',
  'light-blue',
  'lime',
  'orange',
  'pink',
  'purple',
  'red',
  'teal',
  'violet',
  'yellow',
] as const

export type TokenColorName = (typeof TOKEN_COLOR_NAMES)[number]

function hashString(value: string): number {
  let hash = 0
  for (let index = 0; index < value.length; index += 1) {
    hash = (hash * 31 + value.charCodeAt(index)) >>> 0
  }
  return hash
}

/**
 * 生成与 New API `getUserAvatarStyle` 相同的头像样式。
 * 完整字符串参与哈希，避免只按首字母取色导致大量用户落在同一颜色。
 */
export function getUserAvatarStyle(name: string): UserAvatarStyle {
  const hash = hashString(name)
  const hue = hash % 360
  const saturation = 54 + (hash % 8)
  const lightness = 52 + ((hash >> 4) % 8)

  return {
    backgroundColor: `hsl(${hue} ${saturation}% ${lightness}%)`,
    color: 'white',
  }
}

/** 头像首字母与 New API 保持一致，英文名称统一使用大写首字母。 */
export function getUserAvatarFallback(name: string): string {
  return name.trim().charAt(0).toUpperCase() || '?'
}

/**
 * 按 New API `stringToColor` 的字符码总和选择令牌语义色。
 * 这是展示标识，不用于权限或业务判断；名称为空时回退到第一种颜色。
 */
export function getTokenColorName(name: string): TokenColorName {
  let sum = 0
  for (let index = 0; index < name.length; index += 1) {
    sum += name.charCodeAt(index)
  }
  return TOKEN_COLOR_NAMES[sum % TOKEN_COLOR_NAMES.length]
}

/** 返回与令牌语义色对应的 CSS 类名，便于桌面和移动列表复用。 */
export function getTokenColorClass(name: string): string {
  return `token-tone-${getTokenColorName(name)}`
}
