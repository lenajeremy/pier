import type { CSSProperties, ReactNode } from 'react'
import { d } from '../lib/inView'
import { Arrowhead, c, Halo, Handles, Label, T } from './parts'
import sv from './server.module.css'

const TITLE =
  'Visitors reach Caddy over HTTPS on your server. Caddy routes requests to Docker, where builds, functions and services run. pierd, with its HTTP API, SQLite and dashboard, starts containers and configures Caddy. From your laptop, git push and the pier CLI talk to pierd.'

export type ServerPart = 'visitors' | 'caddy' | 'pierd' | 'docker' | 'laptop'

const loop = { '--cycle': '6s', '--start': '2s' } as CSSProperties

// One part of the diagram: steps back when another part has focus, and pops
// in on entrance (on an inner group, so the entrance's final opacity doesn't
// override the dimming).
function Part({ name, focus, delay, children }: { name: ServerPart; focus?: ServerPart; delay: number; children: ReactNode }) {
  return (
    <g className={`${sv.part} ${focus && focus !== name ? sv.dim : ''}`}>
      <g className="m-pop" style={d(delay)}>
        {children}
      </g>
    </g>
  )
}

function Sub({ x, y, w, h, color, fill, title, note }: { x: number; y: number; w: number; h: number; color: string; fill: string; title: string; note?: string }) {
  return (
    <g>
      <rect x={x} y={y} width={w} height={h} fill={fill} stroke={color} strokeDasharray="3 3" />
      <T x={x + 12} y={note ? y + 21 : y + 22} fill={c.navy900}>
        {title}
      </T>
      {note && (
        <T x={x + 12} y={y + 39} size={11.5}>
          {note}
        </T>
      )}
    </g>
  )
}

function Packet({ cx, cy, color, className }: { cx: number; cy: number; color: string; className: string }) {
  return <circle className={`${sv.packet} ${className} m-loop`} cx={cx} cy={cy} r={4.5} fill={color} stroke={c.white} strokeWidth={2} />
}

