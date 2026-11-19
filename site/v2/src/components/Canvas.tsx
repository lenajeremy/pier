import type { ReactNode } from 'react'
import s from './Canvas.module.css'

// The diagram canvas from the design system: navy 25 with fine dots, a
// hairline frame, and selection-handle squares at the corners.
export function Canvas({
  children,
  handles = 'all',
  className,
}: {
  children: ReactNode
  handles?: 'all' | 'top' | 'none'
  className?: string
}) {
  return (
    <div className={[s.canvas, className].filter(Boolean).join(' ')}>
      {handles !== 'none' && (
        <>
          <span className={`${s.handle} ${s.tl}`} aria-hidden="true" />
          <span className={`${s.handle} ${s.tr}`} aria-hidden="true" />
        </>
      )}
      {handles === 'all' && (
        <>
          <span className={`${s.handle} ${s.bl}`} aria-hidden="true" />
          <span className={`${s.handle} ${s.br}`} aria-hidden="true" />
        </>
      )}
      {children}
    </div>
  )
}
