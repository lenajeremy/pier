import type { CSSProperties } from 'react'
import { d } from '../lib/inView'
import f from './flow.module.css'
import { Arrowhead, c, Halo, Handles, Label, T } from './parts'

const TITLE =
  'Pushes to main and feat-search go to pierd, which builds them. Docker runs the static files, functions and services, and Caddy gets the certificate and routes feat-search--blog.pier.example.com to the new build.'

const braces =
  'M271 162C267 162 267 165 267 168V169C267 170.5 266 171.5 264.5 171.5C266 171.5 267 172.5 267 174V175C267 178 267 181 271 181M281 162C285 162 285 165 285 168V169C285 170.5 286 171.5 287.5 171.5C286 171.5 285 172.5 285 174V175C285 178 285 181 281 181'

// Loop timing: a 5s cycle, starting after the entrance; the packet takes 3s
// to cross, and --t is when it reaches each part.
const loop = (travel: number) => ({ '--cycle': '5s', '--start': '1.9s', '--travel': `${travel}px` }) as CSSProperties
function BranchIcon({ x, y }: { x: number; y: number }) {
  // A 20px git branch glyph, top-left at (x, y).
  return (
    <g fill="none" stroke={c.git} strokeWidth={1.5} transform={`translate(${x} ${y})`}>
      <circle cx="4" cy="3" r="2.6" />
      <circle cx="4" cy="19" r="2.6" />
      <circle cx="16" cy="7" r="2.6" />
      <path d="M4 5.6V16.4M16 9.6C16 15 4 13 4 16.4" />
    </g>
  )
}

function Source({ x, label, delay }: { x: number; label: string; delay: number }) {
  return (
    <g className="m-pop" style={d(delay)}>
      <T x={x} y={88} size={12} weight={500} fill={c.gitInk} mono>
        {label}
      </T>
      <rect x={x} y={96} width="56" height="56" fill={c.gitTint} stroke={c.git} strokeWidth={1.2} />
      <rect x={x + 9} y={105} width="38" height="38" fill={c.gitSoft} stroke={c.git} strokeDasharray="3 3" />
      <BranchIcon x={x + 18} y={113} />
      <Handles x={x} y={96} w={56} h={56} color={c.git} />
    </g>
  )
}

