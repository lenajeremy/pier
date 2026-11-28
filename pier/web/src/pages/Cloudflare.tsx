import { useState, type FormEvent } from 'react'
import { api, type CloudflareView } from '../lib/api'
import { useData } from '../lib/data'
import { plural, size } from '../lib/format'
import { Link } from '../lib/router'
import { Check, CloudflareMark, Trash, Visit } from '../components/Icons'
import { confirm } from '../components/Dialog'
import { Menu } from '../components/Menu'
import { Spinner } from '../components/Spinner'
import { toast } from '../components/Toast'
import { Badge, Problem, Skeleton } from '../components/ui'
import { IntegrationHeading } from './Integrations'
import { Gate, onlyOwner, useRole } from '../lib/role'

const permissions = [
  ['Make and delete buckets', 'Workers R2 Storage: Edit'],
  ['Make a key for each bucket', 'Account API Tokens: Edit'],
  ['Serve public buckets from your domain', 'Zone: Read, DNS: Edit'],
]

// CloudflareIntegration is Cloudflare R2's page: connecting the owner's
// account, and once it's connected, every project's buckets.
export function CloudflareIntegration() {
  const { data, error, reload } = useData<CloudflareView>('/api/cloudflare', () => true, 60_000)
  const { owner } = useRole()
  const heading = (state?: React.ReactNode) => (
    <IntegrationHeading name="Cloudflare R2" mark={<CloudflareMark size={24} />} note="File storage for your projects, in your own Cloudflare account.">
      {state}
    </IntegrationHeading>
  )
  if (error || !data) {
    return (
      <main className="main global">
        {heading()}
        {error ? (
          <Problem>{error.message}</Problem>
        ) : (
          <div className="panel">
            <Skeleton rows={2} tall />
          </div>
        )}
      </main>
    )
  }
  const buckets = data.buckets ?? []
  const projects = new Set(buckets.map((b) => b.project)).size
  const files = buckets.reduce((n, b) => n + b.files, 0)
  const bytes = buckets.reduce((n, b) => n + b.bytes, 0)
  const disconnect = () =>
    confirm({
      title: 'Disconnect Cloudflare?',
      body: <p>Pier forgets the account and its token. Delete every bucket first.</p>,
      action: 'Disconnect',
      working: 'Disconnecting…',
      danger: true,
      run: async () => {
        await api('/api/cloudflare', { method: 'DELETE' })
        reload()
      },
    }).then((ok) => ok && toast('Disconnected Cloudflare'))
  return (
    <main className="main global">
      {heading(
        <>
          <span className="integration-state t-caption">
            <i className={'swatch ' + (data.connected ? 'ready' : '')} />
            {data.connected ? 'Connected' : 'Not connected'}
          </span>
          {data.connected && owner && (
            <Menu label="More for Cloudflare" items={[{ label: 'Disconnect', icon: <Trash />, danger: true, onSelect: disconnect }]} />
          )}
        </>,
      )}
      <section className="panel" aria-label="Cloudflare R2">
        {data.connected ? (
          <div className="strip" style={{ ['--cells' as string]: 4 }}>
            <div>
              <span className="t-note">Account</span>
              <span className="t-small t-strong mono-value">
                {data.account?.slice(0, 8)}…{data.account?.slice(-4)}
              </span>
              <span className="t-note">
                Buckets start with <span className="mono">{data.prefix}</span>
              </span>
            </div>
            <div>
              <span className="t-note">Buckets</span>
              <span className="t-small t-strong">{buckets.length.toLocaleString('en-GB')}</span>
              <span className="t-note">{buckets.length ? `In ${plural(projects, 'project')}` : 'Add one from a Storage tab'}</span>
            </div>
            <div>
              <span className="t-note">Stored</span>
              <span className="t-small t-strong">{files ? size(bytes) : '–'}</span>
              <span className="t-note">{plural(files, 'file')}</span>
            </div>
            <div>
              <span className="t-note">Public buckets at</span>
              <span className="t-small t-strong mono-value">{data.domain ? `files-*.${data.domain}` : 'r2.dev'}</span>
              <span className="t-note">{data.domain ? "On Cloudflare's network" : 'Rate-limited, for trying things'}</span>
            </div>
          </div>
        ) : (
          <Connect onDone={reload} />
        )}
      </section>
      {buckets.length > 0 && (
        <div className="stack">
          <div className="toolbar">
            <div className="title">
              <h2 className="t-small t-strong">Buckets</h2>
              <span className="count">{buckets.length}</span>
            </div>
          </div>
          <div className="panel">
            <div className="table all-buckets">
              <div className="t-head">
                <span>Project</span>
                <span>Bucket</span>
                <span>Serves</span>
                <span className="num">Files</span>
                <span className="num">Size</span>
              </div>
              {buckets.map((b) => (
                <div className="t-row" key={b.bucket}>
                  <Link className="t-strong" to={`/projects/${b.project}/storage`}>
                    {b.project}
                  </Link>
                  <span className="mono t-muted" data-tip-cut>
                    {b.name}
                  </span>
                  <span className="scopes">
                    {b.environments.length ? b.environments.map((e) => <Badge key={e} role={e} />) : <span className="t-muted">Nothing</span>}
                  </span>
                  <span className="num t-muted">{b.count_error ? '–' : b.files.toLocaleString('en-GB') + (b.more ? '+' : '')}</span>
                  <span className="num t-muted">{b.count_error || !b.files ? '–' : size(b.bytes)}</span>
                </div>
              ))}
            </div>
          </div>
        </div>
      )}
    </main>
  )
}

