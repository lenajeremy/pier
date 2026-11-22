import type { ReactNode } from 'react'
import type { Health } from '../lib/api'
import { plural } from '../lib/format'
import { Warning } from './Icons'
import { Spinner } from './Spinner'
import { When } from './ui'

// in is how long until a time to come, in words: "in 3 minutes".
function soon(iso: string, now: number): string {
  const m = Math.ceil((new Date(iso).getTime() - now) / 60_000)
  if (m <= 1) return 'within a minute'
  return m < 60 ? `in ${m} minutes` : `in ${plural(Math.round(m / 60), 'hour')}`
}

// trouble is whether production's server needs the owner's eye: three
// failed checks in a row, or a restart because of them.
export const trouble = (h?: Health | null): h is Health => !!h && (h.state === 'unhealthy' || h.state === 'restarting')

// Restarted says what Pier did about it, and what it does next.
function Restarted({ h, now }: { h: Health; now: number }) {
  if (h.state === 'restarting') return <>Pier is restarting the container now.</>
  if (!h.restarted_at) return null
  return (
    <>
      Pier restarted the container <When at={h.restarted_at} now={now} lower />
      {h.restarts > 1 ? `, ${plural(h.restarts, 'time')} in all` : ''}
      {h.next_restart && new Date(h.next_restart).getTime() > now ? `, and restarts it again ${soon(h.next_restart, now)} if it still fails.` : '.'}
    </>
  )
}

// HealthAlert sits on the project's home while production's server fails
// its health check, with what failed, when, and what Pier did about it.
export function HealthAlert({ h, now, action }: { h: Health; now: number; action?: ReactNode }) {
  return (
    <div className="alert error" role="alert">
      {h.state === 'restarting' ? (
        <span className="alert-spinner">
          <Spinner large />
        </span>
      ) : (
        <Warning size={20} />
      )}
      <div>
        <span className="t-small t-strong">
          {h.state === 'restarting' ? "Restarting production's server after failed health checks" : "Production's server is failing its health check"}
        </span>
        <span className="t-caption t-muted">
          {h.last_failure_at && (
            <>
              It started failing <When at={h.since ?? h.last_failure_at} now={now} lower />, and the last check found that{' '}
              {h.last_failure}.{' '}
            </>
          )}
          <Restarted h={h} now={now} />
        </span>
      </div>
      {action}
    </div>
  )
}

// HealthLines is the Health check detail on a deployment's page: how its
// checks are going, and the last failure even after it recovered.
export function HealthLines({ h, now }: { h: Health; now: number }) {
  const word =
    h.state === 'healthy'
      ? 'Passing'
      : h.state === 'failing'
        ? `Failed the last ${h.failures === 1 ? 'check' : plural(h.failures, 'check')}`
        : h.state === 'restarting'
          ? 'Restarting'
          : 'Failing'
  const swatch = h.state === 'healthy' ? 'done' : h.state === 'failing' ? 'warning' : 'failed'
  return (
    <>
      <span className="line">
        {h.state === 'restarting' ? <Spinner className="warning" /> : <i className={'swatch ' + swatch} aria-hidden />}
        <span>{word}</span>
        <span className="t-muted">
          {h.asked === 'the process' ? 'Checks that the process runs' : h.asked}
          {h.checked_at && (
            <>
              , checked <When at={h.checked_at} now={now} lower />
            </>
          )}
        </span>
      </span>
      {h.last_failure && h.last_failure_at && (
        <span className="t-muted">
          {h.state === 'healthy' ? 'Last failed' : 'Started failing'} <When at={h.state === 'healthy' ? h.last_failure_at : (h.since ?? h.last_failure_at)} now={now} lower />:{' '}
          {h.last_failure}.{h.state !== 'healthy' && h.state !== 'failing' ? ' ' : ''}
          {h.state !== 'healthy' && h.state !== 'failing' && <Restarted h={h} now={now} />}
        </span>
      )}
    </>
  )
}
