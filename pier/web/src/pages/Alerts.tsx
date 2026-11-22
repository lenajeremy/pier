import { useState, type FormEvent, type ReactNode } from 'react'
import { api, ApiError } from '../lib/api'
import { failed } from '../lib/actions'
import { useData, useNow } from '../lib/data'
import { plural } from '../lib/format'
import { Chat, Copy, Hook, Mail, Pencil, Redeploy, Trash } from '../components/Icons'
import { confirm, Dialog } from '../components/Dialog'
import { Menu, type MenuItem } from '../components/Menu'
import { Spinner, useBusy } from '../components/Spinner'
import { copy, toast } from '../components/Toast'
import { PageHeading, Problem, Skeleton, When } from '../components/ui'

type Kind = 'email' | 'chat' | 'webhook'
type Email = { provider: 'smtp' | 'resend'; host?: string; port?: number; user?: string; from: string }
type Channel = {
  kind: Kind
  email?: Email
  chat?: { service: 'slack' | 'discord' }
  set: string[] // which secrets are saved: password, api_key, url, signing
  updated_at: string
  last_sent?: string
  last_error?: string
  last_error_at?: string
}
type Data = {
  channels: Partial<Record<Kind, Channel>>
  recipients: { name: string; email: string }[]
  previews: boolean
  limit: { alerts: number; minutes: number }
}
type Fault = { text: string; field?: string } | null
const fault = (e: unknown): Fault =>
  e instanceof ApiError ? { text: e.message, field: String(e.body.field ?? '') } : { text: "Can't reach pierd." }

const names = (list: string[]) => (list.length < 2 ? list[0] : `${list.slice(0, -1).join(', ')} and ${list[list.length - 1]}`)

