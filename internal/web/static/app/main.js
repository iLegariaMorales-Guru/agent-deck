// main.js -- Preact app entry point and full boot sequence
// Handles: auth token extraction, SSE connection, route sync, service worker registration
import { render, html } from 'htm/preact'
import { App } from './App.js'
import { apiFetch } from './api.js'
import { initPush } from './push.js'
import {
  sessionsSignal,
  sessionsLoadedSignal,
  selectedIdSignal,
  connectionSignal,
  authTokenSignal,
  commandCenterSignal,
} from './state.js'
import { addToast } from './Toast.js'

// ---------- Auth token extraction ----------
//
// The token is persisted to localStorage (not just kept in-memory) so an
// "Add to Home Screen" icon keeps working. iOS captures whatever URL the
// tab is showing *at the moment the share sheet opens*, and the token gets
// stripped from that URL within a tick of page load (below) -- so a
// bookmarked/home-screen relaunch would otherwise always hit a bare `/`
// with no token, 401 on every /api/ call, and render as a silently empty
// session list (no visible error; see handlers auth). Storing it here means
// a URL with `?token=` only needs to be opened once, from any entry point.

const AUTH_TOKEN_STORAGE_KEY = 'agentdeck.token'

;(function extractAuthToken() {
  const params = new URLSearchParams(window.location.search)
  const token = params.get('token')

  if (!token) {
    // No token in this URL -- fall back to a previously saved one so a
    // home-screen icon or bookmark opened without ?token= still works.
    try {
      const stored = localStorage.getItem(AUTH_TOKEN_STORAGE_KEY)
      if (stored) authTokenSignal.value = stored
    } catch (_) {
      // localStorage unavailable (private mode); nothing to fall back to
    }
    return
  }

  authTokenSignal.value = token
  try {
    localStorage.setItem(AUTH_TOKEN_STORAGE_KEY, token)
  } catch (_) {
    // private mode; token still works for this tab, just won't survive a relaunch
  }

  // Strip token from URL so it isn't logged by the server or leaked via Referer header
  params.delete('token')
  const cleanSearch = params.toString()
  const cleanPath = window.location.pathname + (cleanSearch ? '?' + cleanSearch : '') + window.location.hash
  history.replaceState(null, '', cleanPath)

  // Prevent token from appearing in Referer headers on any subsequent navigation
  let meta = document.querySelector('meta[name="referrer"]')
  if (!meta) {
    meta = document.createElement('meta')
    meta.name = 'referrer'
    document.head.appendChild(meta)
  }
  meta.content = 'no-referrer'
})()

// ---------- SSE connection ----------

let _menuSource = null

export function startSSE() {
  if (_menuSource) return

  const token = authTokenSignal.value
  const url = token
    ? '/events/menu?token=' + encodeURIComponent(token)
    : '/events/menu'

  const source = new EventSource(url)
  _menuSource = source

  // CRITICAL: The Go server emits SSE events with event type "menu"
  // (see handlers_events.go: writeSSEEvent(w, flusher, "menu", snapshot))
  source.addEventListener('menu', (event) => {
    try {
      const snapshot = JSON.parse(event.data)
      if (snapshot && Array.isArray(snapshot.items)) {
        sessionsSignal.value = snapshot.items
        // POL-1: first SSE snapshot counts as loaded. Skeleton unmounts
        // even if the snapshot is empty — the server has spoken.
        sessionsLoadedSignal.value = true
      }
      connectionSignal.value = 'connected'
    } catch (_) {
      // malformed JSON; keep current connection state
    }
  })

  source.addEventListener('error', () => {
    connectionSignal.value = 'disconnected'
    // EventSource auto-reconnects; we'll update to 'connected' on next successful "menu" event
  })
}

export function stopSSE() {
  if (_menuSource) {
    _menuSource.close()
    _menuSource = null
  }
  if (_ccSource) {
    _ccSource.close()
    _ccSource = null
  }
}

// ---------- Command Center SSE ----------
// A second stream alongside the menu SSE, carrying the synthesized cross-
// project god-view snapshot. Live by construction (fingerprint-diffed
// server-side), so the panel never polls. recentlyCompleted entries drive
// "✅ X just finished" notifications.

