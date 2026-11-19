import type { CSSProperties, ReactNode } from 'react'
import f from './flow.module.css'

// Literal values for SVG presentation attributes (they can't read CSS
// variables). Keep in sync with styles/tokens.css.
export const c = {
  white: '#FFFFFF',
  navy900: '#142047',
  navy600: '#56617F',
  navy500: '#646F8C',
  navy400: '#8C95AD',
  navy300: '#B9C2D4',
  navy200: '#C9D0DD',
  navy100: '#E3E7EF',
  navy50: '#F1F4F9',
  windowLine: '#D5DBE6',
  git: '#6E56CF',
  gitTint: '#F4F1FF',
  gitSoft: '#EAE4FF',
  gitInk: '#5B44B8',
  pierd: '#2F6FEB',
  pierdTint: '#EEF4FF',
  pierdSoft: '#E0EAFF',
  pierdInk: '#1F57C9',
  docker: '#0E9384',
  dockerTint: '#EAF8F5',
  dockerSoft: '#D9F2EC',
  dockerInk: '#0B7468',
  caddy: '#C53D8C',
  caddyTint: '#FDF0F6',
  caddySoft: '#FBE2EE',
  caddyInk: '#A92F75',
}

const SANS = "'Instrument Sans', ui-sans-serif, system-ui, sans-serif"
const MONO = "'Red Hat Mono', ui-monospace, Menlo, monospace"

export function T({
  x,
  y,
  children,
  size = 13,
  weight,
  fill = c.navy600,
  mono = false,
  anchor,
  transform,
  className,
  style,
}: {
  x: number
  y: number
  children: ReactNode
  size?: number
  weight?: number
  fill?: string
  mono?: boolean
  anchor?: 'start' | 'middle' | 'end'
  transform?: string
  className?: string
  style?: CSSProperties
}) {
  return (
    <text
      className={className}
      style={style}
      x={x}
      y={y}
      fontFamily={mono ? MONO : SANS}
      fontSize={size}
      fontWeight={weight}
      fill={fill}
      textAnchor={anchor}
      transform={transform}
    >
      {children}
    </text>
  )
}

// Part label above a node: the part's real name in its ink colour, 14 Medium.
export function Label({ x, y, fill, size = 14, children }: { x: number; y: number; fill: string; size?: number; children: ReactNode }) {
  return (
    <T x={x} y={y} size={size} weight={500} fill={fill}>
      {children}
    </T>
  )
}

// 7px selection handles on the corners of the node the diagram is about.
export function Handles({ x, y, w, h, color, size = 7 }: { x: number; y: number; w: number; h: number; color: string; size?: number }) {
  const o = size / 2 + 0.5
  const pts = [
    [x - o, y - o],
    [x + w - size + o, y - o],
    [x - o, y + h - size + o],
    [x + w - size + o, y + h - size + o],
  ]
  return (
    <g fill={c.white} stroke={color}>
      {pts.map(([px, py], i) => (
        <rect key={i} x={px} y={py} width={size} height={size} />
      ))}
    </g>
  )
}

export function Arrowhead({ id, color }: { id: string; color: string }) {
  return (
    <marker id={id} viewBox="0 0 10 10" refX="8" refY="5" markerWidth="6" markerHeight="6" orient="auto">
      <path d="M0 0L10 5L0 10z" fill={color} />
    </marker>
  )
}

// A ring that flashes around a part as a looping packet reaches it. t is the
// moment (seconds into the loop) the packet arrives; see flow.module.css.
export function Halo({ x, y, w, h, color, t, rx = 0 }: { x: number; y: number; w: number; h: number; color: string; t: number; rx?: number }) {
  return (
    <rect
      className={`${f.halo} m-loop`}
      style={{ '--t': `${t}s` } as CSSProperties}
      x={x}
      y={y}
      width={w}
      height={h}
      rx={rx}
      fill="none"
      stroke={color}
      strokeWidth={2}
      strokeOpacity={0.55}
    />
  )
}
