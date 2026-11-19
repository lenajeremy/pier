import { useEffect, useRef, useState, type KeyboardEvent } from 'react'
import { motionEnabled, useInView } from '../lib/inView'
import { CopyIcon } from './icons'
import s from './PushLog.module.css'

// "How it works", after Firecrawl's CLI section: the steps as a list on the
// left, one terminal on the right. The terminal is a single, real-looking
// git push; picking a step lights up the lines that step produced.

type Part = 'git' | 'docker' | 'pierd' | 'caddy'

const steps: { title: string; part: Part; text: string }[] = [
  { title: 'Push', part: 'git', text: 'Add Pier as a git remote once, then push any branch the way you already do.' },
  {
    title: 'Build',
    part: 'docker',
    text: 'A throwaway Docker container builds your site. Files in api/ become functions, and a root Dockerfile becomes a service.',
  },
  { title: 'Deploy', part: 'pierd', text: 'The result is saved as a deployment that never changes, with its own permanent URL.' },
  {
    title: 'Point',
    part: 'caddy',
    text: "Caddy points the branch's preview domain, or your production domain, at the new deployment, with HTTPS.",
  },
]

// [step, tag, text]. A tag is the word after "remote:", coloured by the part
// that printed it.
type Line = { step: number; tag?: string; text: string; prompt?: boolean; url?: string }
const log: Line[] = [
  { step: 0, prompt: true, text: 'git push pier feat-search' },
  { step: 0, text: 'Enumerating objects: 42, done.' },
  { step: 0, text: 'Writing objects: 100% (24/24), 8.31 KiB, done.' },
  { step: 0, tag: 'pier', text: 'received feat-search at c19e77' },
  { step: 1, tag: 'build', text: 'astro 4.16, found in package.json' },
  { step: 1, tag: 'build', text: 'npm ci, 214 packages in 6.1s' },
  { step: 1, tag: 'build', text: 'astro build, 214 files, 1.3 MB' },
  { step: 1, tag: 'build', text: 'api/og.go is a function' },
  { step: 2, tag: 'deploy', text: 'saved c19e77, it will never change' },
  { step: 2, tag: 'deploy', text: '', url: 'https://blog-c19e77.pier.example.com' },
  { step: 3, tag: 'alias', text: 'feat-search--blog.pier.example.com now serves c19e77' },
  { step: 3, tag: 'https', text: 'certificate ready' },
  { step: 3, tag: 'pier', text: 'preview ready in 11.2s' },
  { step: 3, text: 'To pier.example.com:blog' },
  { step: 3, text: ' * [new branch]      feat-search -> feat-search' },
]

const ADVANCE_MS = 4200

export function PushLog() {
  const [active, setActive] = useState(0)
  const [picked, setPicked] = useState(false)
  const [copied, setCopied] = useState(false)
  const { ref, visible } = useInView<HTMLDivElement>({ live: true })
  const tabs = useRef<(HTMLButtonElement | null)[]>([])

  // Walk through the steps on its own while on screen, until someone picks one.
  useEffect(() => {
    if (picked || !visible || !motionEnabled()) return
    const t = window.setTimeout(() => setActive((a) => (a + 1) % steps.length), ADVANCE_MS)
    return () => window.clearTimeout(t)
  }, [active, picked, visible])

  function pick(i: number) {
    setActive(i)
    setPicked(true)
  }

  function onKey(e: KeyboardEvent, i: number) {
    const next = e.key === 'ArrowDown' || e.key === 'ArrowRight' ? 1 : e.key === 'ArrowUp' || e.key === 'ArrowLeft' ? -1 : 0
    if (!next) return
    e.preventDefault()
    const j = (i + next + steps.length) % steps.length
    pick(j)
    tabs.current[j]?.focus()
  }

  async function copy() {
    try {
      await navigator.clipboard.writeText('git push pier feat-search')
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1600)
    } catch {
      // Clipboard unavailable (insecure context or denied); the text is selectable anyway.
    }
  }

  return (
    <div ref={ref} className={s.frame} data-in={visible ? '' : undefined}>
      <div className={s.tabs} role="tablist" aria-label="Steps" aria-orientation="vertical">
        {steps.map((step, i) => {
          const on = active === i
          return (
            <button
              key={step.title}
              ref={(el) => {
                tabs.current[i] = el
              }}
              type="button"
              role="tab"
              id={`push-step-${i}`}
              aria-selected={on}
              aria-controls="push-log"
              tabIndex={on ? 0 : -1}
              className={`${s.tab} ${on ? `${s.tabOn} ${s[`edge_${step.part}`]}` : ''}`}
              onClick={() => pick(i)}
              onKeyDown={(e) => onKey(e, i)}
            >
              <span className={s.tabHead}>
                <span className={`${s.num} ${s[step.part]}`} aria-hidden="true">
                  {i + 1}
                </span>
                <span className={s.tabTitle}>{step.title}</span>
              </span>
              <span className={s.tabText}>{step.text}</span>
              {on && !picked && <span key={active} className={`${s.progress} ${s[`bar_${step.part}`]}`} aria-hidden="true" />}
            </button>
          )
        })}
      </div>

      <div className={s.terminal} id="push-log" role="tabpanel" aria-labelledby={`push-step-${active}`}>
        <div className={s.bar}>
          <span className={s.dots} aria-hidden="true">
            <span />
            <span />
            <span />
          </span>
          <span className={s.barTitle}>Terminal</span>
          <button type="button" className={s.copy} onClick={copy}>
            <CopyIcon size={14} />
            {copied ? 'Copied' : 'Copy'}
          </button>
        </div>
        <pre className={s.log}>
          <code>
            {log.map((l, i) => (
              <span key={i} className={`${s.line} ${l.step === active ? s.lit : ''}`}>
                {l.prompt && <span className={s.prompt}>$ </span>}
                {l.tag && (
                  <>
                    <span className={s.remote}>remote: </span>
                    <span className={s[`tag_${steps[l.step].part}`]}>{l.tag.padEnd(7, ' ')}</span>
                  </>
                )}
                {l.text}
                {l.url && <span className={s.url}>{l.url}</span>}
                {'\n'}
              </span>
            ))}
          </code>
        </pre>
      </div>

      {/* The three addresses every push ends up with. */}
      <dl className={s.urls}>
        <div className={s.urlCell}>
          <dt>This deployment, never changes</dt>
          <dd>blog-c19e77.pier.example.com</dd>
        </div>
        <div className={s.urlCell}>
          <dt>The branch preview, follows feat-search</dt>
          <dd>feat-search--blog.pier.example.com</dd>
        </div>
        <div className={s.urlCell}>
          <dt>Production, follows main</dt>
          <dd>blog.pier.example.com</dd>
        </div>
      </dl>
    </div>
  )
}
