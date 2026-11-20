import { useState, type FormEvent } from 'react'
import { api } from '../lib/api'
import { useData } from '../lib/data'
import { Link } from '../lib/router'
type Addresses = { address: string; base: string }
type Setup = { saved: Addresses; active: Addresses; pending: boolean; ip: string }
export function Settings() {
  const { data, error, reload } = useData<Setup>('/api/settings')
  const [draft, setDraft] = useState<Addresses | null>(null),
    [notice, msg] = useState(''),
    [problem, fail] = useState(''),
    [busy, wait] = useState(false)
  const [current, setCurrent] = useState(''),
    [password, setPassword] = useState(''),
    [confirmation, confirmPassword] = useState('')
  const values = draft ?? data?.saved ?? { address: '', base: '' }
  async function save(e: FormEvent) {
    e.preventDefault()
    wait(true)
    fail('')
    msg('')
    try {
      await api('/api/settings', { method: 'PUT', body: JSON.stringify(values) })
      await reload()
      setDraft(null)
      msg('Addresses saved.')
    } catch (e) {
      fail((e as Error).message)
    } finally {
      wait(false)
    }
  }
  async function dns() {
    wait(true)
    fail('')
    msg('')
    try {
      const r = await api<Addresses>('/api/settings/dns', { method: 'POST' })
      msg('Dashboard: ' + r.address + ' · Base domain: ' + r.base)
    } catch (e) {
      fail((e as Error).message)
    } finally {
      wait(false)
    }
  }
  async function change(e: FormEvent) {
    e.preventDefault()
    fail('')
    msg('')
    if (password !== confirmation) {
      fail('The new passwords must match.')
      return
    }
    wait(true)
    try {
      await api('/api/settings/password', { method: 'POST', body: JSON.stringify({ current, new: password }) })
      window.dispatchEvent(new Event('pier:signed-out'))
    } catch (e) {
      fail((e as Error).message)
    } finally {
      wait(false)
    }
  }
  return (
    <main className="main global operations">
      <header className="page-heading">
        <h1 className="t-title">Settings</h1>
      </header>
      {(error || problem) && (
        <p className="field-message" role="alert">
          {problem || error?.message}
        </p>
      )}
      {data && (
        <>
          <form className="panel ops-panel" onSubmit={save}>
            <h2>Server addresses</h2>
            {(['address', 'base'] as const).map((k) => (
              <label className="ops-field" key={k}>
                <span>{k === 'address' ? 'Dashboard address' : 'Base domain'}</span>
                <input
                  className="input mono"
                  required
                  value={values[k]}
                  onChange={(e) => setDraft({ ...values, [k]: e.target.value })}
                />
              </label>
            ))}
            <p className="t-note">
              {data.pending
                ? 'Restart pierd to use the saved addresses. Currently serving ' +
                  data.active.address +
                  ' and *.' +
                  data.active.base +
                  '.'
                : 'Saved addresses take effect when pierd restarts.'}
            </p>
            <div className="ops-actions">
              <button className="btn primary" disabled={busy}>
                Save addresses
              </button>
              <button className="btn quiet" type="button" disabled={busy || !!draft} onClick={dns}>
                Check DNS
              </button>
            </div>
            {notice && <p role="status">{notice}</p>}
          </form>
          <section className="panel ops-panel">
            <h2>Account and integrations</h2>
            <Link to="/integrations">GitHub App ↗</Link>
            <Link to="/members">Signed-in computers ↗</Link>
          </section>
          <form className="panel ops-panel" onSubmit={change}>
            <h2>Change password</h2>
            <label className="ops-field">
              <span>Current password</span>
              <input
                type="password"
                className="input"
                required
                value={current}
                autoComplete="current-password"
                onChange={(e) => setCurrent(e.target.value)}
              />
            </label>
            <label className="ops-field">
              <span>New password</span>
              <input
                type="password"
                className="input"
                minLength={12}
                required
                value={password}
                autoComplete="new-password"
                onChange={(e) => setPassword(e.target.value)}
              />
            </label>
            <label className="ops-field">
              <span>Confirm new password</span>
              <input
                type="password"
                className="input"
                required
                autoComplete="new-password"
                value={confirmation}
                onChange={(e) => confirmPassword(e.target.value)}
              />
            </label>
            <p className="t-note">Changing the password signs out all browsers and linked computers.</p>
            <div>
              <button className="btn quiet" disabled={busy}>
                Change password
              </button>
            </div>
          </form>
        </>
      )}
    </main>
  )
}