let _ccSource = null
// Track which completion ids we've already toasted so a steady-state re-emit
// of the same snapshot (or a reconnect) doesn't re-fire notifications.
const _ccSeenCompletions = new Set()

export function startCommandCenterSSE() {
  if (_ccSource) return

  const token = authTokenSignal.value
  const url = token
    ? '/events/command-center?token=' + encodeURIComponent(token)
    : '/events/command-center'

  const source = new EventSource(url)
  _ccSource = source

  // CRITICAL: the Go server emits this event with type "command-center"
  // (handlers_command_center.go: writeSSEEvent(w, flusher, "command-center", snapshot)).
  source.addEventListener('command-center', (event) => {
    try {
      const snapshot = JSON.parse(event.data)
      if (snapshot && typeof snapshot === 'object') {
        commandCenterSignal.value = snapshot
        const done = Array.isArray(snapshot.recentlyCompleted) ? snapshot.recentlyCompleted : []
        for (const c of done) {
          const key = (c && (c.id || '')) + ':' + (c && (c.at || ''))
          if (_ccSeenCompletions.has(key)) continue
          _ccSeenCompletions.add(key)
          if (c && c.title) addToast(`✅ ${c.title} just finished`, 'success')
        }
        // Bound the seen-set so it can't grow unbounded over a long session.
        if (_ccSeenCompletions.size > 200) {
          _ccSeenCompletions.clear()
        }
      }
    } catch (_) {
      // malformed JSON; ignore
    }
  })

  // The command-center stream shares the connection-state signal via the menu
  // stream; we don't flip it here to avoid fighting the menu reconnect logic.
}

// ---------- Initial menu load + SSE kick-off ----------

export async function loadMenu() {
  try {
    const data = await apiFetch('GET', '/api/menu')
    sessionsSignal.value = data.items || []
    // POL-1: first real data arrived — unmount the skeleton. Do NOT set
    // this in the catch branch; the skeleton is the correct state when
    // we're offline.
    sessionsLoadedSignal.value = true
    startSSE()
    startCommandCenterSSE()
  } catch (_) {
    connectionSignal.value = 'disconnected'
    // Still start SSE so it can reconnect when server comes back
    startSSE()
    startCommandCenterSSE()
  }
}

// ---------- Route sync: URL -> selectedIdSignal ----------

export function applyRouteSelection() {
  const path = window.location.pathname || '/'
  if (path.startsWith('/s/')) {
    const raw = path.slice(3)
    if (raw && !raw.includes('/')) {
      try {
        selectedIdSignal.value = decodeURIComponent(raw)
      } catch (_) {
        selectedIdSignal.value = null
      }
      return
    }
  }
  // Don't force-clear selection at boot if no /s/ path; leave it null
}

// ---------- Mobile keyboard viewport fix ----------
//
// iOS's on-screen keyboard doesn't shrink `100dvh` -- dvh only tracks the
// Safari toolbar collapsing/expanding, not the IME. Without this, `.app`
// (see app.css) stays full-height under the keyboard, and anything
// anchored near the bottom of the grid (terminal input line, a composer)
// keeps rendering behind it -- you type and see nothing. VisualViewport
// reports the actually-visible area, so mirror it into a CSS var .app can
// size itself to. This is only a best-effort first line of defense: iOS
// is documented to under-report or skip this resize event specifically
// for standalone/Home-Screen-installed web apps (this app's own install
// mode), so it alone recovers "a little" of the space, not all of it --
// installKeyboardScrollFix below is the mechanism that actually closes
// the gap regardless of whether this number is trustworthy. No-op where
// VisualViewport is unsupported (--app-vh stays unset, .app's `var(...,
// 100dvh)` fallback in app.css applies) -- desktop browsers included.
function installKeyboardViewportFix() {
  const vv = window.visualViewport
  if (!vv) return
  const root = document.documentElement
  function sync() {
    root.style.setProperty('--app-vh', vv.height + 'px')
  }
  vv.addEventListener('resize', sync)
  sync()
}

