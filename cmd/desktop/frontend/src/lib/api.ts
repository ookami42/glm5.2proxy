import { Port } from '../../wailsjs/go/main/Desktop'

let cachedPort: number | null = null

async function getPort(): Promise<number> {
  if (cachedPort == null) {
    try {
      cachedPort = await Port()
    } catch {
      cachedPort = 3005
    }
  }
  return cachedPort
}

async function getBaseURLs(): Promise<string[]> {
  const port = await getPort()
  return [`http://127.0.0.1:${port}`, `http://localhost:${port}`]
}

function isNetworkError(err: unknown): boolean {
  return err instanceof TypeError || (err instanceof Error && /failed to fetch|network/i.test(err.message))
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const bases = await getBaseURLs()
  let lastNetworkError: unknown = null
  for (const base of bases) {
    try {
      const res = await fetch(`${base}${path}`, {
        ...init,
        headers: {
          'Content-Type': 'application/json',
          ...init?.headers,
        },
      })
      if (!res.ok) {
        const body = await res.json().catch(() => ({}))
        const message = body?.error?.message ?? `HTTP ${res.status}`
        throw new Error(message)
      }
      return res.json() as Promise<T>
    } catch (err) {
      if (!isNetworkError(err) || init?.signal?.aborted) {
        throw err
      }
      lastNetworkError = err
    }
  }
  throw lastNetworkError instanceof Error ? lastNetworkError : new Error('Failed to fetch')
}

export const api = {
  get: <T>(path: string, init?: RequestInit) => request<T>(path, init),

  post: <T>(path: string, body?: unknown) =>
    request<T>(path, {
      method: 'POST',
      body: body != null ? JSON.stringify(body) : undefined,
    }),

  patch: <T>(path: string, body?: unknown) =>
    request<T>(path, {
      method: 'PATCH',
      body: body != null ? JSON.stringify(body) : undefined,
    }),

  put: <T>(path: string, body?: unknown) =>
    request<T>(path, {
      method: 'PUT',
      body: body != null ? JSON.stringify(body) : undefined,
    }),

  delete: <T>(path: string) => request<T>(path, { method: 'DELETE' }),
}
