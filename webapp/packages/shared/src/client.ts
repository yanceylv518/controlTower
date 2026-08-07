export interface ApiClientOptions {
  onUnauthorized?: () => void
}

export class ApiError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string,
    public readonly details: Record<string, unknown> = {},
  ) {
    super(code)
    this.name = 'ApiError'
  }
}

export class ApiClient {
  constructor(private readonly options: ApiClientOptions = {}) {}

  async request<T>(path: string, init: RequestInit = {}): Promise<T> {
    const method = (init.method ?? 'GET').toUpperCase()
    const headers = new Headers(init.headers)
    if (init.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
    if (method !== 'GET' && method !== 'HEAD') headers.set('X-Requested-With', 'XMLHttpRequest')

    const response = await fetch(path, { ...init, method, headers, credentials: 'same-origin' })
    if (!response.ok) {
      let code = `http_${response.status}`
      let body: Record<string, unknown> = {}
      try {
        body = (await response.json()) as Record<string, unknown>
        if (typeof body.error === 'string') code = body.error
      } catch {
        // Preserve the status-derived code when the response is not JSON.
      }
      if (response.status === 401) this.options.onUnauthorized?.()
      throw new ApiError(response.status, code, body)
    }
    if (response.status === 204) return undefined as T
    return (await response.json()) as T
  }
}
