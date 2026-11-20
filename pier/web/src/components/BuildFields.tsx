import type { ReactNode } from 'react'
import { changePreset, type Build } from '../lib/api'
import { Info } from './Icons'

// BuildFields is the Build panel's inside (D2): what Pier found or will look
// for, then the five things that can override it. A new project and a
// project's settings both show it.
export function BuildFields({ build: b, onChange, plan, problem }: {
  build: Build
  onChange: (b: Build) => void
  plan?: Record<string, string> | null // what the last build ran, once there's been one
  problem?: { field?: string; error?: string }
}) {
  const server = b.preset === 'dockerfile' || b.preset === 'server'
  const set = (k: keyof Build) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    onChange(k === 'preset' ? changePreset(b, e.target.value as Build['preset']) : { ...b, [k]: e.target.value })
  const field = (k: string) => 'field' + (problem?.field === k ? ' error' : '')
  const help = (k: string, text: ReactNode) =>
    problem?.field === k && problem.error ? (
      <span className="field-message" role="alert">
        {problem.error}
      </span>
    ) : (
      <span className="t-note">{text}</span>
    )
  return (
    <>
      <div className="alert info">
        <Info />
        <div>
          {plan ? (
            <>
              <span className="t-small t-strong">The last build used {plan.label}</span>
              <span className="t-caption t-muted">{steps(plan)} Pier works this out again on every deploy. Fill in a field to override it.</span>
            </>
          ) : (
            <>
              <span className="t-small t-strong">Pier works out how to build on every deploy</span>
              <span className="t-caption t-muted">
                It looks in the root directory for a Dockerfile, a server app or static files. Fill in a field to override what it
                finds.
              </span>
            </>
          )}
        </div>
      </div>
      <div className="form-grid">
        <label className={field('preset')}>
          <span className="field-label">Preset</span>
          <select className="input" value={b.preset} onChange={set('preset')}>
            <option value="">Detect it</option>
            <option value="static">Static HTML</option>
            <option value="vite">Vite</option>
            <option value="server">Railpack web server</option>
            <option value="dockerfile">Dockerfile</option>
          </select>
          {help('preset', 'Railpack builds server apps: Next.js, Express, Go, Python and more.')}
        </label>
        <label className={field('root')}>
          <span className="field-label">Root directory</span>
          <input className="input mono" placeholder="./" value={b.root} onChange={set('root')} spellCheck={false} autoCapitalize="off" />
          {help('root', 'Where the app or its Dockerfile is. Change it for a monorepo.')}
        </label>
        <label className={field('output')}>
          <span className="field-label">Output folder</span>
          <input
            className="input mono"
            disabled={server}
            placeholder={server ? 'Not used' : plan?.output || 'dist'}
            value={b.output}
            onChange={set('output')}
            spellCheck={false}
            autoCapitalize="off"
          />
          {help('output', server ? "A server app isn't served from a folder." : 'The folder the build writes. Pier serves it.')}
        </label>
        <label className={field('install')}>
          <span className="field-label">Install command</span>
          <input
            className="input mono"
            disabled={server}
            placeholder={server ? 'Not used' : plan ? plan.install || 'none' : 'npm ci'}
            value={b.install}
            onChange={set('install')}
            spellCheck={false}
            autoCapitalize="off"
          />
          {help('install', server ? 'The image build installs what it needs.' : 'Picked from the lockfile.')}
        </label>
        <label className={field('command')}>
          <span className="field-label">Build command</span>
          <input
            className="input mono"
            disabled={b.preset === 'dockerfile'}
            placeholder={b.preset === 'dockerfile' ? 'Not used' : plan ? plan.build || 'none' : 'npm run build'}
            value={b.command}
            onChange={set('command')}
            spellCheck={false}
            autoCapitalize="off"
          />
          {help('command', b.preset === 'dockerfile' ? 'The Dockerfile says how to build.' : 'The build script in package.json.')}
        </label>
      </div>
    </>
  )
}

function steps(plan: Record<string, string>): string {
  if (plan.runtime) return 'The image supplies the start command.'
  const ran = [plan.install, plan.build].filter(Boolean)
  const where = plan.root && plan.root !== '.' ? ` in ${plan.root}/` : ''
  const serve = `served ${plan.output === '.' ? 'the folder itself' : plan.output + '/'}`
  if (ran.length === 0) return `It ran nothing${where} and ${serve}.`
  return `It ran ${ran.join(', then ')}${where}, and ${serve}.`
}