// Alerts is where the owner says where Pier sends word of trouble: email,
// a Slack or Discord channel, or a webhook, and which events send it.
export function Alerts() {
  const { data, error, reload } = useData<Data>('/api/alerts', () => true, 30_000)
  const now = useNow()
  const [open, setOpen] = useState<Kind | null>(null)
  const [signing, setSigning] = useState<string | null>(null) // a new signing secret, shown once
  const [testing, test] = useBusy<Kind>()
  const [tested, setTested] = useState<Partial<Record<Kind, string>>>({}) // a failed test's error
  const [saving, setSaving] = useState(false)

  const sendTest = (kind: Kind) =>
    test(kind, async () => {
      setTested((t) => ({ ...t, [kind]: undefined }))
      try {
        await api(`/api/alerts/${kind}/test`, { method: 'POST' })
        toast(kind === 'email' ? 'Sent a test alert. Check the inbox it goes to.' : 'Sent a test alert')
      } catch (e) {
        setTested((t) => ({ ...t, [kind]: e instanceof Error ? e.message : "Can't reach pierd." }))
      }
      await reload()
    })

  const remove = (kind: Kind) =>
    confirm({
      title: kind === 'email' ? 'Stop alert emails?' : kind === 'chat' ? 'Stop alerts to Slack or Discord?' : 'Stop alerts to the webhook?',
      body: (
        <p>
          Pier forgets {kind === 'email' ? 'how to send email' : kind === 'chat' ? 'the channel’s webhook URL' : 'the webhook’s URL and its signing secret'}{' '}
          and sends nothing there from now on. Alerts still go to the other channels.
        </p>
      ),
      action: 'Stop alerts',
      working: 'Stopping…',
      danger: true,
      run: async () => {
        await api(`/api/alerts/${kind}`, { method: 'DELETE' })
        await reload()
      },
    }).then((ok) => ok && toast(kind === 'email' ? 'Stopped alert emails' : kind === 'chat' ? 'Stopped alerts to the channel' : 'Stopped alerts to the webhook'))

  const newSecret = () =>
    confirm({
      title: 'Make a new signing secret?',
      body: (
        <p>
          The old secret stops working at once. Alerts from now on are signed with the new one, so give it to whatever
          receives them before the next alert.
        </p>
      ),
      action: 'Make a new secret',
      working: 'Making…',
      run: async () => {
        const r = await api<{ signing_secret: string }>('/api/alerts/webhook/secret', { method: 'POST' })
        setSigning(r.signing_secret)
        await reload()
      },
    })

  const previews = async (on: boolean) => {
    setSaving(true)
    try {
      await api('/api/alerts', { method: 'PUT', body: JSON.stringify({ previews: on }) })
      await reload()
      toast(on ? 'Failed previews send an alert again' : "Failed previews won't send an alert")
    } catch (e) {
      failed(e)
    } finally {
      setSaving(false)
    }
  }

  if (error) {
    return (
      <main className="main global">
        <PageHeading title="Alerts" />
        <Problem>{error.message}</Problem>
      </main>
    )
  }

  const ch = data?.channels ?? {}
  const people = data?.recipients ?? []
  const emailTo =
    people.length === 0
      ? 'Nobody on this server has alert emails on. Each person turns them on in their account menu.'
      : `Goes to ${people.length > 3 ? plural(people.length, 'person', 'people') : names(people.map((p) => p.email))}. Each person turns alert emails off in their account menu.`

  const rows: { kind: Kind; icon: ReactNode; name: string; off: string; on: (c: Channel) => string; note?: string }[] = [
    {
      kind: 'email',
      icon: <Mail />,
      name: 'Email',
      off: 'Send alerts by email, through your own SMTP server or Resend.',
      on: (c) =>
        c.email?.provider === 'resend'
          ? `Sends through Resend, from ${c.email.from}.`
          : `Sends through ${c.email?.host}:${c.email?.port}, from ${c.email?.from}.`,
      note: emailTo,
    },
    {
      kind: 'chat',
      icon: <Chat />,
      name: 'Slack or Discord',
      off: 'Post alerts in a channel, with the webhook URL Slack or Discord gives you.',
      on: (c) => (c.chat?.service === 'discord' ? 'Posts in a Discord channel.' : 'Posts in a Slack channel.'),
    },
    {
      kind: 'webhook',
      icon: <Hook />,
      name: 'Webhook',
      off: 'Post every alert as JSON to a URL of your own, signed so the receiver can check it came from Pier.',
      on: () => 'Posts JSON, signed with HMAC-SHA256 in the X-Pier-Signature header.',
    },
  ]

  return (
    <main className="main global">
      <PageHeading
        title="Alerts"
        note="Pier tells you when a deploy fails, when production stops passing its health check, and when a scheduled job fails, and once more when each is fixed."
      />
      {!data ? (
        <div className="panel">
          <Skeleton rows={3} tall />
        </div>
      ) : (
        <>
          <section className="panel">
            <div className="panel-head">
              <h2 className="t-small t-strong">Where alerts go</h2>
            </div>
            {rows.map((r) => {
              const c = ch[r.kind]
              const busy = testing === r.kind
              const problem = tested[r.kind]
              const items: MenuItem[] = c
                ? [
                    { label: 'Change', icon: <Pencil />, onSelect: () => setOpen(r.kind) },
                    ...(r.kind === 'webhook' ? [{ label: 'Make a new signing secret', icon: <Redeploy color="#A3ABBE" />, onSelect: newSecret } as MenuItem] : []),
                    'separator',
                    { label: 'Stop alerts here', icon: <Trash />, danger: true, onSelect: () => remove(r.kind) },
                  ]
                : []
              return (
                <div className="channel-row" key={r.kind}>
                  <span className="integration-mark">{r.icon}</span>
                  <div className="channel-what">
                    <span className="t-small t-strong">{r.name}</span>
                    <span className="t-caption t-muted">{c ? r.on(c) : r.off}</span>
                    {c && r.note && <span className="t-note">{r.note}</span>}
                    {problem ? (
                      <span className="t-note bad" role="alert">
                        The test alert didn't arrive: {problem}
                      </span>
                    ) : c?.last_error && c.last_error_at ? (
                      <span className="t-note bad">
                        The last alert didn't arrive <When at={c.last_error_at} now={now} lower />: {c.last_error}
                      </span>
                    ) : c?.last_sent ? (
                      <span className="t-note">
                        Last alert sent <When at={c.last_sent} now={now} lower />.
                      </span>
                    ) : null}
                  </div>
                  <span className="integration-state t-caption">
                    <i className={'swatch ' + (!c ? '' : c.last_error && !c.last_sent ? 'failed' : c.last_error ? 'warning' : 'ready')} />
                    {c ? 'On' : 'Off'}
                  </span>
                  {c ? (
                    <>
                      <button className="btn quiet" disabled={busy} onClick={() => sendTest(r.kind)}>
                        {busy && <Spinner />}
                        {busy ? 'Sending…' : 'Send a test alert'}
                      </button>
                      <Menu label={`More for ${r.name}`} items={items} />
                    </>
                  ) : (
                    <button className="btn quiet" onClick={() => setOpen(r.kind)}>
                      Set up
                    </button>
                  )}
                </div>
              )
            })}
          </section>

          <section className="panel">
            <div className="panel-head">
              <h2 className="t-small t-strong">What sends an alert</h2>
              <span className="t-note end">
                One alert per problem, however often it happens, and one when it's fixed. Pier sends at most{' '}
                {data.limit.alerts} in {data.limit.minutes} minutes.
              </span>
            </div>
            <div className="event-row">
              <div className="channel-what">
                <span className="t-small">A production deploy fails</span>
                <span className="t-note">Always on. Another alert follows when production builds again.</span>
              </div>
            </div>
            <div className="event-row">
              <label className="check">
                <input type="checkbox" checked={data.previews} disabled={saving} onChange={(e) => previews(e.target.checked)} />
                <span>A preview deploy fails</span>
              </label>
              <span className="t-note">
                {data.previews
                  ? 'One alert per branch, and another when that branch builds again.'
                  : 'Off. Failed previews still show on each project’s page.'}
              </span>
            </div>
            <div className="event-row">
              <div className="channel-what">
                <span className="t-small">Production fails its health check</span>
                <span className="t-note">
                  After three failed checks in a row, 30 seconds apart. Pier restarts the container, and sends another alert
                  when it passes again.
                </span>
              </div>
            </div>
            <div className="event-row">
              <div className="channel-what">
                <span className="t-small">A scheduled job fails</span>
                <span className="t-note">One alert per job, with the end of its output, and another when it next succeeds.</span>
              </div>
            </div>
          </section>
        </>
      )}

      {open === 'email' && (
        <EmailDialog
          saved={ch.email}
          onClose={() => setOpen(null)}
          onDone={() => {
            setOpen(null)
            reload()
            toast('Saved. Send a test alert to check it.')
          }}
        />
      )}
      {(open === 'chat' || open === 'webhook') && (
        <UrlDialog
          kind={open}
          saved={ch[open]}
          onClose={() => setOpen(null)}
          onDone={(secret) => {
            setOpen(null)
            reload()
            if (secret) setSigning(secret)
            else toast('Saved. Send a test alert to check it.')
          }}
        />
      )}
      {signing && <SigningDialog secret={signing} onClose={() => setSigning(null)} />}
    </main>
  )
}