// Hero diagram, wide layout, after Cloudflare Workers' system canvas: two
// sources on the left (their "User A / User B"), a route box, a fan of
// crossing connectors between two parts, and a browser window running off
// the edge. The canvas background and handles are drawn by <Canvas>.
export function HeroFlowWide({ className }: { className?: string }) {
  return (
    <svg className={className} style={loop(740)} viewBox="6 6 1148 336" role="img" aria-labelledby="hero-wide-title">
      <title id="hero-wide-title">{TITLE}</title>

      <g className="m-wipe" style={d(150, { '--dur': '1100ms' })} fill="none" stroke={c.navy300} strokeWidth={1.2} strokeDasharray="4 4">
        {/* Two pushes merge into pierd. */}
        <path d="M68 152C68 204 150 200 244 200" />
        <path d="M152 152C152 188 196 200 244 200" />
        <path d="M338 200H395" />
        {/* The build fans out to the three kinds of container. */}
        <path d="M595 200C640 200 640 145 684 145" />
        <path d="M595 200H684" />
        <path d="M595 200C640 200 640 257 684 257" />
        {/* Every container can be reached through both halves of Caddy. */}
        <path d="M758 145C798 145 798 171 838 171" />
        <path d="M758 145C800 145 796 229 838 229" />
        <path d="M758 201C798 201 798 171 838 171" />
        <path d="M758 201C798 201 798 229 838 229" />
        <path d="M758 257C800 257 796 171 838 171" />
        <path d="M758 257C798 257 798 229 838 229" />
        <path d="M926 200H990" />
        <circle cx="400" cy="200" r="4.5" fill={c.white} stroke={c.navy400} strokeDasharray="none" />
        <circle cx="590" cy="200" r="4.5" fill={c.white} stroke={c.navy400} strokeDasharray="none" />
        <circle cx="244" cy="200" r="3.5" fill={c.white} stroke={c.navy400} strokeDasharray="none" />
      </g>

      {/* The build packet runs underneath the nodes, so it appears to pass through them. */}
      <circle className={`${f.packet} ${f.packetX} m-loop`} cx="250" cy="200" r="5" fill={c.pierd} stroke={c.white} strokeWidth={2} />

      <Source x={40} label="main" delay={80} />
      <Source x={124} label="feat-search" delay={160} />

      <g className="m-pop" style={d(280)}>
        <g transform="translate(18 0)">
          <Label x={232} y={122} fill={c.pierdInk}>pierd</Label>
          <rect x="232" y="132" width="88" height="136" fill={c.pierdTint} stroke={c.pierd} strokeWidth={1.2} />
          <rect x="246" y="146" width="60" height="50" fill={c.pierdSoft} stroke={c.pierd} strokeDasharray="3 3" />
          <rect x="246" y="204" width="60" height="50" fill={c.pierdSoft} stroke={c.pierd} strokeDasharray="3 3" />
          <g fill="none" stroke={c.pierd} strokeWidth={1.5}>
            <path d={braces} />
            <ellipse cx="276" cy="219" rx="9" ry="3.5" />
            <path d="M267 219V237C267 239 271 240.5 276 240.5C281 240.5 285 239 285 237V219M267 228C267 230 271 231.5 276 231.5C281 231.5 285 230 285 228" />
          </g>
          <Handles x={232} y={132} w={88} h={136} color={c.pierd} />
          <T x={232} y={294}>API, SQLite</T>
        </g>
      </g>

      <g className="m-pop" style={d(460)}>
        <rect x="400" y="148" width="190" height="104" rx="14" fill={c.white} fillOpacity={0.6} stroke={c.navy400} strokeDasharray="5 4" />
      </g>
      <g className="m-type" style={d(640, { '--steps': 14, '--dur': '420ms' })}>
        <T x={422} y={182} size={14}>build</T>
        <T x={484} y={182} size={14} fill={c.navy900} mono>astro 4.16</T>
      </g>
      <g className="m-type" style={d(820, { '--steps': 14, '--dur': '420ms' })}>
        <T x={422} y={206} size={14}>bundle</T>
        <T x={484} y={206} size={14} fill={c.navy900} mono>api/*.ts</T>
      </g>
      <g className="m-type" style={d(1000, { '--steps': 14, '--dur': '420ms' })}>
        <T x={422} y={230} size={14}>keep</T>
        <T x={484} y={230} size={14} fill={c.navy900} mono>c19e77</T>
      </g>

      <g className="m-pop" style={d(640)}>
        <Label x={670} y={98} fill={c.dockerInk}>docker</Label>
        <rect x="670" y="108" width="88" height="186" fill={c.dockerTint} stroke={c.docker} strokeWidth={1.2} />
        <rect x="684" y="122" width="60" height="46" fill={c.dockerSoft} stroke={c.docker} strokeDasharray="3 3" />
        <rect x="684" y="178" width="60" height="46" fill={c.dockerSoft} stroke={c.docker} strokeDasharray="3 3" />
        <rect x="684" y="234" width="60" height="46" fill={c.dockerSoft} stroke={c.docker} strokeDasharray="3 3" />
        <g fill="none" stroke={c.docker} strokeWidth={1.5}>
          <rect x="703" y="136" width="22" height="18" rx="2" />
          <path d="M703 141H725" />
          <path d="M710 192C707 192 707 194 707 196.5V197.5C707 199 706 200 704.5 200C706 200 707 201 707 202.5V203.5C707 206 707 208 710 208M718 192C721 192 721 194 721 196.5V197.5C721 199 722 200 723.5 200C722 200 721 201 721 202.5V203.5C721 206 721 208 718 208" />
          <path d="M714 246L724 251.5V262.5L714 268L704 262.5V251.5L714 246Z" />
          <path d="M704 251.5L714 257L724 251.5M714 257V268" />
        </g>
        <Handles x={670} y={108} w={88} h={186} color={c.docker} />
        <T x={670} y={320}>static, api, service</T>
      </g>

      {/* Caddy in two halves: certificates on top, routing below. */}
      <g className="m-pop" style={d(820)}>
        <Label x={838} y={122} fill={c.caddyInk}>caddy</Label>
        <rect x="838" y="132" width="88" height="136" fill={c.caddyTint} stroke={c.caddy} strokeWidth={1.2} />
        <rect x="852" y="146" width="60" height="50" fill={c.caddySoft} stroke={c.caddy} strokeDasharray="3 3" />
        <rect x="852" y="204" width="60" height="50" fill={c.caddySoft} stroke={c.caddy} strokeDasharray="3 3" />
        <g fill="none" stroke={c.caddy} strokeWidth={1.5}>
          <rect x="872" y="170" width="20" height="15" rx="2" />
          <path d="M876 170V165.5C876 162.5 878.5 160.5 882 160.5C885.5 160.5 888 162.5 888 165.5V170" />
          <circle cx="882" cy="229" r="10" />
          <path d="M872 229H892M882 219C878 222.5 878 235.5 882 239M882 219C886 222.5 886 235.5 882 239" />
        </g>
        <Handles x={838} y={132} w={88} h={136} color={c.caddy} />
        <T x={838} y={294}>HTTPS, routing</T>
      </g>

      {/* The preview, running off the canvas edge; its page "loads" in. */}
      <g className="m-rise" style={d(1000)}>
        <rect x="990" y="104" width="240" height="210" rx="10" fill={c.white} stroke={c.windowLine} />
        <path d="M990 138H1230" stroke={c.navy100} />
        <g fill="none" stroke={c.navy200}>
          <circle cx="1006" cy="121" r="3.5" />
          <circle cx="1018" cy="121" r="3.5" />
          <circle cx="1030" cy="121" r="3.5" />
        </g>
        <rect x="1044" y="112" width="180" height="18" rx="9" fill={c.navy50} />
        <T x={1054} y={125} size={9} fill={c.navy900} mono>feat-search--blog.pier.example.com</T>
      </g>
      {/* The page itself: the blog's new search results. */}
      <g className="m-fade" style={d(1250)}>
        <T x={1008} y={160} size={10} weight={600} fill={c.navy900}>blog</T>
        <rect x="1060" y="153" width="26" height="6" rx="3" fill="#E6EAF2" />
        <rect x="1094" y="153" width="26" height="6" rx="3" fill="#E6EAF2" />
        <T x={1008} y={188} size={15} weight={500} fill={c.navy900}>Search</T>
      </g>
      <g className="m-grow" style={d(1350)}>
        <rect x="1008" y="198" width="146" height="22" rx="11" fill={c.white} stroke={c.navy200} />
        <T x={1020} y={213} size={10} fill={c.navy900}>rss</T>
      </g>
      <g className="m-rise" style={d(1450)}>
        <T x={1008} y={242} size={10} weight={500} fill={c.navy900}>Fix RSS dates</T>
        <rect x="1008" y="248" width="110" height="5" rx="2.5" fill="#EEF1F6" />
      </g>
      <g className="m-rise" style={d(1530)}>
        <T x={1008} y={270} size={10} weight={500} fill={c.navy900}>RSS feeds on a static site</T>
        <rect x="1008" y="276" width="124" height="5" rx="2.5" fill="#EEF1F6" />
      </g>
      <g className="m-rise" style={d(1610)}>
        <T x={1008} y={298} size={10} weight={500} fill={c.navy900}>Use UTC for feed dates</T>
        <rect x="1008" y="304" width="96" height="5" rx="2.5" fill="#EEF1F6" />
      </g>

      <Halo x={34} y={90} w={68} h={68} color={c.git} t={0} />
      <Halo x={118} y={90} w={68} h={68} color={c.git} t={0} />
      <Halo x={244} y={126} w={100} h={148} color={c.pierd} t={0.18} />
      <Halo x={395} y={143} w={200} h={114} rx={17} color={c.navy400} t={1.01} />
      <Halo x={664} y={102} w={100} h={198} color={c.docker} t={1.88} />
      <Halo x={832} y={126} w={100} h={148} color={c.caddy} t={2.56} />
      <Halo x={1040} y={108} w={188} h={26} rx={13} color={c.pierd} t={3} />
    </svg>
  )
}

