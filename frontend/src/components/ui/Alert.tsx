/** Полоска с сообщением об ошибке или об успешном действии. */
import type { ReactNode } from 'react'

interface AlertProps {
  kind?: 'error' | 'success'
  children?: ReactNode
  onClose?: () => void
}

export function Alert({ kind = 'error', children, onClose }: AlertProps) {
  if (!children) return null
  return (
    <div className={`alert alert-${kind}`}>
      <span>{children}</span>
      {onClose && (
        <button type="button" className="alert-close" onClick={onClose}>
          ×
        </button>
      )}
    </div>
  )
}
