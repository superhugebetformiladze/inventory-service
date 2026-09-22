/** Раздел админки «Пользователи»: создание учёток, смена пароля, отключение. */
import { useCallback, useEffect, useState } from 'react'
import type { FormEvent } from 'react'

import { usersApi } from '@/api'
import type { UserCreate } from '@/api/users'
import { Alert, Card, Loader } from '@/components/ui'
import { formatDateTime } from '@/lib/format'
import type { User } from '@/types/api'

const EMPTY_FORM: UserCreate = { username: '', password: '', full_name: '', role: 'viewer' }

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

export function UsersPanel() {
  const [users, setUsers] = useState<User[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [form, setForm] = useState<UserCreate>(EMPTY_FORM)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      setUsers(await usersApi.list())
    } catch (err) {
      setError(message(err))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  async function onCreate(event: FormEvent): Promise<void> {
    event.preventDefault()
    setError('')
    try {
      await usersApi.create(form)
      setForm(EMPTY_FORM)
      await load()
    } catch (err) {
      setError(message(err))
    }
  }

  async function toggleActive(user: User): Promise<void> {
    try {
      await usersApi.update(user.id, { is_active: !user.is_active })
      await load()
    } catch (err) {
      setError(message(err))
    }
  }

  async function resetPassword(user: User): Promise<void> {
    const password = window.prompt(`Новый пароль для «${user.username}» (минимум 8 символов)`)
    if (!password) return
    if (password.length < 8) {
      setError('Пароль должен быть не короче 8 символов')
      return
    }
    try {
      await usersApi.update(user.id, { password })
    } catch (err) {
      setError(message(err))
    }
  }

  async function remove(user: User): Promise<void> {
    if (!window.confirm(`Удалить пользователя «${user.username}»?`)) return
    try {
      await usersApi.remove(user.id)
      await load()
    } catch (err) {
      setError(message(err))
    }
  }

  return (
    <>
      <Alert onClose={() => setError('')}>{error}</Alert>

      <Card title="Новый пользователь">
        <form className="inline-form" onSubmit={(event) => void onCreate(event)}>
          <label className="field">
            <span>Логин</span>
            <input
              value={form.username}
              onChange={(event) => setForm({ ...form, username: event.target.value })}
              required
              minLength={3}
            />
          </label>
          <label className="field">
            <span>Имя</span>
            <input
              value={form.full_name ?? ''}
              onChange={(event) => setForm({ ...form, full_name: event.target.value })}
            />
          </label>
          <label className="field">
            <span>Пароль</span>
            <input
              type="password"
              value={form.password}
              onChange={(event) => setForm({ ...form, password: event.target.value })}
              required
              minLength={8}
            />
          </label>
          <label className="field">
            <span>Роль</span>
            <select
              value={form.role}
              onChange={(event) =>
                setForm({ ...form, role: event.target.value as UserCreate['role'] })
              }
            >
              <option value="viewer">Просмотр</option>
              <option value="admin">Администратор</option>
            </select>
          </label>
          <button type="submit" className="btn primary">
            Создать
          </button>
        </form>
      </Card>

      <Card title="Пользователи" className="wide">
        {loading ? (
          <Loader />
        ) : (
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th>Логин</th>
                  <th>Имя</th>
                  <th>Роль</th>
                  <th>Статус</th>
                  <th>Создан</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {users.map((user) => (
                  <tr key={user.id}>
                    <td className="strong">{user.username}</td>
                    <td>{user.full_name || '—'}</td>
                    <td>{user.role === 'admin' ? 'Администратор' : 'Просмотр'}</td>
                    <td>
                      <span className={`status status-${user.is_active ? 'ok' : 'off'}`}>
                        {user.is_active ? 'активен' : 'отключён'}
                      </span>
                    </td>
                    <td className="muted">{formatDateTime(user.created_at)}</td>
                    <td className="row-actions">
                      <button
                        type="button"
                        className="btn link"
                        onClick={() => void toggleActive(user)}
                      >
                        {user.is_active ? 'отключить' : 'включить'}
                      </button>
                      <button
                        type="button"
                        className="btn link"
                        onClick={() => void resetPassword(user)}
                      >
                        пароль
                      </button>
                      <button
                        type="button"
                        className="btn link danger"
                        onClick={() => void remove(user)}
                      >
                        удалить
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </>
  )
}
