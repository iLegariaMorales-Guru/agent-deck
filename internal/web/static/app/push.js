// push.js -- Web Push subscription lifecycle (VAPID).
//
// Server side is push_service.go / handlers_push.go: GET /api/push/config,
// POST /api/push/subscribe|unsubscribe|presence. sw.js owns the "push" and
// "notificationclick" service-worker events; this module owns getting a
// PushSubscription into the server's hands and keeping its ClientFocused
// flag honest. That flag is not cosmetic: push_service.go's
// shouldNotifySubscription only sends to a subscription whose
// ClientFocused is explicitly `false` -- `nil` ("unknown", the state before
// any presence ping lands) is treated the same as "focused" and never
// notified. So a presence ping right after subscribe, and on every
// visibilitychange/focus/blur afterwards, is load-bearing, not polish.
//
// iOS Safari note: `PushManager` only exists in a page opened from an
// installed home-screen icon (manifest display:"standalone"), never in a
// regular Safari tab, regardless of iOS version. supported() below reflects
// that; SettingsPanel.js surfaces it as a hint rather than failing silently.
import { apiFetch, authHeaders } from './api.js'
import { pushConfigSignal, pushSubscribedSignal, pushBusySignal, pushEndpointSignal } from './state.js'

function supported() {
  return 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window
}

function urlBase64ToUint8Array(base64String) {
  const padding = '='.repeat((4 - (base64String.length % 4)) % 4)
  const base64 = (base64String + padding).replace(/-/g, '+').replace(/_/g, '/')
  const raw = atob(base64)
  const out = new Uint8Array(raw.length)
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i)
  return out
}

// Presence pings are frequent (every tab focus change) and best-effort, so
// they go through a bare authenticated fetch rather than apiFetch -- a
// flaky network shouldn't toast on every alt-tab.
async function sendPresence(endpoint, focused) {
  if (!endpoint) return
  try {
    await fetch('/api/push/presence', {
      method: 'POST',
      headers: authHeaders(),
      body: JSON.stringify({ endpoint, focused }),
    })
  } catch (_) {
    // best-effort; a missed ping just delays a notification, never breaks one
  }
}

let visibilityWired = false
function wireVisibility() {
  if (visibilityWired) return
  visibilityWired = true
  const ping = () => sendPresence(pushEndpointSignal.value, document.visibilityState === 'visible')
  document.addEventListener('visibilitychange', ping)
  window.addEventListener('focus', ping)
  window.addEventListener('blur', ping)
}

// initPush loads the server's push config and, if this browser already
// holds a subscription from a previous visit, re-syncs the signals and
// pings presence without prompting the user again. Call once at boot.
export async function initPush() {
  let config
  try {
    config = await apiFetch('GET', '/api/push/config')
  } catch (_) {
    return
  }
  pushConfigSignal.value = config

  if (!config?.enabled || !supported()) return

  try {
    const reg = await navigator.serviceWorker.ready
    const sub = await reg.pushManager.getSubscription()
    if (sub) {
      pushSubscribedSignal.value = true
      pushEndpointSignal.value = sub.endpoint
      wireVisibility()
      sendPresence(sub.endpoint, document.visibilityState === 'visible')
    }
  } catch (_) {
    // service worker not ready yet, or PushManager threw; leave defaults
  }
}

export async function subscribePush() {
  const config = pushConfigSignal.value
  if (!config?.enabled || !supported() || pushBusySignal.value) return

  pushBusySignal.value = true
  try {
    const permission = await Notification.requestPermission()
    if (permission !== 'granted') return

    const reg = await navigator.serviceWorker.ready
    let sub = await reg.pushManager.getSubscription()
    if (!sub) {
      sub = await reg.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: urlBase64ToUint8Array(config.vapidPublicKey),
      })
    }

    const json = sub.toJSON()
    await apiFetch('POST', '/api/push/subscribe', { endpoint: json.endpoint, keys: json.keys })

    pushSubscribedSignal.value = true
    pushEndpointSignal.value = sub.endpoint
    wireVisibility()
    await sendPresence(sub.endpoint, document.visibilityState === 'visible')
  } finally {
    pushBusySignal.value = false
  }
}

export async function unsubscribePush() {
  if (pushBusySignal.value) return
  pushBusySignal.value = true
  try {
    if (!supported()) return
    const reg = await navigator.serviceWorker.ready
    const sub = await reg.pushManager.getSubscription()
    const endpoint = sub?.endpoint || pushEndpointSignal.value
    if (sub) await sub.unsubscribe()
    if (endpoint) await apiFetch('POST', '/api/push/unsubscribe', { endpoint })
  } finally {
    pushSubscribedSignal.value = false
    pushEndpointSignal.value = ''
    pushBusySignal.value = false
  }
}
