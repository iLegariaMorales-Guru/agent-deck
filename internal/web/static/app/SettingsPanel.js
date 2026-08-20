// SettingsPanel.js -- Settings surface (reads GET /api/settings).
// Restyled (PR-B) to use the bundle's `.kv` row layout from app.css.
import { html } from 'htm/preact'
import { useState, useEffect } from 'preact/hooks'
import { settingsSignal, pushConfigSignal, pushSubscribedSignal, pushBusySignal } from './state.js'
import { authHeaders } from './api.js'
import { subscribePush, unsubscribePush } from './push.js'

// PushSection reads signals that main.js's initPush() populates at boot --
// it never fetches on its own, so opening the drawer before that call
// resolves just shows the "loading" state below, not a duplicate request.
function PushSection() {
  const config = pushConfigSignal.value
  if (!config) return null

  if (!config.enabled) {
    return html`
      <div class="kv" data-testid="settings-push"><span class="k">push notifications</span><span class="v warn">off on server</span></div>
      <div style="font-family: var(--mono); font-size: 11px; color: var(--muted); margin-top: 4px;">
        Start the server with <code>agent-deck web --push</code> to turn this on.
      </div>
    `
  }

  const browserSupported = 'serviceWorker' in navigator && 'PushManager' in window
  if (!browserSupported) {
    return html`
      <div class="kv" data-testid="settings-push"><span class="k">push notifications</span><span class="v warn">unsupported here</span></div>
      <div style="font-family: var(--mono); font-size: 11px; color: var(--muted); margin-top: 4px;">
        iPhone/iPad: Share → Add to Home Screen, then open the app icon -- Safari
        only allows push notifications for installed home-screen apps, not a browser tab.
      </div>
    `
  }

  const subscribed = pushSubscribedSignal.value
  const busy = pushBusySignal.value
  return html`
    <div class="kv" data-testid="settings-push">
      <span class="k">push notifications</span>
      <span class="v" style="display: flex; align-items: center; gap: 8px;">
        <span style="font-family: var(--mono); font-size: 11px; color: var(--text-dim);">
          ${busy ? 'working…' : subscribed ? 'on' : 'off'}
        </span>
        <div class=${`switch ${subscribed ? 'on' : ''}`}
             data-testid="settings-push-switch"
             onClick=${() => { if (!busy) (subscribed ? unsubscribePush() : subscribePush()) }}/>
      </span>
    </div>
  `
}

export function SettingsPanel() {
  const [error, setError] = useState(null)
  const settings = settingsSignal.value

  useEffect(() => {
    if (settings) return
    fetch('/api/settings', { headers: authHeaders() })
      .then(r => { if (!r.ok) throw new Error('Settings request failed: ' + r.status); return r.json() })
      .then(data => { settingsSignal.value = data })
      .catch(err => setError(err.message || 'Failed to load settings'))
  }, [])

  if (error) {
    return html`<div style="font-family: var(--mono); font-size: 12px; color: var(--tn-red);">${error}</div>`
  }
  if (!settings) {
    return html`<div style="font-family: var(--mono); font-size: 12px; color: var(--muted);">Loading…</div>`
  }
  return html`
    <div data-testid="settings-panel" style="display: flex; flex-direction: column; gap: 2px;">
      <div class="kv" data-testid="settings-profile"><span class="k">profile</span><span class="v">${settings.profile || 'default'}</span></div>
      <div class="kv" data-testid="settings-version"><span class="k">version</span><span class="v">${settings.version || 'unknown'}</span></div>
      <div class="kv" data-testid="settings-read-only"><span class="k">read-only</span><span class=${`v ${settings.readOnly ? 'warn' : 'ok'}`}>${settings.readOnly ? 'yes' : 'no'}</span></div>
      <div class="kv" data-testid="settings-web-mutations"><span class="k">web mutations</span><span class=${`v ${settings.webMutations ? 'ok' : 'warn'}`}>${settings.webMutations ? 'enabled' : 'disabled'}</span></div>
      <div class="kv" data-testid="settings-hidden-tools"><span class="k">hidden tools</span><span class="v">${(settings.hiddenTools || []).join(', ') || 'none'}</span></div>
      <div class="kv" data-testid="settings-picker-tools"><span class="k">picker tools</span><span class="v">${(settings.pickerTools || []).join(', ') || 'loading…'}</span></div>
      <div class="kv" data-testid="settings-trusted-domains"><span class="k">trusted domains</span><span class="v">${(settings.trustedDomains || []).join(', ') || 'none'}</span></div>
      <div class="kv" data-testid="settings-confirm-link-open"><span class="k">link confirm</span><span class=${`v ${settings.confirmLinkOpen === false ? 'warn' : 'ok'}`}>${settings.confirmLinkOpen === false ? 'off' : 'on'}</span></div>
      <${PushSection}/>
      <div style="font-family: var(--mono); font-size: 11px; color: var(--muted); margin-top: 8px;">
        Edit <code>~/.config/agent-deck/config.toml</code> (<code>[ui] hidden_tools</code>, <code>[web] trusted_domains</code>) or use TUI Settings → Visible tools…
      </div>
    </div>
  `
}
