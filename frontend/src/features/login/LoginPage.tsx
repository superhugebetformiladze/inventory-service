/** Экран входа: логин, пароль и сообщение об ошибке. */
import { useState } from 'react'
import type { FormEvent } from 'react'
import { Navigate } from 'react-router-dom'

import { useAuth } from '@/auth/AuthContext'
import { Alert } from '@/components/ui'

export function LoginPage() {
  const { user, loading, login } = useAuth()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  if (loading) return <div className="page-loader">Загрузка…</div>
  if (user) return <Navigate to="/" replace />

  async function onSubmit(event: FormEvent): Promise<void> {
    event.preventDefault()
    setError('')
    setBusy(true)
    try {
      await login(username.trim(), password)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="login-page">
      <form className="login-card" onSubmit={(event) => void onSubmit(event)}>
        <div className="login-brand">
          <span className="brand-mark" />
          <div>
            <h1>Мониторинг оборудования</h1>
            <p>Анализ XML-выгрузок системы мониторинга станков</p>
          </div>
        </div>

        <Alert onClose={() => setError('')}>{error}</Alert>

        <label className="field">
          <span>Логин</span>
          <input
            value={username}
            onChange={(event) => setUsername(event.target.value)}
            autoComplete="username"
            required
          />
        </label>

        <label className="field">
          <span>Пароль</span>
          <input
            type="password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
            autoComplete="current-password"
            required
          />
        </label>

        <button type="submit" className="btn primary block" disabled={busy}>
          {busy ? 'Проверяем…' : 'Войти'}
        </button>
      </form>
    </div>
  )
}
