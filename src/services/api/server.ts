import { ApiError, request, setCSRFToken } from './client.js'

const API = '/api/v1'

export type AuthUser = {
  id: string
  username: string
  avatar: string
  groups: string[]
  disabled: boolean
  createdAt: number
  updatedAt: number
}

export type SessionInfo = {
  user: AuthUser
  csrf: string
  expiresAt: number
  hasApiKey?: boolean
}

export type ServerSettings = {
  preferences: Record<string, unknown>
  hasApiKey: boolean
  updatedAt: number
}

const readJSON = async <T>(response: Response): Promise<T> => (await response.json()) as T

/** 读取当前 Cookie 会话；未登录返回 null，其他错误照常抛出。 */
export const getMe = async (): Promise<SessionInfo | null> => {
  try {
    const response = await request(
      `${API}/auth/me`,
      { method: 'GET' },
      { handleUnauthorized: false }
    )
    const body = await readJSON<SessionInfo>(response)
    setCSRFToken(body.csrf)
    return body
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      return null
    }
    throw error
  }
}

export const login = async (username: string, password: string): Promise<SessionInfo> => {
  const response = await request(
    `${API}/auth/login`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password }),
    },
    { handleUnauthorized: false }
  )
  const body = await readJSON<SessionInfo>(response)
  setCSRFToken(body.csrf)
  return body
}

export const logout = async (): Promise<void> => {
  await request(`${API}/auth/logout`, { method: 'POST' })
  setCSRFToken('')
}

export const changePassword = async (
  currentPassword: string,
  newPassword: string
): Promise<void> => {
  await request(`${API}/auth/password`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ currentPassword, newPassword }),
  })
}

export const changeUsername = async (username: string): Promise<void> => {
  await request(`${API}/auth/username`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username }),
  })
}

export const saveAvatar = async (avatar: string): Promise<void> => {
  await request(`${API}/auth/avatar`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ avatar }),
  })
}

export const getSettings = async (): Promise<ServerSettings> => {
  const response = await request(`${API}/settings`, { method: 'GET' })
  return readJSON<ServerSettings>(response)
}

export const updatePreferences = async (
  preferences: Record<string, unknown>
): Promise<ServerSettings> => {
  const response = await request(`${API}/settings`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ preferences }),
  })
  return readJSON<ServerSettings>(response)
}

/** 保存后服务端只返回 hasApiKey，不会把 Key 回传。 */
export const saveApiKey = async (apiKey: string): Promise<void> => {
  await request(`${API}/settings/api-key`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ apiKey }),
  })
}

export const clearApiKey = async (): Promise<void> => {
  await request(`${API}/settings/api-key`, { method: 'DELETE' })
}