function EmailDialog({ saved, onClose, onDone }: { saved?: Channel; onClose: () => void; onDone: () => void }) {
  const s = saved?.email
  const [provider, setProvider] = useState<'smtp' | 'resend'>(s?.provider ?? 'smtp')
  const [host, setHost] = useState(s?.host ?? '')
  const [port, setPort] = useState(String(s?.port ?? 587))
  const [user, setUser] = useState(s?.user ?? '')
  const [password, setPassword] = useState('')
  const [key, setKey] = useState('')
  const [from, setFrom] = useState(s?.from ?? '')
  const [problem, setProblem] = useState<Fault>(null)
  const [busy, setBusy] = useState(false)
  const has = (secret: string) => !!saved?.set.includes(secret) && s?.provider === provider
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setProblem(null)
    try {
      await api('/api/alerts/email', {
        method: 'PUT',
        body: JSON.stringify({ provider, host, port: Number(port) || 0, user, password, api_key: key, from }),
      })
      onDone()
    } catch (err) {
      setProblem(fault(err))
      setBusy(false)
    }
  }
  const field = (k: string) => 'field' + (problem?.field === k ? ' error' : '')
  const help = (k: string, text: string) =>
    problem?.field === k ? (
      <span className="field-message" role="alert">
        {problem.text}
      </span>
    ) : (
      <span className="t-note">{text}</span>
    )
  const typed = (set: (v: string) => void) => (e: React.ChangeEvent<HTMLInputElement>) => {
    set(e.target.value)
    setProblem(null)
  }
  return (
    <Dialog
      wide
      title="Send alerts by email"
      busy={busy}
      onClose={onClose}
      onSubmit={submit}
      footer={
        <>
          {problem && !problem.field && (
            <span className="field-message grow" role="alert">
              {problem.text}
            </span>
          )}
          <button className="btn quiet medium" type="button" disabled={busy} onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary medium" type="submit" disabled={busy || !from.trim()}>
            {busy && <Spinner />}
            {busy ? 'Saving…' : 'Save'}
          </button>
        </>
      }
    >
      <p>Every person on this server who has alert emails on gets one message each.</p>
      <div className="field">
        <span className="field-label">Send through</span>
        <div className="seg" role="radiogroup" aria-label="Send through">
          {(['smtp', 'resend'] as const).map((p) => (
            <button key={p} type="button" className={provider === p ? 'on' : ''} role="radio" aria-checked={provider === p} onClick={() => setProvider(p)}>
              {p === 'smtp' ? 'SMTP server' : 'Resend'}
            </button>
          ))}
        </div>
      </div>
      {provider === 'smtp' ? (
        <div className="form-grid two">
          <label className={field('host')}>
            <span className="field-label">Server</span>
            <input className="input mono" placeholder="smtp.example.com" value={host} onChange={typed(setHost)} spellCheck={false} autoCapitalize="off" />
            {help('host', 'Your mail provider’s SMTP host.')}
          </label>
          <label className={field('port')}>
            <span className="field-label">Port</span>
            <input className="input mono" inputMode="numeric" value={port} onChange={typed(setPort)} />
            {help('port', '587 upgrades to TLS with STARTTLS. 465 uses TLS from the start.')}
          </label>
          <label className={field('user')}>
            <span className="field-label">Username</span>
            <input className="input mono" value={user} onChange={typed(setUser)} spellCheck={false} autoCapitalize="off" autoComplete="off" />
            {help('user', 'Leave it empty for a server that takes mail without signing in.')}
          </label>
          <label className={field('password')}>
            <span className="field-label">Password</span>
            <input
              className="input"
              type="password"
              value={password}
              placeholder={has('password') ? 'Saved. Type a new one to replace it.' : ''}
              onChange={typed(setPassword)}
              autoComplete="new-password"
            />
            {help('password', 'Stored encrypted. Pier never shows it again.')}
          </label>
        </div>
      ) : (
        <label className={field('api_key')}>
          <span className="field-label">API key</span>
          <input
            className="input mono"
            type="password"
            value={key}
            placeholder={has('api_key') ? 'Saved. Paste a new one to replace it.' : 're_…'}
            onChange={typed(setKey)}
            autoComplete="off"
          />
          {help('api_key', 'A Resend key that can send email. Stored encrypted; Pier never shows it again.')}
        </label>
      )}
      <label className={field('from')}>
        <span className="field-label">From</span>
        <input className="input mono" placeholder="Pier <alerts@example.com>" value={from} onChange={typed(setFrom)} spellCheck={false} autoCapitalize="off" />
        {help('from', provider === 'resend' ? 'An address on a domain you verified with Resend.' : 'An address your mail server lets you send from.')}
      </label>
    </Dialog>
  )
}

