/**
 * Кто сейчас работает в системе.
 *
 * Хранит текущего пользователя и делится им со всеми страницами — чтобы не передавать
 * его вручную через каждый компонент. При запуске проверяет сохранённый токен: если он
 * ещё годен, сервер вернёт данные пользователя и вход повторять не придётся.
 *
 * Если сервер ответит «токен больше не годен», приложение узнаёт об этом через событие
 * и показывает экран входа — на какой бы странице человек ни находился.
 */
import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'

import { authApi, getToken, setToken, UNAUTHORIZED_EVENT } from '@/api'
import type { User } from '@/types/api'

interface AuthState {
  user: User | null
  loading: boolean
  isAdmin: boolean
  login: (username: string, password: string) => Promise<void>
  logout: () => void
}

const AuthContext = createContext<AuthState | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null)
  const [loading, setLoading] = useState(true)

  const refresh = useCallback(async () => {
    if (!getToken()) {
      setUser(null)
      setLoading(false)
      return
    }
    try {
      setUser(await authApi.me())
    } catch {
      setUser(null)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void refresh()
    const onUnauthorized = () => setUser(null)
    window.addEventListener(UNAUTHORIZED_EVENT, onUnauthorized)
    return () => window.removeEventListener(UNAUTHORIZED_EVENT, onUnauthorized)
  }, [refresh])

  const value = useMemo<AuthState>(
    () => ({
      user,
      loading,
      isAdmin: user?.role === 'admin',
      async login(username: string, password: string) {
        await authApi.login(username, password)
        setUser(await authApi.me())
      },
      logout() {
        setToken(null)
        setUser(null)
      },
    }),
    [user, loading],
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthState {
  const context = useContext(AuthContext)
  if (!context) throw new Error('useAuth вызван вне AuthProvider')
  return context
}
