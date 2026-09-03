// panes/PrismaticPane.js -- Web UI for Guru's Prismatic CNI integration
// monorepo, session-scoped like McpPane/SkillsPane.
//
// PR 1 of a phased build (see the design mockup this was built from):
// integration detection (GET /api/sessions/{id}/prismatic, internal/prismatic)
// + a hub view + quick-launch actions that prefill CreateSessionDialog.
// Deliberately does NOT talk to any Prismatic-specific API (prism CLI,
// Prismatic admin API, Guru API creds) — those land in later PRs:
//   PR 2: a credentials vault (QA/Prod Prism refresh tokens + Guru API creds)
//   PR 3: source-definition curl generation (parses createSource.ts + prism CLI)
//   PR 4: an investigate context picker (needs the prismatic-admin MCP)
// Everything here reads off data agent-deck already has or can derive
// structurally from the filesystem; "Deploy"/"tests"/"handoff" are just
// canned initial prompts for a normal session, not a new run type.
import { html } from 'htm/preact'
import { useEffect, useState } from 'preact/hooks'
import { selectedIdSignal, createSessionDialogSignal, createSessionPrefillSignal } from '../state.js'
import { menuModelSignal } from '../dataModel.js'
import { apiFetch } from '../api.js'

const TYPE_FILTERS = [
  { id: 'cni', label: 'CNI' },
  { id: 'component', label: 'Components' },
  { id: 'shared', label: 'Shared' },
  { id: 'other', label: 'Other' },
  { id: 'all', label: 'All' },
]

function typeLabel(t) {
  if (t === 'cni') return 'CNI'
  if (t === 'component') return 'COMP'
  if (t === 'shared') return 'LIB'
  return 'OTHER'
}

// launchQuickAction opens CreateSessionDialog prefilled with the
// integration's directory as the working dir and a canned prompt — the
// dialog itself still requires the user to review and hit "Create session"
// (nothing here bypasses that confirmation).
function launchQuickAction({ path, prompt, groupPath }) {
  createSessionPrefillSignal.value = { path, prompt, groupPath }
  createSessionDialogSignal.value = true
}

export function PrismaticPane() {
  const { sessions } = menuModelSignal.value
  const selectedId = selectedIdSignal.value
  const session = sessions.find(s => s.id === selectedId)

  if (!session) {
    return html`
      <div class="pris">
        <div class="pris-head"><div class="title">Prismatic</div></div>
        <div class="pris-empty">
          <div class="t">No session selected</div>
          <div class="m">Pick a session in the sidebar. If its project lives inside a Prismatic CNI monorepo, its integrations show up here.</div>
        </div>
      </div>
    `
  }

  return html`<${PrismaticPaneForSession} key=${session.id} session=${session}/>`
}

