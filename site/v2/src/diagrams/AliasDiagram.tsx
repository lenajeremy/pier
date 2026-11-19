import type { CSSProperties } from 'react'
import { d } from '../lib/inView'
import a from './alias.module.css'
import { Arrowhead, c, Handles, T } from './parts'

const TITLE =
  'Production, blog.pier.example.com, points at deployment 8f3a2c. Running pier rollback moves it to the older deployment 41d0e9. The preview feat-search--blog.pier.example.com points at c19e77.'

type Box = { x: number; y: number; w: number; h: number; id: string; meta: string }

function DeployBox({ x, y, w, h, id, meta }: Box) {
  return (
    <g>
      <rect x={x} y={y} width={w} height={h} fill={c.white} stroke={c.pierd} strokeOpacity={0.45} strokeWidth={1.2} />
      <T x={x + 14} y={y + (h > 56 ? 26 : 22)} fill={c.navy900} mono>
        {id}
      </T>
      <T x={x + 14} y={y + (h > 56 ? 48 : 41)} size={12}>
        {meta}
      </T>
    </g>
  )
}

// The "serving" state drawn over a deployment: pierd tint, full stroke and
// selection handles. The loop fades it between 8f3a2c and 41d0e9.
function Serving({ className, ...b }: Box & { className: string }) {
  return (
    <g className={`${className} m-loop`}>
      <rect x={b.x} y={b.y} width={b.w} height={b.h} fill={c.pierdTint} stroke={c.pierd} strokeWidth={1.2} />
      <T x={b.x + 14} y={b.y + (b.h > 56 ? 26 : 22)} fill={c.navy900} mono>
        {b.id}
      </T>
      <T x={b.x + 14} y={b.y + (b.h > 56 ? 48 : 41)} size={12}>
        {b.meta}
      </T>
      <Handles x={b.x} y={b.y} w={b.w} h={b.h} color={c.pierd} />
    </g>
  )
}

function Chip({ x, y, w, children }: { x: number; y: number; w: number; children: string }) {
  return (
    <g>
      <rect x={x} y={y} width={w} height={26} rx={4} fill={c.white} stroke={c.navy100} />
      <T x={x + 10} y={y + 17} size={12} fill={c.navy900} mono>
        {children}
      </T>
    </g>
  )
}

// "Rollbacks rebuild nothing", wide layout (board: Alias diagram).
export function AliasDiagramWide({ className }: { className?: string }) {
  const old: Box = { x: 300, y: 170, w: 124, h: 64, id: '8f3a2c', meta: 'main, 2 hours ago' }
  const prev: Box = { x: 160, y: 170, w: 124, h: 64, id: '41d0e9', meta: 'main, yesterday' }
  return (
    <svg
      className={className}
      style={{ '--move': 'translateX(-140px)' } as CSSProperties}
      viewBox="6 6 628 318"
      role="img"
      aria-labelledby="alias-wide-title"
    >
      <title id="alias-wide-title">{TITLE}</title>
      <defs>
        <Arrowhead id="alias-wide-arrow" color={c.caddy} />
      </defs>
      <path className="m-wipe" style={d(100)} d="M24 202H616" stroke={c.navy200} strokeDasharray="3 4" />

      <g className="m-rise" style={d(150, { '--i': 0, '--stagger': '90ms' })}>
        <DeployBox x={20} y={170} w={124} h={64} id="7a2b10" meta="main, 3 days ago" />
      </g>
      <g className="m-rise" style={d(150, { '--i': 1, '--stagger': '90ms' })}>
        <DeployBox {...prev} />
        <Serving className={a.hiNew} {...prev} />
      </g>
      <g className="m-rise" style={d(150, { '--i': 2, '--stagger': '90ms' })}>
        <DeployBox {...old} />
        <Serving className={a.hiOld} {...old} />
      </g>
      <g className="m-rise" style={d(150, { '--i': 3, '--stagger': '90ms' })}>
        <DeployBox x={440} y={170} w={124} h={64} id="c19e77" meta="feat-search, 3 min" />
      </g>

      <g className="m-fade" style={d(700)}>
        <g className={`${a.hintBack} m-loop`}>
          <path
            className={a.march}
            d="M296 68C296 124 222 116 222 164"
            fill="none"
            stroke={c.caddy}
            strokeWidth={1.3}
            strokeDasharray="4 4"
            markerEnd="url(#alias-wide-arrow)"
          />
          <Chip x={104} y={98} w={118}>
            pier rollback
          </Chip>
        </g>
        <g className={`${a.hintFwd} m-loop`}>
          <path
            className={a.march}
            d="M288 68C288 124 362 116 362 164"
            fill="none"
            stroke={c.caddy}
            strokeWidth={1.3}
            strokeDasharray="4 4"
            markerEnd="url(#alias-wide-arrow)"
          />
          <Chip x={384} y={98} w={112}>
            pier promote
          </Chip>
        </g>
      </g>

      {/* Production: the alias that moves. */}
      <g className="m-rise" style={d(500)}>
        <g className={`${a.move} m-loop`}>
          <T x={262} y={30} weight={500} fill={c.caddyInk}>
            production
          </T>
          <rect x="262" y="38" width="200" height="30" rx="15" fill={c.caddyTint} stroke={c.caddy} strokeWidth={1.2} />
          <T x={362} y={58} size={12} fill={c.navy900} mono anchor="middle">
            blog.pier.example.com
          </T>
          <path d="M362 68V164" stroke={c.caddy} strokeWidth={1.4} markerEnd="url(#alias-wide-arrow)" />
        </g>
      </g>

      <g className="m-rise" style={d(600)}>
        <path d="M502 274V242" stroke={c.caddy} strokeWidth={1.4} markerEnd="url(#alias-wide-arrow)" />
        <rect x="368" y="276" width="262" height="30" rx="15" fill={c.white} stroke={c.caddy} strokeWidth={1.2} />
        <T x={499} y={296} size={11} fill={c.navy900} mono anchor="middle">
          feat-search--blog.pier.example.com
        </T>
      </g>
      <T className="m-fade" style={d(700)} x={24} y={296} fill={c.navy500}>
        Deployments for blog, oldest first
      </T>
    </svg>
  )
}

