/**
 * Точка входа: здесь браузер начинает выполнять приложение.
 *
 * Находим на странице пустой контейнер <div id="root"> и просим React нарисовать
 * внутри него приложение. Вокруг него две обёртки: BrowserRouter отвечает за адреса
 * страниц, AuthProvider — за то, кто сейчас вошёл в систему.
 */
import React from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'

import { App } from '@/app/App'
import { AuthProvider } from '@/auth/AuthContext'

import '@/styles/index.css'

const container = document.getElementById('root')
if (!container) throw new Error('Не найден контейнер #root')

createRoot(container).render(
  <React.StrictMode>
    <BrowserRouter>
      <AuthProvider>
        <App />
      </AuthProvider>
    </BrowserRouter>
  </React.StrictMode>,
)
