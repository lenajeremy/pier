import { useState, type FormEvent } from 'react'
import { api } from '../lib/api'
import { useData } from '../lib/data'
import { Info } from '../components/Icons'
import { confirm } from '../components/Dialog'
import { toast } from '../components/Toast'
import { PageHeading, Problem, Skeleton } from '../components/ui'

type Addresses = { address: string; base: string }
type Setup = { saved: Addresses; active: Addresses; pending: boolean; ip: string }
const message = (e: unknown) => (e instanceof Error ? e.message : "Can't reach pierd.")
const good = (result: string) => /^(Matches|Local)/.test(result)

// Settings is K4: where the server is reached, and the owner's password.
export function Settings() {
  const { data, error, reload } = useData<Setup>('/api/settings')
  const [draft, setDraft] = useState<Addresses | null>(null)
  const [dns, setDns] = useState<Addresses | null>(null)
  const [problem, setProblem] = useState('')
  const [busy, setBusy] = useState('')
  const [current, setCurrent] = useState('')
  const [password, setPassword] = useState('')
  const [again, setAgain] = useState('')
  const [wrong, setWrong] = useState<{ field: string; text: string } | null>(null)

  const values = draft ?? data?.saved ?? { address: '', base: '' }
  const dirty = !!draft && !!data && (draft.address !== data.saved.address || draft.base !== data.saved.base)

  const save = async (e: FormEvent) => {
    e.preventDefault()
    setBusy('save')
    setProblem('')
    setDns(null)
    try {
      await api('/api/settings', { method: 'PUT', body: JSON.stringify(values) })
      await reload()
      setDraft(null)
      toast('Saved the addresses. They take effect when pierd restarts.')
    } catch (err) {
      setProblem(message(err))
    } finally {
      setBusy('')
    }
  }
  const check = async () => {
    setBusy('dns')
    setProblem('')
    try {
      setDns(await api<Addresses>('/api/settings/dns', { method: 'POST' }))
    } catch (err) {
      setProblem(message(err))
    } finally {
      setBusy('')
    }
  }
  const change = (e: FormEvent) => {
    e.preventDefault()
    if (password.length < 12) return setWrong({ field: 'new', text: 'Use at least 12 characters.' })
    if (password !== again) return setWrong({ field: 'again', text: "This doesn't match the new password." })
    setWrong(null)
    confirm({
      title: 'Change your password?',
      body: (
        <p>
          Every browser and linked computer is signed out, this one included. You sign in again with the new password, and
          approve each computer again with pier login.
        </p>
      ),
      action: 'Change password',
      working: 'Changing…',
      run: async () => {
        await api('/api/settings/password', { method: 'POST', body: JSON.stringify({ current, new: password }) })
        window.dispatchEvent(new Event('pier:signed-out'))
      },
    })
  }

  return (
    <main className="main global">
      <PageHeading title="Settings" note="Where this server is reached, and how you sign in to it." />
      {error && <Problem>{error.message}</Problem>}
      {!data && !error && (
        <div className="panel">
          <Skeleton rows={3} tall />
        </div>
      )}
      {data && (
        <>
          <form className="panel form-panel" onSubmit={save} noValidate>
            <h2 className="t-small t-strong">Server addresses</h2>
            {data.pending && (
              <div className="alert info">
                <Info />
                <div>
                  <span className="t-small t-strong">Restart pierd to use the saved addresses</span>
                  <span className="t-caption t-muted">
                    Until then the dashboard is at {data.active.address}, and projects get addresses under{' '}
                    {data.active.base}.
                  </span>
                </div>
              </div>
            )}
            <div className="form-grid two">
              <label className="field">
                <span className="field-label">Dashboard address</span>
                <input
                  className="input mono"
                  value={values.address}
                  spellCheck={false}
                  autoCapitalize="off"
                  onChange={(e) => {
                    setDraft({ ...values, address: e.target.value.trim() })
                    setDns(null)
                  }}
                />
                {dns ? <DnsLine result={dns.address} /> : <span className="t-note">Where you open this dashboard.</span>}
              </label>
              <label className="field">
                <span className="field-label">Base domain</span>
                <input
                  className="input mono"
                  value={values.base}
                  spellCheck={false}
                  autoCapitalize="off"
                  onChange={(e) => {
                    setDraft({ ...values, base: e.target.value.trim() })
                    setDns(null)
                  }}
                />
                {dns ? (
                  <DnsLine result={dns.base} />
                ) : (
                  <span className="t-note">
                    Every project gets an address under it, like blog.{values.base || 'example.com'}.
                  </span>
                )}
              </label>
            </div>
            {problem && (
              <span className="field-message" role="alert">
                {problem}
              </span>
            )}
            <div className="form-foot">
              <span className="t-note">
                {dirty
                  ? 'Saved addresses take effect when pierd restarts.'
                  : data.ip
                    ? `Check DNS looks up both names and compares them with this server's IP, ${data.ip}.`
                    : 'Set the server’s IP with pierd init to check DNS from here.'}
              </span>
              <button className="btn quiet" type="button" disabled={!!busy || dirty} onClick={check}>
                {busy === 'dns' ? 'Checking…' : 'Check DNS'}
              </button>
              <button className="btn primary" type="submit" disabled={!!busy || !dirty}>
                {busy === 'save' ? 'Saving…' : 'Save addresses'}
              </button>
            </div>
          </form>

          <form className="panel form-panel" onSubmit={change} noValidate>
            <h2 className="t-small t-strong">Password</h2>
            <div className="form-grid">
              <label className="field">
                <span className="field-label">Current password</span>
                <input type="password" className="input" value={current} autoComplete="current-password" onChange={(e) => setCurrent(e.target.value)} />
              </label>
              <label className={'field' + (wrong?.field === 'new' ? ' error' : '')}>
                <span className="field-label">New password</span>
                <input
                  type="password"
                  className="input"
                  value={password}
                  autoComplete="new-password"
                  onChange={(e) => {
                    setPassword(e.target.value)
                    setWrong(null)
                  }}
                />
                {wrong?.field === 'new' ? (
                  <span className="field-message" role="alert">
                    {wrong.text}
                  </span>
                ) : (
                  <span className="t-note">At least 12 characters.</span>
                )}
              </label>
              <label className={'field' + (wrong?.field === 'again' ? ' error' : '')}>
                <span className="field-label">New password, again</span>
                <input
                  type="password"
                  className="input"
                  value={again}
                  autoComplete="new-password"
                  onChange={(e) => {
                    setAgain(e.target.value)
                    setWrong(null)
                  }}
                />
                {wrong?.field === 'again' && (
                  <span className="field-message" role="alert">
                    {wrong.text}
                  </span>
                )}
              </label>
            </div>
            <div className="form-foot">
              <span className="t-note">
                Changing it signs out every browser and linked computer. If you've lost it, run{' '}
                <span className="mono">pierd reset-password</span> on the server.
              </span>
              <button className="btn quiet" type="submit" disabled={!current || !password || !again}>
                Change password
              </button>
            </div>
          </form>
        </>
      )}
    </main>
  )
}

function DnsLine({ result }: { result: string }) {
  return (
    <span className="t-note check-line" role="status">
      <i className={'swatch ' + (good(result) ? 'ready' : 'warning')} />
      {result}
    </span>
  )
}
