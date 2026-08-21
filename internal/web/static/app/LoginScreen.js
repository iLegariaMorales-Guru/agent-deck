// LoginScreen.js -- shown in place of the app shell whenever an /api/ call
// comes back 401 (see api.js -> authRequiredSignal).
//
// Exchanges the server's access token for a session cookie via POST
// /api/login. This is the real fix for iOS Home Screen web apps: instead of
// relying on a token that has to survive a trip through the URL or
// localStorage into a context that may not share storage with the Safari
// tab it was installed from (see the push-notif-mobile-auth memory), each
// browser context — tab or standalone app — logs in for itself. The cookie
// it gets back is scoped to that context alone.
import { html } from 'htm/preact'
import { useState } from 'preact/hooks'
import { authHeaders } from './api.js'

export function LoginScreen() {
  const [token, setToken] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const submit = async (e) => {
    e.preventDefault()
    if (busy) return
    setBusy(true)
    setError('')
    try {
      const res = await fetch('/api/login', {
        method: 'POST',
        headers: authHeaders(),
        body: JSON.stringify({ token }),
      })
      if (!res.ok) {
        const data = await res.json().catch(() => null)
        setError(data?.error?.message || 'Incorrect access token or PIN')
        setBusy(false)
        return
      }
      // Reload rather than flipping signals by hand: the whole boot
      // sequence (menu load, SSE, service worker) starts clean with the
      // session cookie already in place.
      window.location.reload()
    } catch (_err) {
      setError('Network error — is the server reachable?')
      setBusy(false)
    }
  }

  return html`
    <div class="login-screen">
      <form class="dialog login-card" onSubmit=${submit}>
        <div class="dh">
          <span class="kicker">AGENT DECK</span>
          <div class="t">Sign in</div>
        </div>
        <div class="db">
          <div class="field">
            <label for="login-token">Access token or PIN</label>
            <input
              id="login-token"
              type="password"
              autofocus
              autocomplete="current-password"
              placeholder="Access token or PIN"
              value=${token}
              onInput=${(e) => setToken(e.target.value)}
            />
          </div>
          ${error && html`<div class="login-error">${error}</div>`}
        </div>
        <div class="df">
          <button type="submit" class="btn primary" disabled=${busy || !token}>
            ${busy ? 'Signing in…' : 'Sign in'}
          </button>
        </div>
      </form>
    </div>
  `
}
