/**
 * Карта страниц приложения.
 *
 * Здесь написано, какой адрес какую страницу открывает: «/» — аналитика,
 * «/machine/…» — карточка станка, «/production» — план и факт выпуска,
 * «/admin» — админка, «/login» — вход.
 * Обёртка Protected не пускает дальше тех, кто не вошёл, а раздел админки —
 * ещё и тех, у кого нет прав администратора.
 */
import { Navigate, Route, Routes } from 'react-router-dom'
import type { ReactNode } from 'react'

import { useAuth } from '@/auth/AuthContext'
import { AdminPage } from '@/features/admin/AdminPage'
import { DashboardPage } from '@/features/dashboard/DashboardPage'
import { LoginPage } from '@/features/login/LoginPage'
import { MachinePage } from '@/features/machine/MachinePage'
import { ProductionPage } from '@/features/production/ProductionPage'

import { Layout } from './Layout'

interface ProtectedProps {
  children: ReactNode
  adminOnly?: boolean
}

/** Пускает дальше только вошедших; adminOnly — только администраторов. */
function Protected({ children, adminOnly = false }: ProtectedProps) {
  const { user, loading, isAdmin } = useAuth()
  if (loading) return <div className="page-loader">Загрузка…</div>
  if (!user) return <Navigate to="/login" replace />
  if (adminOnly && !isAdmin) return <Navigate to="/" replace />
  return <>{children}</>
}

export function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route
        element={
          <Protected>
            <Layout />
          </Protected>
        }
      >
        <Route path="/" element={<DashboardPage />} />
        <Route path="/machine/:machineKey" element={<MachinePage />} />
        <Route path="/production" element={<ProductionPage />} />
        <Route
          path="/admin"
          element={
            <Protected adminOnly>
              <AdminPage />
            </Protected>
          }
        />
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}
