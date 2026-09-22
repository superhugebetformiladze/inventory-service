/** Управление пользователями — доступно только администраторам. */
import type { Role, User } from '@/types/api'

import { request } from './client'

export interface UserCreate {
  username: string
  password: string
  full_name?: string
  role: Role
}

export interface UserUpdate {
  password?: string
  full_name?: string
  role?: Role
  is_active?: boolean
}

export const list = (): Promise<User[]> => request<User[]>('/users')

export const create = (body: UserCreate): Promise<User> =>
  request<User>('/users', { method: 'POST', body })

export const update = (id: number, body: UserUpdate): Promise<User> =>
  request<User>(`/users/${id}`, { method: 'PATCH', body })

export const remove = (id: number): Promise<void> => request<void>(`/users/${id}`, { method: 'DELETE' })
