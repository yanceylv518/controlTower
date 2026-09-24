export type LogUserSuggestion = {
  id: number
  username: string
  display_name: string
}

const normalizedTerm = (value: string) => value.trim().toLowerCase()

export function filterUserSuggestions(items: LogUserSuggestion[], keyword: string, limit = 20) {
  const term = normalizedTerm(keyword)
  if (!term) return items.slice(0, limit)
  return items.filter(item => [item.display_name, item.username, String(item.id)]
    .some(value => value.toLowerCase().includes(term))).slice(0, limit)
}

export function mergeUserSuggestions(primary: LogUserSuggestion[], secondary: LogUserSuggestion[], limit = 20) {
  const users = new Map<number, LogUserSuggestion>()
  for (const item of [...primary, ...secondary]) {
    const existing = users.get(item.id)
    users.set(item.id, existing
      ? { ...existing, username: existing.username || item.username, display_name: existing.display_name || item.display_name }
      : item)
  }
  return [...users.values()].slice(0, limit)
}

export function suggestionCacheKey(...parts: string[]) {
  return JSON.stringify(parts)
}

export class SuggestionCache<T> {
  private readonly entries = new Map<string, { items: T[]; expiresAt: number }>()

  constructor(
    private readonly capacity = 40,
    private readonly ttlMs = 30_000,
    private readonly now: () => number = Date.now,
  ) {}

  get(key: string): T[] | undefined {
    const entry = this.entries.get(key)
    if (!entry) return undefined
    if (entry.expiresAt <= this.now()) {
      this.entries.delete(key)
      return undefined
    }
    this.entries.delete(key)
    this.entries.set(key, entry)
    return entry.items.slice()
  }

  set(key: string, items: T[]) {
    this.entries.delete(key)
    this.entries.set(key, { items: items.slice(), expiresAt: this.now() + this.ttlMs })
    while (this.entries.size > this.capacity) {
      const oldest = this.entries.keys().next().value
      if (oldest === undefined) break
      this.entries.delete(oldest)
    }
  }
}

export const userSuggestionCache = new SuggestionCache<LogUserSuggestion>()
