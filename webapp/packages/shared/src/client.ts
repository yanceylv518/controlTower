export interface ApiClientOptions {
  onUnauthorized?: () => void
}

export class ApiError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string,
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
      try {
        const body = (await response.json()) as { error?: string }
        if (body.error) code = body.error
      } catch {
        // Preserve the status-derived code when the response is not JSON.
      }
      if (response.status === 401) this.options.onUnauthorized?.()
      throw new ApiError(response.status, code)
    }
    if (response.status === 204) return undefined as T
    return (await response.json()) as T
  }
}