// ---------- Mobile keyboard scroll-into-view fix ----------
//
// The fix above shrinks .app when VisualViewport is trustworthy, but this
// app deliberately keeps #app-root pinned with `position: fixed` and
// `body { overflow: hidden }` so nothing scrolls at rest -- every pane
// scrolls internally instead (see app.css). That also means there is
// normally nothing for the browser's native "scroll the focused input
// above the keyboard" behavior to move: no overflow exists to scroll.
//
// Rather than compute the keyboard's exact pixel height ourselves (the
// one number VisualViewport won't reliably give us in this app's install
// mode), open up real, standards-compliant scroll room ONLY while a text
// field is focused: drop #app-root out of `fixed` into normal document
// flow and grow a spacer below it, so the document genuinely has
// somewhere to scroll to. Then let the browser's native `scrollIntoView`
// -- which reasons from actual layout geometry, not our guess -- bring
// the focused field above the keyboard. Reverts to the pinned, no-scroll
// state on blur, so nothing changes for the at-rest (no keyboard) case.
const APP_ROOT_FIXED_CSS = 'position:fixed;inset:0;z-index:10;'
const APP_ROOT_FLOW_CSS = 'position:relative;z-index:10;'
// Generous worst-case iPhone keyboard height (incl. the QuickType/predictive
// bar) -- doesn't need to be exact, scrollIntoView only needs "enough room
// exists below the field," not a precise number.
const KEYBOARD_SCROLL_SPACER_HEIGHT = '340px'

function isTextEntryTarget(el) {
  return !!el && !!el.tagName &&
    (el.tagName === 'TEXTAREA' || el.tagName === 'INPUT' || el.isContentEditable)
}

function installKeyboardScrollFix(root) {
  if (!root) return
  let spacer = null
  function ensureSpacer() {
    if (!spacer) {
      spacer = document.createElement('div')
      spacer.setAttribute('aria-hidden', 'true')
      spacer.style.cssText = 'height:0;flex:none;'
      document.body.appendChild(spacer)
    }
    return spacer
  }

  document.addEventListener('focusin', (e) => {
    if (!isTextEntryTarget(e.target)) return
    root.style.cssText = APP_ROOT_FLOW_CSS
    document.body.style.overflowY = 'auto'
    ensureSpacer().style.height = KEYBOARD_SCROLL_SPACER_HEIGHT
    // iOS animates the keyboard in over ~250ms; scrolling before it
    // settles measures against the pre-keyboard layout and undershoots.
    setTimeout(() => {
      if (document.activeElement === e.target) {
        e.target.scrollIntoView({ block: 'end', behavior: 'smooth' })
      }
    }, 300)
  }, { capture: true })

  document.addEventListener('focusout', (e) => {
    if (!isTextEntryTarget(e.target)) return
    // Deferred + re-checked: a focus hop between two text fields (e.g. tab
    // order in a dialog) fires focusout/focusin back-to-back -- don't
    // tear the flow state down mid-hop only to rebuild it immediately.
    setTimeout(() => {
      if (isTextEntryTarget(document.activeElement)) return
      window.scrollTo({ top: 0, behavior: 'smooth' })
      if (spacer) spacer.style.height = '0'
      document.body.style.overflowY = ''
      root.style.cssText = APP_ROOT_FIXED_CSS
    }, 50)
  }, { capture: true })
}

// ---------- Service worker registration ----------

export function registerServiceWorker() {
  if (!('serviceWorker' in navigator)) return

  function doRegister() {
    navigator.serviceWorker.register('/sw.js', { scope: '/' }).catch(() => {
      // SW registration failure is non-fatal; app works without it
    })
  }

  if (document.readyState === 'complete' || document.readyState === 'interactive') {
    doRegister()
  } else {
    window.addEventListener('load', doRegister, { once: true })
  }
}

// ---------- Boot sequence ----------

const root = document.getElementById('app-root')
if (root) {
  root.style.cssText = APP_ROOT_FIXED_CSS
  applyRouteSelection()
  loadMenu()
  installKeyboardViewportFix()
  installKeyboardScrollFix(root)
  registerServiceWorker()
  initPush()
  render(html`<${App} />`, root)
}
