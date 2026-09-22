/**
 * Основа общения с сервером: один помощник на все запросы.
 *
 * Делает три вещи, которые иначе пришлось бы повторять в каждом месте:
 *   * подставляет токен входа в заголовок каждого запроса;
 *   * превращает ответ сервера об ошибке в понятный текст для пользователя;
 *   * замечает ответ «вы не авторизованы» и сообщает приложению, что пора показать вход.
 *
 * Токен хранится в localStorage — это хранилище браузера, которое переживает
 * перезагрузку страницы.
 */
const TOKEN_KEY = 'xmlparser.token'

/** Событие для AuthProvider: токен протух, надо показать экран входа. */
export const UNAUTHORIZED_EVENT = 'xmlparser:unauthorized'

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY)
}

export function setToken(token: string | null): void {
  if (token) localStorage.setItem(TOKEN_KEY, token)
  else localStorage.removeItem(TOKEN_KEY)
}

export class ApiError extends Error {
  readonly status: number

  constructor(message: string, status: number) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

interface RequestOptions {
  method?: 'GET' | 'POST' | 'PATCH' | 'PUT' | 'DELETE'
  body?: unknown
  form?: FormData | URLSearchParams
  raw?: boolean
}

async function readError(response: Response): Promise<string> {
  try {
    const data = await response.json()
    const detail = (data as { detail?: unknown }).detail
    if (Array.isArray(detail)) {
      return detail
        .map((item) => (typeof item === 'object' && item && 'msg' in item ? String(item.msg) : String(item)))
        .join(', ')
    }
    if (detail) return String(detail)
  } catch {
    /* тело не JSON — оставляем код ошибки */
  }
  return `Ошибка ${response.status}`
}

export async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { method = 'GET', body, form, raw } = options
  const headers: Record<string, string> = {}
  const token = getToken()
  if (token) headers.Authorization = `Bearer ${token}`

  let payload: BodyInit | undefined
  if (form) {
    payload = form
  } else if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
    payload = JSON.stringify(body)
  }

  const response = await fetch(`/api${path}`, { method, headers, body: payload })

  if (response.status === 401) {
    setToken(null)
    window.dispatchEvent(new Event(UNAUTHORIZED_EVENT))
    throw new ApiError('Сессия истекла, войдите заново', 401)
  }

  if (!response.ok) {
    throw new ApiError(await readError(response), response.status)
  }

  if (raw) return response as unknown as T
  if (response.status === 204) return undefined as T
  return (await response.json()) as T
}

/** Собирает query-строку, пропуская пустые значения; массивы повторяются ключом. */
export function qs(params?: object): string {
  const search = new URLSearchParams()
  Object.entries(params ?? {}).forEach(([key, value]) => {
    if (value === undefined || value === null || value === '') return
    if (Array.isArray(value)) value.forEach((item) => search.append(key, String(item)))
    else search.append(key, String(value))
  })
  const text = search.toString()
  return text ? `?${text}` : ''
}

export async function saveBlob(blob: Blob, name: string): Promise<void> {
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = name
  link.click()
  URL.revokeObjectURL(url)
}
