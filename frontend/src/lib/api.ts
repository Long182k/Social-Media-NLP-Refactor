import { useUIStore } from '@/stores/uiStore'
import type { ApiErrorPayload, AuthResponse, PublicUser } from '@/types/api'

export class ApiError extends Error {
  readonly status: number
  readonly code: string

  constructor(status: number, code: string, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }
}

type AuthBroadcastMessage =
  | { type: 'SESSION_REFRESHED'; accessToken: string; user: PublicUser; ts: number }
  | { type: 'SESSION_CLEARED'; ts: number }

const AUTH_CHANNEL_NAME = 'smn_auth'
const AUTH_LOCK_NAME = 'smn_auth_refresh'

let refreshInFlight: Promise<string | null> | null = null
let lastBroadcastRefreshAt = 0
let lastBroadcastClearedAt = 0

const authChannel: BroadcastChannel | null =
  typeof BroadcastChannel !== 'undefined' ? new BroadcastChannel(AUTH_CHANNEL_NAME) : null

if (authChannel) {
  authChannel.onmessage = (event: MessageEvent<AuthBroadcastMessage>) => {
    const msg = event.data
    if (!msg || typeof msg !== 'object') return
    if (msg.type === 'SESSION_REFRESHED' && msg.accessToken && msg.user) {
      lastBroadcastRefreshAt = Math.max(lastBroadcastRefreshAt, msg.ts || Date.now())
      useUIStore.getState().setSession(msg.accessToken, msg.user)
    } else if (msg.type === 'SESSION_CLEARED') {
      lastBroadcastClearedAt = Math.max(lastBroadcastClearedAt, msg.ts || Date.now())
      useUIStore.getState().clearSession()
    }
  }
}

export function broadcastSessionCleared(): void {
  const ts = Date.now()
  lastBroadcastClearedAt = Math.max(lastBroadcastClearedAt, ts)
  authChannel?.postMessage({ type: 'SESSION_CLEARED', ts } satisfies AuthBroadcastMessage)
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

async function runWithCrossTabLock<T>(fn: () => Promise<T>): Promise<T> {
  if (typeof navigator !== 'undefined' && navigator.locks?.request) {
    return navigator.locks.request(AUTH_LOCK_NAME, fn)
  }
  return fn()
}

async function doRefreshRequest(): Promise<Response> {
  return fetch('/api/auth/refresh', {
    method: 'POST',
    credentials: 'include',
  })
}

export async function refreshAccessToken(): Promise<string | null> {
  if (refreshInFlight) {
    return refreshInFlight
  }

  const startedAt = Date.now()

  refreshInFlight = (async () => {
    try {
      return await runWithCrossTabLock(async () => {
        // Yield one macrotask so any BroadcastChannel message posted by another tab
        // right before releasing the lock is processed in this tab's event loop.
        await sleep(0)

        const currentToken = useUIStore.getState().accessToken
        if (lastBroadcastRefreshAt >= startedAt && currentToken) {
          return currentToken
        }
        if (lastBroadcastClearedAt >= startedAt && lastBroadcastClearedAt > lastBroadcastRefreshAt) {
          return null
        }

        let data: AuthResponse | null = null
        let lastErr: unknown = null
        let terminalAuthFailure = false

        for (let attempt = 0; attempt < 2; attempt += 1) {
          try {
            const res = await doRefreshRequest()
            if (res.status === 401 || res.status === 403) {
              terminalAuthFailure = true
              break
            }
            if (res.ok) {
              data = (await res.json()) as AuthResponse
              break
            }
          } catch (err) {
            lastErr = err
          }
          if (attempt === 0) {
            await sleep(300)
          }
        }

        if (terminalAuthFailure) {
          useUIStore.getState().clearSession()
          broadcastSessionCleared()
          return null
        }

        if (!data) {
          // Transient network/5xx/stream failure after retry: preserve session/cookie without broadcasting clear.
          void lastErr
          if (!useUIStore.getState().accessToken) {
            useUIStore.getState().setSessionRestoreError(true)
          }
          return useUIStore.getState().accessToken
        }

        const ts = Date.now()
        lastBroadcastRefreshAt = Math.max(lastBroadcastRefreshAt, ts)
        useUIStore.getState().setSession(data.accessToken, data.user)
        authChannel?.postMessage({
          type: 'SESSION_REFRESHED',
          accessToken: data.accessToken,
          user: data.user,
          ts,
        } satisfies AuthBroadcastMessage)
        return data.accessToken
      })
    } finally {
      refreshInFlight = null
    }
  })()

  return refreshInFlight
}

async function parseErrorResponse(res: Response): Promise<ApiError> {
  try {
    const body = (await res.json()) as ApiErrorPayload
    return new ApiError(
      res.status,
      body.error ?? 'API_ERROR',
      body.message ?? (res.statusText || 'Request failed'),
    )
  } catch {
    return new ApiError(res.status, 'API_ERROR', res.statusText || 'Request failed')
  }
}

export interface ApiFetchOptions extends RequestInit {
  skipAuthRetry?: boolean
}

export async function authorizedFetch(path: string, init: ApiFetchOptions = {}): Promise<Response> {
  const { skipAuthRetry = false, headers: initHeaders, ...rest } = init
  const token = useUIStore.getState().accessToken

  const headers = new Headers(initHeaders)
  if (typeof rest.body === 'string' && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }
  if (token && !headers.has('Authorization')) {
    headers.set('Authorization', `Bearer ${token}`)
  }

  const res = await fetch(path, {
    ...rest,
    headers,
    credentials: 'include',
  })

  if (res.status === 401 && !skipAuthRetry && !path.startsWith('/api/auth/')) {
    const nextToken = await refreshAccessToken()
    if (nextToken) {
      const retryHeaders = new Headers(initHeaders)
      if (typeof rest.body === 'string' && !retryHeaders.has('Content-Type')) {
        retryHeaders.set('Content-Type', 'application/json')
      }
      retryHeaders.set('Authorization', `Bearer ${nextToken}`)

      const retryRes = await fetch(path, {
        ...rest,
        headers: retryHeaders,
        credentials: 'include',
      })
      return retryRes
    }
    throw new ApiError(401, 'UNAUTHORIZED', 'Session expired. Please sign in again.')
  }

  return res
}

export async function apiFetch<T>(path: string, init: ApiFetchOptions = {}): Promise<T> {
  const res = await authorizedFetch(path, init)
  if (!res.ok) {
    throw await parseErrorResponse(res)
  }

  return res.status === 204 ? undefined as T : (await res.json()) as T
}
