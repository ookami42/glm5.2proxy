import { Port } from '../../wailsjs/go/main/Desktop'

let cachedPort: number | null = null

async function getBaseURL(): Promise<string> {
  if (cachedPort == null) {
    try {
      cachedPort = await Port()
    } catch {
      cachedPort = 3005
    }
  }
  return `http://localhost:${cachedPort}`
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const base = await getBaseURL()
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