function Connect({ onDone }: { onDone: () => void }) {
  const { owner } = useRole()
  const [account, setAccount] = useState('')
  const [token, setToken] = useState('')
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState<{ field?: string; message: string }>()
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setProblem(undefined)
    try {
      await api('/api/cloudflare', { method: 'PUT', body: JSON.stringify({ account, token }) })
      toast('Connected Cloudflare')
      onDone()
    } catch (err) {
      const body = (err as { body?: { field?: string; error?: string } }).body
      setProblem({ field: body?.field, message: body?.error ?? (err instanceof Error ? err.message : "Can't reach pierd.") })
      setBusy(false)
    }
  }
  const tokens = /^[0-9a-f]{32}$/.test(account) ? `https://dash.cloudflare.com/${account}/api-tokens` : 'https://dash.cloudflare.com/?to=/:account/api-tokens'
  return (
    <form className="integration-setup" onSubmit={submit} noValidate>
      <p className="t-caption t-muted">Pier needs one account token that can do this:</p>
      <div className="integration-permissions">
        {permissions.map(([label, value]) => (
          <div key={value}>
            <span className="t-caption">{label}</span>
            <span className="t-caption mono t-muted">
              {value}
              <Check size={14} />
            </span>
          </div>
        ))}
      </div>
      <div className="form-row">
        <label className={'field' + (problem?.field === 'account' ? ' error' : '')}>
          <span className="field-label">Account ID</span>
          <input
            className="input mono"
            value={account}
            onChange={(e) => (setAccount(e.target.value.trim()), setProblem(undefined))}
            placeholder="4793d734c0b8e484dfc37ec392b5fa8a"
            disabled={!owner}
            spellCheck={false}
            autoCapitalize="off"
          />
          {problem?.field === 'account' && (
            <span className="field-message" role="alert">
              {problem.message}
            </span>
          )}
        </label>
        <label className={'field' + (problem?.field === 'token' ? ' error' : '')}>
          <span className="field-label">API token</span>
          <input
            className="input mono"
            type="password"
            value={token}
            onChange={(e) => (setToken(e.target.value.trim()), setProblem(undefined))}
            placeholder="Paste the token"
            disabled={!owner}
            spellCheck={false}
            autoComplete="off"
          />
          {problem?.field === 'token' && (
            <span className="field-message" role="alert">
              {problem.message}
            </span>
          )}
        </label>
      </div>
      <div className="form-foot">
        {problem && !problem.field ? (
          <span className="field-message" role="alert">
            {problem.message}
          </span>
        ) : (
          <span className="t-note">The token stays on this server, sealed.</span>
        )}
        <a className="btn quiet visit" href={tokens} target="_blank" rel="noreferrer">
          Make one on Cloudflare
          <Visit />
        </a>
        <Gate allow={owner} reason={onlyOwner('connect Cloudflare')}>
          <button className="btn primary" disabled={busy || !account || !token}>
            {busy && <Spinner />}
            {busy ? 'Checking…' : 'Connect'}
          </button>
        </Gate>
      </div>
    </form>
  )
}