function UrlDialog({ kind, saved, onClose, onDone }: {
  kind: 'chat' | 'webhook'
  saved?: Channel
  onClose: () => void
  onDone: (signingSecret?: string) => void
}) {
  const [url, setUrl] = useState('')
  const [problem, setProblem] = useState<Fault>(null)
  const [busy, setBusy] = useState(false)
  const has = !!saved?.set.includes('url')
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setProblem(null)
    try {
      const r = await api<{ signing_secret?: string }>(`/api/alerts/${kind}`, { method: 'PUT', body: JSON.stringify({ url: url.trim() }) })
      onDone(r.signing_secret)
    } catch (err) {
      setProblem(fault(err))
      setBusy(false)
    }
  }
  const chat = kind === 'chat'
  return (
    <Dialog
      wide
      title={chat ? 'Post alerts in Slack or Discord' : 'Post alerts to a webhook'}
      busy={busy}
      onClose={onClose}
      onSubmit={submit}
      footer={
        <>
          <button className="btn quiet medium" type="button" disabled={busy} onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary medium" type="submit" disabled={busy || !url.trim()}>
            {busy && <Spinner />}
            {busy ? 'Saving…' : 'Save'}
          </button>
        </>
      }
    >
      <p>
        {chat
          ? 'In Slack, add an incoming webhook to the channel. In Discord, open the channel’s settings, then Integrations, then Webhooks. Pier tells which it is from the URL, and posts in each one’s own format.'
          : 'Pier POSTs each alert to this URL as JSON. The first time, it makes a signing secret and shows it to you once, so the receiver can check each alert came from Pier.'}
      </p>
      <label className={'field' + (problem ? ' error' : '')}>
        <span className="field-label">{chat ? 'Webhook URL' : 'URL'}</span>
        <input
          className="input mono"
          value={url}
          placeholder={has ? 'Saved. Paste a new URL to replace it.' : chat ? 'https://hooks.slack.com/services/…' : 'https://example.com/pier-alerts'}
          onChange={(e) => {
            setUrl(e.target.value)
            setProblem(null)
          }}
          spellCheck={false}
          autoCapitalize="off"
          autoComplete="off"
        />
        {problem ? (
          <span className="field-message" role="alert">
            {problem.text}
          </span>
        ) : (
          <span className="t-note">The URL is a secret. It's stored encrypted, and Pier never shows it again.</span>
        )}
      </label>
    </Dialog>
  )
}

// SigningDialog shows a webhook's new signing secret, the only time it's shown.
function SigningDialog({ secret, onClose }: { secret: string; onClose: () => void }) {
  return (
    <Dialog
      wide
      title="Copy the signing secret"
      onClose={onClose}
      footer={
        <button className="btn primary medium" onClick={onClose}>
          Done
        </button>
      }
    >
      <p>Pier shows it this once. Keep it where your receiver can read it; if it's lost, make a new one.</p>
      <div className="secret-box">
        <span className="mono">{secret}</span>
        <button className="icon-btn bare" onClick={() => copy(secret, 'the secret')} aria-label="Copy the secret" data-tip="Copy">
          <Copy />
        </button>
      </div>
      <p className="t-caption t-muted">
        Each alert carries <span className="mono">X-Pier-Signature: sha256=…</span>, the HMAC-SHA256 of the request body keyed
        with this secret, in hex. Work it out over the body as it arrived, and compare the two in constant time.
      </p>
    </Dialog>
  )
}
