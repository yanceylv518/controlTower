// 身份标识颜色沿用 New API rc35 的稳定哈希规则，保证同一名称跨行、跨刷新保持一致。
export type UserAvatarStyle = Readonly<{
  backgroundColor: string
  color: string
}>

// 令牌颜色采用 New API 的语义色顺序，CSS 层映射当前主题的前景和底色。
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

  // Keep the identity hue stable, but don't put white initials on a pale yellow/green.
  const l = lightness / 100, s = saturation / 100
  const a = s * Math.min(l, 1 - l)
  const rgb = [0, 8, 4].map(n => {
    const k = (n + hue / 30) % 12
    const value = l - a * Math.max(-1, Math.min(k - 3, 9 - k, 1))
    return value <= .04045 ? value / 12.92 : ((value + .055) / 1.055) ** 2.4
  })
  const luminance = .2126 * rgb[0] + .7152 * rgb[1] + .0722 * rgb[2]

  return {
    backgroundColor: `hsl(${hue} ${saturation}% ${lightness}%)`,
    color: 1.05 / (luminance + .05) >= 4.6 ? '#ffffff' : '#000000',
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