function PrismaticPaneForSession({ session }) {
  const [state, setState] = useState({ loading: true, supported: false, root: '', integrations: [], error: '' })
  const [filter, setFilter] = useState('cni')
  const [focusedDir, setFocusedDir] = useState('')
  const [env, setEnv] = useState('qa')

  useEffect(() => {
    let cancelled = false
    setState(s => ({ ...s, loading: true, error: '' }))
    apiFetch('GET', `/api/sessions/${encodeURIComponent(session.id)}/prismatic`)
      .then(resp => {
        if (cancelled) return
        setState({
          loading: false,
          supported: !!resp.supported,
          root: resp.root || '',
          integrations: resp.integrations || [],
          error: '',
        })
      })
      .catch(err => {
        if (cancelled) return
        setState({ loading: false, supported: false, root: '', integrations: [], error: err.message || 'failed to load' })
      })
    return () => { cancelled = true }
  }, [session.id])

  if (state.loading) {
    return html`
      <div class="pris">
        <div class="pris-head"><div class="title">Prismatic</div></div>
        <div class="pris-empty"><div class="m">Checking whether ${session.title} is inside a Prismatic monorepo…</div></div>
      </div>
    `
  }

  if (state.error) {
    return html`
      <div class="pris">
        <div class="pris-head"><div class="title">Prismatic</div></div>
        <div class="pris-empty"><div class="t">Couldn't check this session</div><div class="m">${state.error}</div></div>
      </div>
    `
  }

  if (!state.supported) {
    return html`
      <div class="pris">
        <div class="pris-head"><div class="title">Prismatic</div><div class="sub">${session.title}</div></div>
        <div class="pris-empty">
          <div class="t">Not a Prismatic checkout</div>
          <div class="m">
            ${session.title}'s project doesn't look like a Prismatic CNI monorepo —
            no sibling directory here has both a package.json and a src/ folder using
            @prismatic-io/spectral. Select a session whose project is inside one.
          </div>
        </div>
      </div>
    `
  }

  const counts = { all: state.integrations.length }
  for (const t of ['cni', 'component', 'shared', 'other']) {
    counts[t] = state.integrations.filter(i => i.type === t).length
  }
  const visible = filter === 'all' ? state.integrations : state.integrations.filter(i => i.type === filter)
  const focused = state.integrations.find(i => i.dir === focusedDir)
  const rootBase = state.root.split('/').filter(Boolean).pop() || state.root
  const groupPath = session.group && session.group !== 'default' ? session.group : ''

  return html`
    <div class="pris">
      <div class="pris-head">
        <div>
          <div class="title">Prismatic</div>
          <div class="sub">${rootBase} · ${state.integrations.length} integration${state.integrations.length === 1 ? '' : 's'}</div>
        </div>
      </div>

      <div class="pris-section">
        <div class="pris-section-head">
          <span class="kicker">Integrations</span>
          <span class="sub-kicker">${state.root}</span>
        </div>
        <div class="pris-filters">
          ${TYPE_FILTERS.map(f => html`
            <div key=${f.id} class=${`pris-chip ${f.id} ${filter === f.id ? 'active' : ''}`}
                 onClick=${() => setFilter(f.id)}>
              ${f.label} <b>${counts[f.id]}</b>
            </div>
          `)}
        </div>
        ${visible.length === 0
          ? html`<div style="font-family: var(--mono); font-size: 11.5px; color: var(--muted);">No "${filter}" integrations here.</div>`
          : html`
            <div class="pris-int-grid">
              ${visible.map(i => html`
                <button key=${i.dir} class=${`pris-int-tile ${focusedDir === i.dir ? 'on' : ''}`}
                        title=${i.description || i.dir}
                        onClick=${() => setFocusedDir(focusedDir === i.dir ? '' : i.dir)}>
                  <span class=${`tyb ${i.type}`}>${typeLabel(i.type)}</span>
                  <span class="nm">${i.dir}</span>
                  <span class="flows">${i.flowCount > 0 ? `${i.flowCount}f` : ''}</span>
                </button>
              `)}
            </div>
          `}
      </div>

      ${focused && html`
        <div class="pris-section">
          <div class="pris-section-head">
            <span class="kicker">Quick actions</span>
            <span class="sub-kicker">${focused.dir} → opens New Session, prefilled</span>
          </div>
          <div class="pris-quick-row">
            <button class="pris-qbtn" onClick=${() => launchQuickAction({ path: focused.path, groupPath })}>
              <span class="ic">▶</span>Open session here
            </button>
            ${(focused.type === 'cni' || focused.type === 'component') && html`
              <button class="pris-qbtn" onClick=${() => launchQuickAction({
                path: focused.path, groupPath, prompt: `/deploy-integration ${focused.dir} ${env}`,
              })}>
                <span class="ic">⇪</span>Deploy → ${env.toUpperCase()}
              </button>
            `}
            ${focused.type === 'cni' && html`
              <button class="pris-qbtn" onClick=${() => launchQuickAction({
                path: focused.path, groupPath, prompt: `/cni-tests ${focused.dir}`,
              })}>
                <span class="ic">✓</span>Add / scaffold tests
              </button>
            `}
            <button class="pris-qbtn" onClick=${() => launchQuickAction({
              path: focused.path, groupPath, prompt: '/integration-handoff-doc',
            })}>
              <span class="ic">⇥</span>Generate handoff doc
            </button>
            <div class=${`pris-env-chip ${env}`} onClick=${() => setEnv(env === 'qa' ? 'prod' : 'qa')}
                 title="Only changes the generated prompt text — credentials/deploy targeting land in a later PR">
              <span class="d"></span><b>${env.toUpperCase()}</b>
            </div>
          </div>
        </div>
      `}
    </div>
  `
}
