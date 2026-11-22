import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import { useData } from '../lib/data'
import { confirm } from './Dialog'
import { Copy, Visit } from './Icons'
import { Spinner } from './Spinner'
import { copy, toast } from './Toast'

type Status = { state: 'started' | 'failed' | 'done'; version?: string; step?: string; error?: string; at: string }
type Info = {
  running: string
  latest: string
  newer: boolean
  notes?: string
  helper: boolean // the root helper that applies an update is installed
  command: string // the installer, which installs it
  pending: boolean // asked for, and the helper hasn't picked it up yet
  status: Status | null
}

export type Update = ReturnType<typeof usePierdUpdate>

// usePierdUpdate is the owner's view of updating pierd: the release out,
// asking for it, and following the update through pierd's restart until
// the new pierd answers. Everyone else gets nothing to do.
export function usePierdUpdate(owner: boolean) {
  const info = useData<Info>(owner ? '/api/server/upgrade' : null, () => true, (d) =>
    d.pending || d.status?.state === 'started' ? 2000 : 60_000,
  )
  const [target, setTarget] = useState('') // the release it's updating to
  const [phase, setPhase] = useState<'idle' | 'updating' | 'done' | 'failed'>('idle')
  const [step, setStep] = useState('')
  const [failure, setFailure] = useState('')

  // A page opened while an update is under way follows it too.
  const d = info.data
  useEffect(() => {
    if (!d || phase !== 'idle') return
    if (d.pending || d.status?.state === 'started') {
      setTarget(d.status?.version || d.latest)
      setPhase('updating')
    } else if (d.status?.state === 'failed' && d.status.version && d.status.version !== d.running) {
      setTarget(d.status.version)
      setFailure(d.status.error ?? '')
      setPhase('failed')
    }
  }, [d, phase])

  // While it updates, pierd goes away and comes back as the new release.
  // Failed requests in between are the restart, not a problem.
  useEffect(() => {
    if (phase !== 'updating' || !target) return
    let stopped = false
    const tick = async () => {
      try {
        const v = await api<{ version: string }>('/api/version')
        if (!stopped && v.version === target) {
          setPhase('done')
          window.dispatchEvent(new Event('pier:refresh-session'))
          toast(`pierd is now ${target}`)
          info.reload()
          return
        }
      } catch {
        // restarting
      }
      try {
        const u = await api<Info>('/api/server/upgrade')
        if (stopped) return
        if (u.status?.state === 'failed' && u.status.version === target) {
          setFailure(u.status.error ?? '')
          setPhase('failed')
          return
        }
        if (u.status?.step) setStep(u.status.step)
      } catch {
        // restarting
      }
    }
    const t = window.setInterval(tick, 1500)
    tick()
    return () => {
      stopped = true
      window.clearInterval(t)
    }
  }, [phase, target])

  const start = (latest: string) =>
    confirm({
      title: `Update pierd to ${latest}?`,
      body: (
        <>
          <p>
            Pier downloads {latest} from GitHub, checks it, puts it in place and restarts pierd. Your sites and this dashboard
            stop answering for the few seconds that takes, and a build that's running is cut short.
          </p>
          <p>If anything goes wrong before the restart, nothing changes and this page says why.</p>
        </>
      ),
      action: 'Update',
      working: 'Asking pierd…',
      run: async () => {
        await api('/api/server/upgrade', { method: 'POST', body: JSON.stringify({ version: latest }) })
        setTarget(latest)
        setStep('')
        setFailure('')
        setPhase('updating')
      },
    })

  return { info: d, phase, target, step, failure, start }
}

// UpdateEnd is what sits beside "Running, version 0.7.0": the release
// that's out, with its notes, and the button that updates to it.
export function UpdateEnd({ u }: { u: Update }) {
  const d = u.info
  if (u.phase === 'updating') {
    return (
      <span className="t-caption t-muted working-line">
        <Spinner />
        Updating to {u.target}
      </span>
    )
  }
  if (!d || !d.newer || u.phase === 'done') return null
  return (
    <>
      <a className="t-caption release-out" href={d.notes} target="_blank" rel="noreferrer">
        {d.latest} is out
        <Visit size={14} />
      </a>
      {d.helper && (
        <button className="btn quiet small" onClick={() => u.start(d.latest)}>
          {u.phase === 'failed' ? `Try ${d.latest} again` : `Update to ${d.latest}`}
        </button>
      )}
    </>
  )
}

// UpdateNote is the line under pierd's name: how the update is going, why
// it failed, or, when the server can't update itself yet, the command that
// lets it.
export function UpdateNote({ u }: { u: Update }) {
  const d = u.info
  if (u.phase === 'updating') {
    return (
      <span className="t-note">
        {u.step ? `${u.step}. ` : ''}pierd restarts; this page reconnects on its own.
      </span>
    )
  }
  if (u.phase === 'done') return <span className="t-note">Updated to {u.target}.</span>
  if (u.phase === 'failed') {
    return (
      <span className="t-note bad" role="alert">
        The update to {u.target} didn't happen: {u.failure || 'pierd gave no reason.'}
      </span>
    )
  }
  if (d && d.newer && !d.helper) {
    return (
      <span className="t-note update-command">
        Run the installer once more to turn on updates from here:{' '}
        <span className="mono">{d.command}</span>
        <button className="copy shown" onClick={() => copy(d.command, 'the command')} aria-label="Copy the command" data-tip="Copy the command">
          <Copy size={14} />
        </button>
      </span>
    )
  }
  return null
}
