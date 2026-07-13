import { ApiClient, authApi, dashboardApi } from '@ct/shared'
let unauthorizedHandler: (() => void) | undefined
export const setUnauthorizedHandler = (handler: () => void) => { unauthorizedHandler = handler }
export const client = new ApiClient({ onUnauthorized: () => unauthorizedHandler?.() })
export const auth = authApi(client)
export const dashboard = dashboardApi(client)
