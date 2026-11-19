import { d, InView } from '../lib/inView'
import { AliasPanel, WindowBar } from './DashboardSection'
import { WaitlistForm } from './WaitlistForm'
import s from './WaitlistBand.module.css'

// The four parts of Pier in their fixed order, the brand signature. On
// entrance the boxes arrive left to right; then a deploy pulse runs through
// them on a loop and the connectors flow toward Caddy.
function Signature() {
  const boxes = [
    { x: 2, fill: 'rgba(110,86,207,0.18)', stroke: '#9C88F0' },
    { x: 98, fill: 'rgba(47,111,235,0.18)', stroke: '#6E9BF5' },
    { x: 194, fill: 'rgba(14,147,132,0.2)', stroke: '#3CC2B0' },
    { x: 290, fill: 'rgba(197,61,140,0.2)', stroke: '#E57AB6' },
  ]
  return (
    <svg className={s.signature} viewBox="0 0 332 44" fill="none" aria-hidden="true">
      <g className="m-wipe" style={d(300, { '--dur': '900ms' })}>
        <path className={`${s.flow} m-loop`} d="M44 22H96M140 22H192M236 22H288" stroke="#56617F" strokeDasharray="4 4" />
      </g>
      {boxes.map((b, i) => (
        <g key={b.x} className="m-pop" style={d(100, { '--i': i, '--stagger': '140ms' })}>
          <rect x={b.x} y="2" width="40" height="40" fill={b.fill} stroke={b.stroke} strokeWidth={1.2} />
          <rect
            className={`${s.pulse} m-loop`}
            style={{ ['--t' as string]: `${i * 0.35}s` }}
            x={b.x}
            y="2"
            width="40"
            height="40"
            fill={b.stroke}
            fillOpacity={0.22}
          />
        </g>
      ))}
      <g stroke="#9C88F0" strokeWidth={1.4} className="m-fade" style={d(150)}>
        <circle cx="17" cy="15" r="2.4" />
        <circle cx="17" cy="29" r="2.4" />
        <circle cx="27" cy="18" r="2.4" />
        <path d="M17 17.4V26.6M27 20.4C27 25 17 23 17 26.6" />
      </g>
      <g stroke="#6E9BF5" strokeWidth={1.4} className="m-fade" style={d(290)}>
        <ellipse cx="118" cy="15" rx="7" ry="2.8" />
        <path d="M111 15V29C111 30.6 114 31.8 118 31.8C122 31.8 125 30.6 125 29V15M111 22C111 23.6 114 24.8 118 24.8C122 24.8 125 23.6 125 22" />
      </g>
      <g stroke="#3CC2B0" strokeWidth={1.4} className="m-fade" style={d(430)}>
        <path d="M214 12L223 17V27L214 32L205 27V17L214 12Z" />
        <path d="M205 17L214 22L223 17M214 22V32" />
      </g>
      <g stroke="#E57AB6" strokeWidth={1.4} className="m-fade" style={d(570)}>
        <rect x="302" y="21" width="16" height="12" rx="1.5" />
        <path d="M305.5 21V17.5C305.5 15.2 307.5 13.6 310 13.6C312.5 13.6 314.5 15.2 314.5 17.5V21" />
      </g>
    </svg>
  )
}

// Full-bleed navy band. Inside it the page column continues as faint dashed
// lines, so the band reads as the same sheet, inverted.
export function WaitlistBand() {
  return (
    <section id="join" className={s.band} aria-labelledby="join-title">
      <InView live className={s.inner}>
        <Signature />
        <h2 id="join-title" className={`${s.title} m-rise`} style={d(250)}>
          Get Pier when it ships
        </h2>
        <p className={`${s.text} m-rise`} style={d(330)}>
          One email when the first release is ready to install on your server. Nothing else.
        </p>
        <div className={`${s.form} m-rise`} style={d(420)}>
          <WaitlistForm tone="dark" />
        </div>
        {/* Webflow DevLink puts the product right under the ask, so the
            waitlist is for something real. Cropped by the band's edge. */}
        <div className={`${s.peek} m-rise`} style={d(600, { '--dur': '1000ms' })} aria-hidden="true">
          <div className={s.window}>
            <div className={s.barWide}>
              <WindowBar />
            </div>
            <div className={s.barCompact}>
              <WindowBar compact />
            </div>
            <div className={s.panels}>
              <AliasPanel kind="production" />
              <div className={s.second}>
                <AliasPanel kind="preview" />
              </div>
            </div>
          </div>
        </div>
      </InView>
    </section>
  )
}