// Phone layout: the same flow, top to bottom.
export function HeroFlowTall({ className }: { className?: string }) {
  return (
    <svg className={className} style={loop(472)} viewBox="0 0 390 664" role="img" aria-labelledby="hero-tall-title">
      <title id="hero-tall-title">{TITLE}</title>
      <defs>
        <Arrowhead id="hero-tall-arrow" color={c.navy400} />
      </defs>

      <g className="m-wipe-down" style={d(150, { '--dur': '1100ms' })}>
        <g fill="none" stroke={c.navy300} strokeWidth={1.2} strokeDasharray="4 4" markerEnd="url(#hero-tall-arrow)">
          <path d="M68 128V166" />
          <path d="M68 278V316" />
          <path d="M68 398V436" />
          <path d="M68 518V546" />
        </g>
        <g fill={c.white} stroke={c.navy400} strokeWidth={1.2}>
          <circle cx="68" cy="124" r="4" />
          <circle cx="68" cy="274" r="4" />
          <circle cx="68" cy="394" r="4" />
          <circle cx="68" cy="514" r="4" />
        </g>
      </g>

      <circle className={`${f.packet} ${f.packetY} m-loop`} cx="68" cy="80" r="5" fill={c.pierd} stroke={c.white} strokeWidth={2} />

      <g className="m-rise" style={d(100)}>
        <Label x={36} y={38} fill={c.gitInk}>git push</Label>
        <rect x="36" y="48" width="64" height="64" fill={c.gitTint} stroke={c.git} strokeWidth={1.2} />
        <rect x="46" y="58" width="44" height="44" fill={c.gitSoft} stroke={c.git} strokeDasharray="3 3" />
        <g fill="none" stroke={c.git} strokeWidth={1.5}>
          <circle cx="62" cy="71" r="2.6" />
          <circle cx="62" cy="89" r="2.6" />
          <circle cx="75" cy="75" r="2.6" />
          <path d="M62 73.6V86.4M75 77.6C75 84 62 82 62 86.4" />
        </g>
        <T x={120} y={74} fill={c.navy900} mono>feat-search</T>
        <T x={120} y={94}>a branch you just pushed</T>
      </g>

      <g className="m-rise" style={d(300)}>
        <Label x={36} y={188} fill={c.pierdInk}>pierd</Label>
        <rect x="36" y="198" width="64" height="64" fill={c.pierdTint} stroke={c.pierd} strokeWidth={1.2} />
        <rect x="46" y="208" width="44" height="44" fill={c.pierdSoft} stroke={c.pierd} strokeDasharray="3 3" />
        <g fill="none" stroke={c.pierd} strokeWidth={1.5}>
          <ellipse cx="68" cy="221" rx="8" ry="3" />
          <path d="M60 221V237C60 239 63.5 240.5 68 240.5C72.5 240.5 76 239 76 237V221M60 229C60 231 63.5 232.5 68 232.5C72.5 232.5 76 231 76 229" />
        </g>
        <Handles x={36} y={198} w={64} h={64} color={c.pierd} />
      </g>
      <g className="m-type" style={d(480, { '--steps': 12, '--dur': '360ms' })}>
        <T x={120} y={216}>builds</T>
        <T x={174} y={216} fill={c.navy900} mono>astro 4.16</T>
      </g>
      <g className="m-type" style={d(620, { '--steps': 12, '--dur': '360ms' })}>
        <T x={120} y={236}>bundles</T>
        <T x={174} y={236} fill={c.navy900} mono>api/*.ts</T>
      </g>
      <g className="m-type" style={d(760, { '--steps': 12, '--dur': '360ms' })}>
        <T x={120} y={256}>keeps</T>
        <T x={174} y={256} fill={c.navy900} mono>c19e77</T>
      </g>

      <g className="m-rise" style={d(500)}>
        <Label x={36} y={338} fill={c.dockerInk}>docker</Label>
        <rect x="36" y="348" width="64" height="40" fill={c.dockerTint} stroke={c.docker} strokeWidth={1.2} />
        <g fill="none" stroke={c.docker} strokeWidth={1.4}>
          <path d="M68 357L75 361V369L68 373L61 369V361L68 357Z" />
          <path d="M61 361L68 365L75 361M68 365V373" />
        </g>
        <T x={120} y={366}>runs static files, api and</T>
        <T x={120} y={384}>services in containers</T>
      </g>

      <g className="m-rise" style={d(700)}>
        <Label x={36} y={458} fill={c.caddyInk}>caddy</Label>
        <rect x="36" y="468" width="64" height="40" fill={c.caddyTint} stroke={c.caddy} strokeWidth={1.2} />
        <g fill="none" stroke={c.caddy} strokeWidth={1.4}>
          <rect x="61" y="485" width="14" height="10" rx="1.5" />
          <path d="M63.8 485V482C63.8 480 65.7 478.6 68 478.6C70.3 478.6 72.2 480 72.2 482V485" />
        </g>
        <T x={120} y={486}>gets the certificate and</T>
        <T x={120} y={504}>routes the hostname</T>
      </g>

      <g className="m-rise" style={d(900)}>
        <rect x="20" y="552" width="350" height="90" rx="10" fill={c.white} stroke={c.windowLine} />
        <path d="M20 584H370" stroke={c.navy100} />
        <g fill="none" stroke={c.navy200}>
          <circle cx="36" cy="568" r="3.5" />
          <circle cx="48" cy="568" r="3.5" />
          <circle cx="60" cy="568" r="3.5" />
        </g>
        <rect x="74" y="558" width="284" height="20" rx="10" fill={c.navy50} />
        <T x={216} y={572} size={11} fill={c.navy900} mono anchor="middle">feat-search--blog.pier.example.com</T>
      </g>
      <rect className="m-grow" style={d(1150)} x="38" y="600" width="90" height="9" rx="3" fill="#E6EAF2" />
      <rect className="m-grow" style={d(1230)} x="38" y="618" width="160" height="14" rx="4" fill="#DCE2EE" />

      <Halo x={30} y={42} w={76} h={76} color={c.git} t={0} />
      <Halo x={30} y={192} w={76} h={76} color={c.pierd} t={0.95} />
      <Halo x={30} y={342} w={76} h={52} color={c.docker} t={1.83} />
      <Halo x={30} y={462} w={76} h={52} color={c.caddy} t={2.59} />
      <Halo x={70} y={554} w={292} h={28} rx={14} color={c.pierd} t={3} />
    </svg>
  )
}
