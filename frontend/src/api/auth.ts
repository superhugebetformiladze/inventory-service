/** Вход в систему и получение данных текущего пользователя. */
import type { User } from '@/types/api'

import { ApiError, request, setToken } from './client'

interface TokenResponse {
  access_token: string
  token_type: string
}

export async function login(username: string, password: string): Promise<void> {
  const form = new URLSearchParams()
  form.set('username', username)
  form.set('password', password)

  const response = await fetch('/api/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: form,
  })

  if (!response.ok) {
    const data = (await response.json().catch(() => ({}))) as { detail?: string }
    throw new ApiError(data.detail || 'Не удалось войти', response.status)
  }

  const data = (await response.json()) as TokenResponse
  setToken(data.access_token)
}

export const me = (): Promise<User> => request<User>('/auth/me')
