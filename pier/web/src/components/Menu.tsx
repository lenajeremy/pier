import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { navigate } from '../lib/router'
import { More } from './Icons'

export type MenuItem =
  | {
      label: string
      icon?: ReactNode
      hint?: string // a shortcut or a short note, at the end
      danger?: boolean
      disabled?: boolean
      onSelect?: () => void
      to?: string // a page in the dashboard
      href?: string // somewhere else, in a new tab
    }
  | 'separator'

// Menu is the Row actions menu (Components 4). It's drawn over the page, at
// the button that opened it, so a table's edge never cuts it off, and it
// opens upward when there's no room below.
export function Menu({ items, label, children, align = 'end', className }: {
  items: MenuItem[]
  label: string // what the button is for, read aloud
  children?: ReactNode // the button's content; the more dots if left out
  align?: 'start' | 'end'
  className?: string
}) {
  const [open, setOpen] = useState(false)
  const button = useRef<HTMLButtonElement>(null)
  const menu = useRef<HTMLDivElement>(null)
  const [at, setAt] = useState<{ left: number; top: number } | null>(null)

  useLayoutEffect(() => {
    if (!open || !button.current || !menu.current) return
    const b = button.current.getBoundingClientRect()
    const m = menu.current.getBoundingClientRect()
    const below = b.bottom + 6 + m.height <= window.innerHeight - 8
    const left = align === 'end' ? b.right - m.width : b.left
    setAt({
      left: Math.min(window.innerWidth - m.width - 8, Math.max(8, left)),
      top: below ? b.bottom + 6 : Math.max(8, b.top - 6 - m.height),
    })
  }, [open, align, items.length])

  useEffect(() => {
    if (!open) return
    const first = menu.current?.querySelector<HTMLElement>('.menu-item:not([disabled])')
    first?.focus()
    const down = (e: MouseEvent) => {
      const t = e.target as Node
      if (!menu.current?.contains(t) && !button.current?.contains(t)) setOpen(false)
    }
    const away = () => setOpen(false)
    document.addEventListener('mousedown', down)
    window.addEventListener('resize', away)
    window.addEventListener('scroll', away, true)
    return () => {
      document.removeEventListener('mousedown', down)
      window.removeEventListener('resize', away)
      window.removeEventListener('scroll', away, true)
    }
  }, [open])

  const key = (e: React.KeyboardEvent) => {
    const rows = [...(menu.current?.querySelectorAll<HTMLElement>('.menu-item:not([disabled])') ?? [])]
    const i = rows.indexOf(document.activeElement as HTMLElement)
    if (e.key === 'Escape' || e.key === 'Tab') {
      e.preventDefault()
      setOpen(false)
      button.current?.focus()
    } else if (e.key === 'ArrowDown') {
      e.preventDefault()
      rows[(i + 1) % rows.length]?.focus()
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      rows[(i - 1 + rows.length) % rows.length]?.focus()
    } else if (e.key === 'Home') {
      e.preventDefault()
      rows[0]?.focus()
    } else if (e.key === 'End') {
      e.preventDefault()
      rows[rows.length - 1]?.focus()
    }
  }

  const choose = (item: Exclude<MenuItem, 'separator'>) => {
    setOpen(false)
    button.current?.focus()
    if (item.to) navigate(item.to)
    item.onSelect?.()
  }

  return (
    <>
      <button
        ref={button}
        type="button"
        className={className ?? 'icon-btn'}
        aria-label={label}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={(e) => {
          e.stopPropagation()
          setOpen(!open)
        }}
      >
        {children ?? <More />}
      </button>
      {open &&
        createPortal(
          <div
            ref={menu}
            className="menu floating"
            role="menu"
            aria-label={label}
            style={at ? { left: at.left, top: at.top } : { left: 0, top: 0, visibility: 'hidden' }}
            onKeyDown={key}
            onClick={(e) => e.stopPropagation()}
          >
            {items.map((item, i) =>
              item === 'separator' ? (
                <div key={i} className="menu-sep" role="separator" />
              ) : item.href ? (
                <a
                  key={item.label}
                  className="menu-item"
                  role="menuitem"
                  href={item.href}
                  target="_blank"
                  rel="noreferrer"
                  onClick={() => setOpen(false)}
                >
                  {item.icon}
                  <span className="grow">{item.label}</span>
                  {item.hint && <span className="menu-hint">{item.hint}</span>}
                </a>
              ) : (
                <button
                  key={item.label}
                  type="button"
                  className={'menu-item' + (item.danger ? ' danger' : '')}
                  role="menuitem"
                  disabled={item.disabled}
                  onClick={() => choose(item)}
                >
                  {item.icon}
                  <span className="grow">{item.label}</span>
                  {item.hint && <span className="menu-hint">{item.hint}</span>}
                </button>
              ),
            )}
          </div>,
          document.body,
        )}
    </>
  )
}
