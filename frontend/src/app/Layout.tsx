/**
 * Общая рамка всех страниц: шапка с логотипом, меню и кнопкой «Выйти».
 *
 * Сама страница подставляется в <Outlet /> — это место, куда роутер вставляет
 * содержимое текущего раздела. Пункт «Админка» виден только администраторам.
 */
import { NavLink, Outlet } from 'react-router-dom'

import { useAuth } from '@/auth/AuthContext'

export function Layout() {
  const { user, isAdmin, logout } = useAuth()

  return (
    <div className="app">
      <header className="app-header">
        <div className="brand">
          <span className="brand-mark" />
          <div>
            <div className="brand-title">Мониторинг оборудования</div>
            <div className="brand-sub">разбор XML-выгрузок</div>
          </div>
        </div>

        <nav className="app-nav">
          <NavLink to="/" end className={({ isActive }) => (isActive ? 'active' : '')}>
            Аналитика
          </NavLink>
          <NavLink to="/production" className={({ isActive }) => (isActive ? 'active' : '')}>
            Продукция
          </NavLink>
          {isAdmin && (
            <NavLink to="/admin" className={({ isActive }) => (isActive ? 'active' : '')}>
              Админка
            </NavLink>
          )}
        </nav>

        <div className="app-user">
          <div className="app-user-name">
            {user?.full_name || user?.username}
            <span className="role-badge">{isAdmin ? 'админ' : 'просмотр'}</span>
          </div>
          <button type="button" className="btn ghost" onClick={logout}>
            Выйти
          </button>
        </div>
      </header>

      <main className="app-main">
        <Outlet />
      </main>
    </div>
  )
}