// "Small enough to understand", wide layout (board: Server diagram).
export function ServerDiagramWide({ className, focus }: { className?: string; focus?: ServerPart }) {
  return (
    <svg className={className} style={loop} viewBox="6 6 588 488" role="img" aria-labelledby="server-wide-title">
      <title id="server-wide-title">{TITLE}</title>
      <defs>
        <Arrowhead id="server-wide-arrow" color={c.navy400} />
      </defs>
      <g className="m-fade">
        <rect x="160" y="36" width="410" height="430" fill={c.white} fillOpacity={0.65} stroke={c.navy400} strokeDasharray="6 5" />
        <T x={176} y={60} size={14} weight={500} fill={c.navy900}>
          Your server
        </T>
        <T x={176} y={78} size={12}>
          one Linux box with Docker
        </T>
      </g>

      <g className="m-fade" style={d(800)}>
        <g fill="none" stroke={c.navy400} strokeDasharray="4 4" markerEnd="url(#server-wide-arrow)">
          <path d="M132 130H384" />
          <path d="M290 222V146H384" />
          <path d="M340 300H384" />
          <path d="M470 158V240" />
          <path d="M132 318H184" />
        </g>
        <T x={196} y={122} size={11} mono>
          GET blog.example.com
        </T>
        <T x={284} y={188} size={11} anchor="end">
          admin API, :2019
        </T>
        <T x={462} y={196} size={11} mono anchor="end">
          /api/* to functions
        </T>
        <T x={462} y={212} size={11} mono anchor="end">
          / to static files
        </T>
      </g>

      <Packet cx={132} cy={130} color={c.caddy} className={sv.reqWide} />
      <Packet cx={132} cy={318} color={c.git} className={sv.depWide} />

      <Part name="visitors" focus={focus} delay={100}>
        <rect x="22" y="110" width="110" height="40" rx="20" fill={c.white} stroke={c.navy300} />
        <T x={77} y={135} fill={c.navy900} anchor="middle">
          Visitors
        </T>
      </Part>

      <Part name="caddy" focus={focus} delay={250}>
        <Label x={390} y={98} size={13} fill={c.caddyInk}>
          caddy
        </Label>
        <rect x="390" y="106" width="160" height="52" fill={c.caddyTint} stroke={c.caddy} strokeWidth={1.2} />
        <T x={404} y={137} size={11.5} fill={c.navy900} mono>
          *.pier.example.com
        </T>
        <Handles x={390} y={106} w={160} h={52} color={c.caddy} />
      </Part>

      <Part name="pierd" focus={focus} delay={400}>
        <Label x={190} y={218} size={13} fill={c.pierdInk}>
          pierd
        </Label>
        <rect x="190" y="226" width="150" height="150" fill={c.pierdTint} stroke={c.pierd} strokeWidth={1.2} />
        <Sub x={203} y={239} w={124} h={34} color={c.pierd} fill={c.pierdSoft} title="HTTP API" />
        <Sub x={203} y={284} w={124} h={34} color={c.pierd} fill={c.pierdSoft} title="SQLite" />
        <Sub x={203} y={329} w={124} h={34} color={c.pierd} fill={c.pierdSoft} title="Dashboard" />
        <Handles x={190} y={226} w={150} h={150} color={c.pierd} />
      </Part>

      <Part name="docker" focus={focus} delay={550}>
        <Label x={390} y={238} size={13} fill={c.dockerInk}>
          docker
        </Label>
        <rect x="390" y="246" width="160" height="196" fill={c.dockerTint} stroke={c.docker} strokeWidth={1.2} />
        <Sub x={403} y={259} w={134} h={50} color={c.docker} fill={c.dockerSoft} title="Builds" note="thrown away after" />
        <Sub x={403} y={319} w={134} h={50} color={c.docker} fill={c.dockerSoft} title="Functions" note="asleep until called" />
        <Sub x={403} y={379} w={134} h={50} color={c.docker} fill={c.dockerSoft} title="Services" note="health-checked" />
        <Handles x={390} y={246} w={160} h={196} color={c.docker} />
      </Part>

      <Part name="laptop" focus={focus} delay={700}>
        <Label x={22} y={262} size={13} fill={c.gitInk}>
          your laptop
        </Label>
        <rect x="22" y="270" width="110" height="96" fill={c.gitTint} stroke={c.git} strokeWidth={1.2} />
        <rect x="34" y="282" width="86" height="30" fill={c.gitSoft} stroke={c.git} strokeDasharray="3 3" />
        <T x={44} y={302} size={12} fill={c.navy900} mono>
          git push
        </T>
        <rect x="34" y="322" width="86" height="30" fill={c.gitSoft} stroke={c.git} strokeDasharray="3 3" />
        <T x={44} y={342} size={12} fill={c.navy900} mono>
          pier
        </T>
      </Part>

      <Halo x={384} y={100} w={172} h={64} color={c.caddy} t={1.8} />
      <Halo x={384} y={240} w={172} h={208} color={c.docker} t={3.3} />
      <Halo x={184} y={220} w={162} h={162} color={c.pierd} t={3.6} />
      <Halo x={384} y={240} w={172} h={208} color={c.docker} t={4.44} />
    </svg>
  )
}

// Phone layout: visitors at the top, your laptop at the bottom.
export function ServerDiagramTall({ className, focus }: { className?: string; focus?: ServerPart }) {
  return (
    <svg className={className} style={loop} viewBox="0 0 390 600" role="img" aria-labelledby="server-tall-title">
      <title id="server-tall-title">{TITLE}</title>
      <defs>
        <Arrowhead id="server-tall-arrow" color={c.navy400} />
      </defs>
      <g className="m-fade">
        <rect x="20" y="82" width="350" height="420" fill={c.white} fillOpacity={0.65} stroke={c.navy400} strokeDasharray="6 5" />
        <T x={34} y={104} weight={500} fill={c.navy900}>
          Your server
        </T>
      </g>

      <g className="m-fade" style={d(800)}>
        <g fill="none" stroke={c.navy400} strokeDasharray="4 4" markerEnd="url(#server-tall-arrow)">
          <path d="M195 62V128" />
          <path d="M195 180V222" />
          <path d="M195 400V366" />
          <path d="M290 446H330V158H296" />
          <path d="M195 556V488" />
        </g>
        <T x={203} y={96} size={11} mono>
          GET blog.example.com
        </T>
        <T x={203} y={206} size={11} mono>
          /api/* to functions
        </T>
        <T x={203} y={388} size={12}>
          starts containers
        </T>
        <T x={336} y={300} size={12} transform="rotate(90 336 300)">
          admin API
        </T>
      </g>

      <Packet cx={195} cy={62} color={c.caddy} className={sv.reqTall} />
      <Packet cx={195} cy={554} color={c.git} className={sv.depTall} />

      <Part name="visitors" focus={focus} delay={100}>
        <rect x="140" y="24" width="110" height="38" rx="19" fill={c.white} stroke={c.navy300} />
        <T x={195} y={48} fill={c.navy900} anchor="middle">
          Visitors
        </T>
      </Part>

      <Part name="caddy" focus={focus} delay={250}>
        <Label x={100} y={128} size={13} fill={c.caddyInk}>
          caddy
        </Label>
        <rect x="100" y="136" width="190" height="44" fill={c.caddyTint} stroke={c.caddy} strokeWidth={1.2} />
        <T x={195} y={163} size={12} fill={c.navy900} mono anchor="middle">
          *.pier.example.com
        </T>
      </Part>

      <Part name="docker" focus={focus} delay={400}>
        <Label x={66} y={222} size={13} fill={c.dockerInk}>
          docker
        </Label>
        <rect x="66" y="230" width="258" height="130" fill={c.dockerTint} stroke={c.docker} strokeWidth={1.2} />
        <Sub x={78} y={242} w={234} h={32} color={c.docker} fill={c.dockerSoft} title="Builds, thrown away after" />
        <Sub x={78} y={280} w={234} h={32} color={c.docker} fill={c.dockerSoft} title="Functions, asleep until called" />
        <Sub x={78} y={318} w={234} h={32} color={c.docker} fill={c.dockerSoft} title="Services, health-checked" />
      </Part>

      <Part name="pierd" focus={focus} delay={550}>
        <Label x={100} y={402} size={13} fill={c.pierdInk}>
          pierd
        </Label>
        <rect x="100" y="410" width="190" height="72" fill={c.pierdTint} stroke={c.pierd} strokeWidth={1.2} />
        <T x={114} y={436} fill={c.navy900}>
          HTTP API and dashboard
        </T>
        <T x={114} y={458} fill={c.navy900}>
          SQLite
        </T>
        <Handles x={100} y={410} w={190} h={72} color={c.pierd} />
      </Part>

      <Part name="laptop" focus={focus} delay={700}>
        <Label x={100} y={530} size={13} fill={c.gitInk}>
          your laptop
        </Label>
        <rect x="100" y="538" width="190" height="40" fill={c.gitTint} stroke={c.git} strokeWidth={1.2} />
        <T x={195} y={563} size={12} fill={c.navy900} mono anchor="middle">
          git push, pier
        </T>
      </Part>

      <Halo x={94} y={130} w={202} h={56} color={c.caddy} t={1.32} />
      <Halo x={60} y={224} w={270} h={142} color={c.docker} t={2.4} />
      <Halo x={94} y={404} w={202} h={84} color={c.pierd} t={3.72} />
      <Halo x={60} y={224} w={270} h={142} color={c.docker} t={4.56} />
    </svg>
  )
}
