import { useState, type FormEvent, type ReactNode } from 'react'
import { api } from '../lib/api'
import { failed } from '../lib/actions'
import { useData } from '../lib/data'
import { Copy, Redeploy, Trash, Visit } from '../components/Icons'
import { confirm } from '../components/Dialog'
import { Menu } from '../components/Menu'
import { copy, toast } from '../components/Toast'
import { Problem, Skeleton, When } from '../components/ui'

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

function DomainCard({ host, url, status, note, live, actions, children }: {
  host: string
  url?: string
  status: string
  note?: ReactNode
  live: boolean
  actions?: ReactNode
  children?: ReactNode
}) {
  return (
    <section className="panel domain-card">
      <header>
        <div className="domain-info">
          <h3 className="mono t-caption t-strong">
            {url ? (
              <a href={url} target="_blank" rel="noreferrer">
                {host}
              </a>
            ) : (
              host
            )}
          </h3>
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

// Domains is F1 to F3: the project's own domains, the DNS records each still
// needs, and the address it always has under the base domain.
export function Domains({ project, now }: { project: string; now: number }) {
  const path = '/api/projects/' + encodeURIComponent(project) + '/domains'
  const { data, error, reload } = useData<Response>(path, () => true, 10000)
  const [host, setHost] = useState('')
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState('')

  const add = async (e: FormEvent) => {
    e.preventDefault()
    const name = host.trim().toLowerCase()
    if (!name) return
    setProblem('')
    setBusy(true)
    try {
      await api(path, { method: 'POST', body: JSON.stringify({ host: name }) })
      setHost('')
      await reload()
      toast(`Added ${name}. It goes live once its DNS records match.`)
    } catch (err) {
      setProblem(err instanceof Error ? err.message : "Can't reach pierd.")
    } finally {
      setBusy(false)
    }
  }
  const check = async (d: Domain) => {
    try {
      await api(path + '/' + encodeURIComponent(d.host) + '/check', { method: 'POST' })
      await reload()
    } catch (err) {
      failed(err)
    }
  }
  const remove = (d: Domain) =>
    confirm({
      title: (
        <>
          Remove <span className="mono">{d.host}</span>?
        </>
      ),
      body: (
        <p>
          {d.host}
          {d.redirect ? ` and ${d.redirect} stop` : ' stops'} serving {project} at once. {data?.default_host} keeps working, and
          you can add the domain again later.
        </p>
      ),
      action: 'Remove',
      working: 'Removing…',
      danger: true,
      run: async () => {
        await api(path + '/' + encodeURIComponent(d.host), { method: 'DELETE' })
        await reload()
      },
    }).then((ok) => ok && toast(`Removed ${d.host}`))

  const scheme = data?.https ? 'https://' : 'http://'
  return (
    <main className="main domains-page">
      <form className="domain-add" onSubmit={add} noValidate>
        <div>
          <h2 className="t-small t-strong">Domains</h2>
          <p className="t-caption t-muted">Point your own domain at {project}. Pier checks the DNS and gets HTTPS for you.</p>
        </div>
        <label className={'field' + (problem ? ' error' : '')}>
          <input
            className="input mono"
            aria-label="Domain name"
            autoCapitalize="off"
            autoComplete="off"
            spellCheck={false}
            placeholder="yourdomain.com"
            value={host}
            onChange={(e) => {
              setHost(e.target.value.replace(/^https?:\/\//, '').replace(/[\s/]/g, ''))
              setProblem('')
            }}
          />
        </label>
        <button className="btn primary large" disabled={busy || !host.trim()}>
          {busy ? 'Adding…' : 'Add domain'}
        </button>
      </form>
      {problem && <Problem>{problem}</Problem>}
      {error && <Problem>{error.message}</Problem>}
      {!data && !error && (
        <div className="panel">
          <Skeleton rows={2} tall />
        </div>
      )}
      {data && (
        <div className="domain-cards">
          {data.domains.map((d) => {
            const live = d.verified && data.deployed && (!data.https || d.tls)
            const records = [
              { type: 'A', name: d.redirect ? '@' : d.host, value: data.public_ip, actual: d.address_result },
              ...(d.redirect ? [{ type: 'CNAME', name: 'www', value: d.host, actual: d.redirect_result }] : []),
            ]
            return (
              <div className="domain-cards" key={d.host}>
                <DomainCard
                  host={d.host}
                  url={live ? scheme + d.host : undefined}
                  status={d.verified ? serving(data, d.tls) : 'Waiting for DNS'}
                  note={
                    d.verified && data.deployed && data.https && !d.tls ? (
                      'DNS matches. Pier is getting the certificate.'
                    ) : d.checked ? (
                      <>
                        Checked <When at={d.checked} now={now} lower />
                      </>
                    ) : (
                      'Waiting for the first DNS check.'
                    )
                  }
                  live={live}
                  actions={
                    <>
                      {!live && (
                        <button className="btn quiet icon" onClick={() => check(d)}>
                          <Redeploy size={14} />
                          Check now
                        </button>
                      )}
                      <Menu
                        label={`Actions for ${d.host}`}
                        items={[
                          ...(live ? [{ label: 'Visit', icon: <Visit color="#A3ABBE" />, href: scheme + d.host }] : []),
                          { label: 'Copy address', icon: <Copy />, onSelect: () => copy(scheme + d.host) },
                          ...(live ? [{ label: 'Check the DNS again', icon: <Redeploy color="#A3ABBE" />, onSelect: () => check(d) }] : []),
                          'separator' as const,
                          { label: 'Remove', icon: <Trash />, danger: true, onSelect: () => remove(d) },
                        ]}
                      />
                    </>
                  }
                >
                  {!d.verified && (
                    <div className="domain-records">
                      <p className="t-small">Add {records.length === 1 ? 'this record' : 'these records'} at your DNS provider.</p>
                      <div className="domain-table">
                        <table>
                          <thead>
                            <tr>
                              <th>Type</th>
                              <th>Name</th>
                              <th>Value</th>
                              <th>Right now</th>
                            </tr>
                          </thead>
                          <tbody>
                            {records.map((r) => (
                              <tr key={r.type}>
                                <td>{r.type}</td>
                                <td>
                                  <Copyable text={r.name} what="the name" />
                                </td>
                                <td>
                                  {r.value ? (
                                    <Copyable text={r.value} what="the value" />
                                  ) : (
                                    <span className="t-quiet">Set the server's IP with pierd init</span>
                                  )}
                                </td>
                                <td className={/^points to|^is /i.test(r.actual) ? 'wrong' : ''}>{r.actual || 'Not checked yet'}</td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </div>
                      <p className="t-note">
                        Pier checks every 10 seconds. Once {records.length === 1 ? 'it resolves' : 'both resolve'}, it gets the HTTPS
                        certificate on its own. DNS changes can take a few minutes.
                      </p>
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
                          ? 'A permanent redirect, with HTTPS.'
                          : data.https
                            ? 'Waiting for HTTPS.'
                            : 'A permanent redirect, over HTTP.'
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
            url={data.deployed ? scheme + data.default_host : undefined}
            status={serving(data, data.default_tls)}
            note="The address from your base domain. It can't be removed."
            live={data.deployed && (!data.https || data.default_tls)}
            actions={
              <button className="copy shown" onClick={() => copy(scheme + data.default_host)} aria-label={`Copy ${data.default_host}`} data-tip="Copy address">
                <Copy />
              </button>
            }
          />
        </div>
      )}
    </main>
  )
}

function Copyable({ text, what }: { text: string; what: string }) {
  return (
    <span className="copyable">
      <span>{text}</span>
      <button className="copy shown" type="button" onClick={() => copy(text, what)} aria-label={`Copy ${text}`} data-tip={`Copy ${what}`}>
        <Copy size={14} />
      </button>
    </span>
  )
}
