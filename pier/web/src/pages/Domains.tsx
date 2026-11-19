import { useState, type FormEvent, type ReactNode } from 'react'
import { api } from '../lib/api'
import { useData } from '../lib/data'
import { ago } from '../lib/format'
import { Redeploy } from '../components/Icons'

type Domain = {
  host: string
  redirect?: string
  verified: boolean
  checked: number
  address_result: string
  redirect_result: string
  tls: boolean
  redirect_tls: boolean
}
type Response = {
  domains: Domain[]
  public_ip: string
  default_host: string
  https: boolean
  default_tls: boolean
  deployed: boolean
}

function serving(data: Response, tls: boolean) {
  if (!data.deployed) return 'Waiting for a production deployment'
  if (!data.https) return 'Serving over HTTP'
  return tls ? 'Live, with HTTPS' : 'Waiting for HTTPS'
}

function DomainCard({
  host,
  status,
  note,
  live,
  actions,
  children,
}: {
  host: string
  status: string
  note?: string
  live: boolean
  actions?: ReactNode
  children?: ReactNode
}) {
  return (
    <section className="panel domain-card">
      <header>
        <div className="domain-info">
          <h3 className="mono t-caption t-strong">{host}</h3>
          <span className="domain-status t-caption t-muted">
            <i className={'swatch ' + (live ? 'ready' : 'building')} />
            {status}
            {note && <span className="t-note">{note}</span>}
          </span>
        </div>
        {actions && <div className="domain-actions">{actions}</div>}
      </header>
      {children}
    </section>
  )
}

export function Domains({ project, now }: { project: string; now: number }) {
  const path = '/api/projects/' + encodeURIComponent(project) + '/domains'
  const { data, error, reload } = useData<Response>(path, () => true, 10000)
  const [host, setHost] = useState('')
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState('')
  async function change(method: string, suffix: string, body?: object) {
    setProblem('')
    setBusy(true)
    try {
      await api(path + suffix, { method, body: body && JSON.stringify(body) })
      if (body) setHost('')
      await reload()
    } catch (e) {
      setProblem(e instanceof Error ? e.message : 'Could not reach Pier.')
    } finally {
      setBusy(false)
    }
  }
  function add(e: FormEvent) {
    e.preventDefault()
    void change('POST', '', { host })
  }
  return (
    <main className="main domains-page">
      <form className="domain-add" onSubmit={add}>
        <div>
          <h2 className="t-small t-strong">Domains</h2>
          <p className="t-caption t-muted">
            Point your own domain at {project}. Pier checks the DNS and gets HTTPS for you.
          </p>
        </div>
        <input
          className="input mono"
          aria-label="Domain name"
          required
          autoCapitalize="off"
          spellCheck={false}
          placeholder="yourdomain.com"
          value={host}
          onChange={(e) => setHost(e.target.value)}
        />
        <button className="btn primary large" disabled={busy}>
          Add domain
        </button>
      </form>
      {(problem || error) && (
        <p role="alert" className="field-message">
          {problem || error?.message}
        </p>
      )}
      {data && (
        <div className="domain-cards">
          {data.domains.map((d) => {
            const suffix = '/' + encodeURIComponent(d.host)
            const note = d.checked
              ? 'Checked ' + ago(new Date(d.checked).toISOString(), now)
              : 'Waiting for the first DNS check.'
            const records = [
              ['A', d.redirect ? '@' : d.host, data.public_ip || 'Set the server IP with pierd init', d.address_result],
              ...(d.redirect ? [['CNAME', 'www', d.host, d.redirect_result]] : []),
            ]
            return (
              <div className="domain-cards" key={d.host}>
                <DomainCard
                  host={d.host}
                  status={d.verified ? serving(data, d.tls) : 'Waiting for DNS'}
                  note={
                    d.verified && data.deployed && data.https && !d.tls
                      ? 'DNS matches. Pier is waiting for the certificate.'
                      : note
                  }
                  live={d.verified && data.deployed && (!data.https || d.tls)}
                  actions={
                    <>
                      <button className="btn quiet" disabled={busy} onClick={() => change('POST', suffix + '/check')}>
                        <Redeploy size={14} />
                        Check now
                      </button>
                      <button
                        className="btn quiet"
                        disabled={busy}
                        onClick={() => {
                          if (
                            window.confirm(
                              'Remove ' + d.host + (d.redirect ? ' and ' + d.redirect : '') + ' from this project?',
                            )
                          )
                            void change('DELETE', suffix)
                        }}
                      >
                        Remove
                      </button>
                    </>
                  }
                >
                  {!d.verified && (
                    <div className="domain-records">
                      <p className="t-small">Add these records at your DNS provider.</p>
                      <div className="domain-table">
                        <table>
                          <thead>
                            <tr>
                              {['Type', 'Name', 'Value', 'Right now'].map((t) => (
                                <th key={t}>{t}</th>
                              ))}
                            </tr>
                          </thead>
                          <tbody>
                            {records.map(([type, name, value, actual]) => (
                              <tr key={type}>
                                <td>{type}</td>
                                <td>{name}</td>
                                <td>{value}</td>
                                <td>{actual || 'Not checked yet'}</td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </div>
                      <p className="t-note">Pier checks every 10 seconds. DNS changes can take a few minutes.</p>
                    </div>
                  )}
                </DomainCard>
                {d.verified && d.redirect && (
                  <DomainCard
                    host={d.redirect}
                    status={data.deployed ? 'Redirects to ' + d.host : 'Waiting for a production deployment'}
                    note={
                      data.deployed
                        ? d.redirect_tls
                          ? 'Permanent redirect, with HTTPS'
                          : data.https
                            ? 'Waiting for HTTPS'
                            : 'Permanent redirect, over HTTP'
                        : undefined
                    }
                    live={data.deployed && (!data.https || d.redirect_tls)}
                  />
                )}
              </div>
            )
          })}
          <DomainCard
            host={data.default_host}
            status={serving(data, data.default_tls)}
            note="The test address from your base domain."
            live={data.deployed && (!data.https || data.default_tls)}
          />
        </div>
      )}
    </main>
  )
}