// Phone layout: deployments in a column, newest first. Production slides
// down one row on rollback.
export function AliasDiagramTall({ className }: { className?: string }) {
  const old: Box = { x: 186, y: 122, w: 144, h: 52, id: '8f3a2c', meta: 'main, 2 hours ago' }
  const prev: Box = { x: 186, y: 188, w: 144, h: 52, id: '41d0e9', meta: 'main, yesterday' }
  return (
    <svg
      className={className}
      style={{ '--move': 'translateY(66px)' } as CSSProperties}
      viewBox="4 4 342 364"
      role="img"
      aria-labelledby="alias-tall-title"
    >
      <title id="alias-tall-title">{TITLE}</title>
      <defs>
        <Arrowhead id="alias-tall-arrow" color={c.caddy} />
      </defs>
      <T className="m-fade" x={20} y={36} fill={c.navy500}>
        Deployments for blog, newest first
      </T>

      <g className="m-rise" style={d(100, { '--i': 0, '--stagger': '80ms' })}>
        <DeployBox x={186} y={56} w={144} h={52} id="c19e77" meta="feat-search, 3 min" />
      </g>
      <g className="m-rise" style={d(100, { '--i': 1, '--stagger': '80ms' })}>
        <DeployBox {...old} />
        <Serving className={a.hiOld} {...old} />
      </g>
      <g className="m-rise" style={d(100, { '--i': 2, '--stagger': '80ms' })}>
        <DeployBox {...prev} />
        <Serving className={a.hiNew} {...prev} />
      </g>
      <g className="m-rise" style={d(100, { '--i': 3, '--stagger': '80ms' })}>
        <DeployBox x={186} y={254} w={144} h={52} id="7a2b10" meta="main, 3 days ago" />
      </g>

      <g className="m-rise" style={d(420)}>
        <rect x="18" y="58" width="150" height="48" rx="14" fill={c.white} stroke={c.caddy} strokeWidth={1.2} />
        <T x={32} y={77} size={12} weight={500} fill={c.gitInk}>
          preview
        </T>
        <T x={32} y={95} size={10.5} fill={c.navy900} mono>
          feat-search--blog
        </T>
        <path d="M168 82H180" stroke={c.caddy} strokeWidth={1.4} markerEnd="url(#alias-tall-arrow)" />
      </g>

      <g className="m-fade" style={d(650)}>
        <g className={`${a.hintBack} m-loop`}>
          <path
            className={a.march}
            d="M92 172C92 204 140 214 180 214"
            fill="none"
            stroke={c.caddy}
            strokeWidth={1.3}
            strokeDasharray="4 4"
            markerEnd="url(#alias-tall-arrow)"
          />
          <Chip x={18} y={226} w={112}>
            pier rollback
          </Chip>
        </g>
        <g className={`${a.hintFwd} m-loop`}>
          <path
            className={a.march}
            d="M110 190C110 162 150 148 180 148"
            fill="none"
            stroke={c.caddy}
            strokeWidth={1.3}
            strokeDasharray="4 4"
            markerEnd="url(#alias-tall-arrow)"
          />
          <Chip x={18} y={262} w={112}>
            pier promote
          </Chip>
        </g>
      </g>

      <g className="m-rise" style={d(520)}>
        <g className={`${a.move} m-loop`}>
          <rect x="18" y="124" width="150" height="48" rx="14" fill={c.caddyTint} stroke={c.caddy} strokeWidth={1.2} />
          <T x={32} y={143} size={12} weight={500} fill={c.caddyInk}>
            production
          </T>
          <T x={32} y={161} size={10.5} fill={c.navy900} mono>
            blog.pier.example.com
          </T>
          <path d="M168 148H180" stroke={c.caddy} strokeWidth={1.4} markerEnd="url(#alias-tall-arrow)" />
        </g>
      </g>

      <g className="m-fade" style={d(750)}>
        <T x={20} y={342}>Solid: what each alias serves now.</T>
        <T x={20} y={360}>Dashed: where a rollback moves it.</T>
      </g>
    </svg>
  )
}
