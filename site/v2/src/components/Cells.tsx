import { createElement, type CSSProperties, type ReactNode } from 'react'
import { useInView } from '../lib/inView'
import s from './Cells.module.css'

// A row of equal cells that share hairlines: solid between rows, dashed
// between columns, with no gutters (design system part 4, "Cell row").
// Use as="ol"/"ul" with <Cell as="li"> when the cells are a list.
export function Cells({
  children,
  cols,
  bottom = false,
  as: Grid = 'div',
  label,
  stagger = 90,
  className,
}: {
  children: ReactNode
  cols: [phone: number, tablet: number, desktop: number]
  bottom?: boolean
  as?: 'div' | 'ol' | 'ul'
  label?: string
  stagger?: number
  className?: string
}) {
  const style = { '--c1': cols[0], '--c2': cols[1], '--c3': cols[2], '--stagger-lg': `${stagger}ms` } as CSSProperties
  return (
    <div className={[s.cells, bottom && s.bottom, className].filter(Boolean).join(' ')}>
      <Grid className={s.grid} style={style} aria-label={label}>
        {children}
      </Grid>
    </div>
  )
}

// Each cell plays its own entrance when it scrolls into view. --i staggers
// cells that arrive together (wide screens only; on phones they arrive one
// at a time anyway). Cells are "live" so loops inside them pause off screen.
export function Cell({
  children,
  as = 'div',
  index = 0,
  className,
}: {
  children: ReactNode
  as?: 'div' | 'li'
  index?: number
  className?: string
}) {
  const { ref, seen, visible } = useInView<HTMLElement>({ live: true })
  return createElement(
    as,
    {
      ref,
      className: [s.cell, className].filter(Boolean).join(' '),
      style: { '--i': index } as CSSProperties,
      'data-in': seen ? '' : undefined,
      'data-live': visible ? '' : undefined,
    },
    children,
  )
}
