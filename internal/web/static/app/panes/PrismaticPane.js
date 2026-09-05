// panes/PrismaticPane.js -- Web UI for Guru's Prismatic CNI integration
// monorepo, session-scoped like McpPane/SkillsPane.
//
// PR 1 of a phased build (see the design mockup this was built from):
// integration detection (GET /api/sessions/{id}/prismatic, internal/prismatic)
// + a hub view + quick-launch actions that prefill CreateSessionDialog.
//   PR 2: a credentials vault (QA/Prod Prism refresh tokens + Guru API creds)
//   PR 3: source-definition curl generation (this file's CurlsDialog, backed
//         by POST /api/sessions/{id}/prismatic/curls + internal/prismatic)
//   PR 4: an investigate context picker (needs the prismatic-admin MCP)
// Everything here reads off data agent-deck already has or can derive
// structurally from the filesystem; "Deploy"/"tests"/"handoff" are just
// canned initial prompts for a normal session, not a new run type.
import { html } from 'htm/preact'
import { useEffect, useState } from 'preact/hooks'
import { selectedIdSignal, createSessionDialogSignal, createSessionPrefillSignal, mutationsEnabledSignal } from '../state.js'
import { menuModelSignal } from '../dataModel.js'
import { apiFetch } from '../api.js'
import { addToast } from '../Toast.js'

// Mirrors internal/prismatic/sourcedef.go's SourceDefCategories/TestTeams —
// static reference data, not worth a round trip to fetch.
const SOURCE_DEF_CATEGORIES = ['Other', 'Wiki/KB', 'Ticketing/Project Management', 'CRM', 'File Storage']
const TEST_TEAMS = [
  { id: 'd99f50f8-72f1-4d0f-a5bb-f14c2d6d9173', name: 'Caroline Test Team' },
  { id: '014dc5f6-9488-43fe-a892-206d276a7a9c', name: 'Guru HQ' },
]

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
  const [credStatus, setCredStatus] = useState(null)
  const [curlsDialogFor, setCurlsDialogFor] = useState(null) // the focused integration, while the wizard is open

  function refreshCredentials() {
    apiFetch('GET', '/api/prismatic/credentials')
      .then(resp => setCredStatus(resp))
      .catch(() => { /* CredentialsSection shows its own load-failed state via credStatus === null */ })
  }
  useEffect(refreshCredentials, [])

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
            ${focused.type === 'cni' && html`
              <button class="pris-qbtn" onClick=${() => setCurlsDialogFor(focused)}>
                <span class="ic">⎘</span>Generate source-def curls
              </button>
            `}
            <button class="pris-qbtn" onClick=${() => launchQuickAction({
              path: focused.path, groupPath, prompt: '/integration-handoff-doc',
            })}>
              <span class="ic">⇥</span>Generate handoff doc
            </button>
            <div class=${`pris-env-chip ${env}`} onClick=${() => setEnv(env === 'qa' ? 'prod' : 'qa')}
                 title="Only changes the generated prompt text — the launched session's Claude does its own auth via the deploy-integration skill, this doesn't inject env vars yet">
              <span class="d"></span><b>${env.toUpperCase()}</b>
            </div>
          </div>
        </div>
      `}

      <div class="pris-section">
        <div class="pris-section-head">
          <span class="kicker">Credentials</span>
          <span class="sub-kicker">used by "Generate source-def curls" above — never sent to a launched session</span>
        </div>
        <${CredentialsSection} status=${credStatus} onChange=${refreshCredentials}/>
      </div>

      ${curlsDialogFor && html`
        <${CurlsDialog} session=${session} integration=${curlsDialogFor} onClose=${() => setCurlsDialogFor(null)}/>
      `}
    </div>
  `
}

// CurlsDialog is the source-definition curl wizard, in one of two modes:
//   create — env -> category -> (prod only) test teams -> ipaas ID
//     resolution -> the paste-and-run curl sequence. Backed by
//     POST /api/sessions/{id}/prismatic/curls, which does its own cache ->
//     prism CLI -> manual-input resolution — this component just walks
//     whatever stage the backend hands back.
//   edit — fetch the already-registered definition (live GET against
//     Guru's admin API, POST /api/sessions/{id}/prismatic/existing), tweak
//     a few fields, then POST /api/prismatic/curls/update for a single PUT
//     curl that preserves every field this UI doesn't touch.
function CurlsDialog({ session, integration, onClose }) {
  const [mode, setMode] = useState('create')
  const [env, setEnv] = useState('qa')
  const [category, setCategory] = useState(SOURCE_DEF_CATEGORIES[0])
  const [teamIds, setTeamIds] = useState([TEST_TEAMS[0].id])
  // Extra teams beyond the two defaults, added by pasting a team id directly
  // (encoded or decoded — whichever form the admin API actually expects;
  // we don't validate the shape, we just pass through whatever's pasted).
  const [customTeams, setCustomTeams] = useState([])
  const [customTeamId, setCustomTeamId] = useState('')
  const [customTeamName, setCustomTeamName] = useState('')
  const [manualIpaasId, setManualIpaasId] = useState('')
  const [nameOverride, setNameOverride] = useState('')
  const [iconUrlOverride, setIconUrlOverride] = useState('')
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState(null) // the last response from the server
  const [copiedAll, setCopiedAll] = useState(false)

  // Edit-mode only: the fetched definition, and the editable fields seeded
  // from it. existing.status: 'idle' | 'found' | 'notfound' | 'error'.
  const [existing, setExisting] = useState({ status: 'idle', definition: null, reason: '' })
  const [editName, setEditName] = useState('')
  const [editIconUrl, setEditIconUrl] = useState('')
  const [editCategory, setEditCategory] = useState('')
  const [editAvailability, setEditAvailability] = useState('GENERAL')
  const [editIpaasId, setEditIpaasId] = useState('')
  const [editNativePermission, setEditNativePermission] = useState(false)

  function switchMode(m) {
    setMode(m)
    setResult(null)
    setExisting({ status: 'idle', definition: null, reason: '' })
  }

  function toggleTeam(id) {
    setTeamIds(ids => ids.includes(id) ? ids.filter(x => x !== id) : [...ids, id])
  }

  function addCustomTeam() {
    const id = customTeamId.trim()
    if (!id) return
    const name = customTeamName.trim() || id
    setCustomTeams(teams => [...teams, { id, name }])
    setTeamIds(ids => ids.includes(id) ? ids : [...ids, id])
    setCustomTeamId('')
    setCustomTeamName('')
  }

  function removeCustomTeam(id) {
    setCustomTeams(teams => teams.filter(t => t.id !== id))
    setTeamIds(ids => ids.filter(x => x !== id))
  }

  async function generate(ipaasId) {
    setBusy(true)
    setCopiedAll(false)
    try {
      const resp = await apiFetch('POST', `/api/sessions/${encodeURIComponent(session.id)}/prismatic/curls`, {
        integrationDir: integration.dir,
        env,
        category,
        teamIds: env === 'prod' ? teamIds : undefined,
        ipaasId: ipaasId || undefined,
        name: nameOverride.trim() || undefined,
        iconUrl: iconUrlOverride.trim() || undefined,
      })
      setResult(resp)
    } catch (e) {
      // apiFetch already toasts the error message.
    } finally {
      setBusy(false)
    }
  }

  async function loadExisting() {
    setBusy(true)
    try {
      const resp = await apiFetch('POST', `/api/sessions/${encodeURIComponent(session.id)}/prismatic/existing`, {
        integrationDir: integration.dir, env,
      })
      if (resp.found) {
        const def = resp.definition || {}
        const cfg = def.config || {}
        setEditName(def.name || '')
        setEditIconUrl(def.iconUrl || '')
        setEditCategory(def.category || SOURCE_DEF_CATEGORIES[0])
        setEditAvailability(def.availability || 'GENERAL')
        setEditIpaasId(cfg.ipaasIntegrationId || '')
        setEditNativePermission(!!cfg.supportsSourceNativePermissions)
        setExisting({ status: 'found', definition: def, reason: '' })
      } else if (resp.reason) {
        setExisting({ status: 'error', definition: null, reason: resp.reason })
      } else {
        setExisting({ status: 'notfound', definition: null, reason: '' })
      }
    } catch (e) {
      // apiFetch already toasts the error message.
    } finally {
      setBusy(false)
    }
  }

  async function generateUpdate() {
    setBusy(true)
    setCopiedAll(false)
    try {
      const edited = {
        ...existing.definition,
        name: editName,
        iconUrl: editIconUrl,
        category: editCategory,
        availability: editAvailability,
        config: {
          ...(existing.definition.config || {}),
          ipaasIntegrationId: editIpaasId,
          supportsSourceNativePermissions: editNativePermission,
        },
      }
      const resp = await apiFetch('POST', '/api/prismatic/curls/update', { env, definition: edited })
      setResult(resp)
    } catch (e) {
      // apiFetch already toasts the error message.
    } finally {
      setBusy(false)
    }
  }

  async function copy(text) {
    try {
      await navigator.clipboard.writeText(text)
      addToast('Copied')
    } catch (e) {
      addToast('Copy failed — select and copy manually')
    }
  }

  async function copyAll() {
    if (!result?.curls?.length) return
    await copy(result.curls.map(c => c.curl).join('\n\n'))
    setCopiedAll(true)
  }

  const needsInput = result?.needsInput || ''

  return html`
    <div class="pris-curls-overlay" onClick=${e => { if (e.target === e.currentTarget) onClose() }}>
      <div class="pris-curls-dialog">
        <div class="pris-curls-head">
          <div class="title">Source-def curls · ${integration.dir}</div>
          <button class="pris-cred-btn" onClick=${onClose}>Close</button>
        </div>

        ${!result && html`
          <div class="pris-curls-radios" style="padding: 12px 16px 0;">
            <button class=${`pris-cred-btn ${mode === 'create' ? 'active' : ''}`} onClick=${() => switchMode('create')}>Create new</button>
            <button class=${`pris-cred-btn ${mode === 'edit' ? 'active' : ''}`} onClick=${() => switchMode('edit')}>Edit existing</button>
          </div>
        `}

        ${!result && mode === 'create' && html`
          <div class="pris-curls-body">
            <div class="pris-curls-field">
              <label>Environment</label>
              <div class="pris-curls-radios">
                ${['qa', 'prod'].map(e => html`
                  <button key=${e} class=${`pris-cred-btn ${env === e ? 'active' : ''}`} onClick=${() => setEnv(e)}>${e.toUpperCase()}</button>
                `)}
              </div>
            </div>
            <div class="pris-curls-field">
              <label>Category</label>
              <select value=${category} onChange=${e => setCategory(e.target.value)}>
                ${SOURCE_DEF_CATEGORIES.map(c => html`<option key=${c} value=${c}>${c}</option>`)}
              </select>
            </div>
            <div class="pris-curls-field">
              <label>Name (optional override)</label>
              <input type="text" placeholder="defaults to createSource.ts's sourceName"
                     value=${nameOverride} onInput=${e => setNameOverride(e.target.value)}/>
            </div>
            <div class="pris-curls-field">
              <label>Icon URL (optional override)</label>
              <input type="text" placeholder="defaults to Guru's source-icons convention"
                     value=${iconUrlOverride} onInput=${e => setIconUrlOverride(e.target.value)}/>
            </div>
            ${env === 'prod' && html`
              <div class="pris-curls-field">
                <label>Test team(s) for the TEAM → GENERAL rollout dance</label>
                ${TEST_TEAMS.map(team => html`
                  <label key=${team.id} class="pris-curls-check">
                    <input type="checkbox" checked=${teamIds.includes(team.id)} onChange=${() => toggleTeam(team.id)}/>
                    ${team.name}
                  </label>
                `)}
                ${customTeams.map(team => html`
                  <label key=${team.id} class="pris-curls-check">
                    <input type="checkbox" checked=${teamIds.includes(team.id)} onChange=${() => toggleTeam(team.id)}/>
                    ${team.name}
                    <button class="pris-cred-btn danger" onClick=${() => removeCustomTeam(team.id)}>×</button>
                  </label>
                `)}
                <div class="pris-curls-addteam">
                  <input type="text" placeholder="team id (encoded or decoded — paste whatever the endpoint expects)"
                         value=${customTeamId} onInput=${e => setCustomTeamId(e.target.value)}/>
                  <input type="text" placeholder="label (optional)"
                         value=${customTeamName} onInput=${e => setCustomTeamName(e.target.value)}/>
                  <button class="pris-cred-btn" disabled=${!customTeamId.trim()} onClick=${addCustomTeam}>+ Add team</button>
                </div>
              </div>
            `}
            <button class="pris-qbtn" disabled=${busy} onClick=${() => generate()}>
              ${busy ? 'Resolving…' : 'Generate'}
            </button>
          </div>
        `}

        ${!result && mode === 'edit' && existing.status === 'idle' && html`
          <div class="pris-curls-body">
            <div class="pris-curls-field">
              <label>Environment</label>
              <div class="pris-curls-radios">
                ${['qa', 'prod'].map(e => html`
                  <button key=${e} class=${`pris-cred-btn ${env === e ? 'active' : ''}`} onClick=${() => setEnv(e)}>${e.toUpperCase()}</button>
                `)}
              </div>
            </div>
            <button class="pris-qbtn" disabled=${busy} onClick=${loadExisting}>
              ${busy ? 'Loading…' : `Load ${env.toUpperCase()} definition`}
            </button>
          </div>
        `}

        ${!result && mode === 'edit' && (existing.status === 'error' || existing.status === 'notfound') && html`
          <div class="pris-curls-body">
            <div class="pris-curls-reason">
              ${existing.status === 'error'
                ? existing.reason
                : `No existing ${env.toUpperCase()} source definition found for this integration yet — nothing to edit. Use "Create new" instead.`}
            </div>
            <button class="pris-cred-btn" onClick=${() => setExisting({ status: 'idle', definition: null, reason: '' })}>Back</button>
          </div>
        `}

        ${!result && mode === 'edit' && existing.status === 'found' && html`
          <div class="pris-curls-body">
            <div class="pris-curls-reason">
              Loaded <b>${existing.definition.name}</b> (${existing.definition.type}) from ${env.toUpperCase()}. Other fields are kept as-is.
            </div>
            <div class="pris-curls-field">
              <label>Name</label>
              <input type="text" value=${editName} onInput=${e => setEditName(e.target.value)}/>
            </div>
            <div class="pris-curls-field">
              <label>Icon URL</label>
              <input type="text" value=${editIconUrl} onInput=${e => setEditIconUrl(e.target.value)}/>
            </div>
            <div class="pris-curls-field">
              <label>Category</label>
              <select value=${editCategory} onChange=${e => setEditCategory(e.target.value)}>
                ${SOURCE_DEF_CATEGORIES.map(c => html`<option key=${c} value=${c}>${c}</option>`)}
              </select>
            </div>
            <div class="pris-curls-field">
              <label>Availability</label>
              <div class="pris-curls-radios">
                ${['GENERAL', 'TEAM'].map(a => html`
                  <button key=${a} class=${`pris-cred-btn ${editAvailability === a ? 'active' : ''}`} onClick=${() => setEditAvailability(a)}>${a}</button>
                `)}
              </div>
            </div>
            <div class="pris-curls-field">
              <label>ipaasIntegrationId</label>
              <input type="text" value=${editIpaasId} onInput=${e => setEditIpaasId(e.target.value)}/>
            </div>
            <label class="pris-curls-check">
              <input type="checkbox" checked=${editNativePermission} onChange=${e => setEditNativePermission(e.target.checked)}/>
              Supports source native permissions
            </label>
            <div class="pris-curls-row">
              <button class="pris-cred-btn" onClick=${() => setExisting({ status: 'idle', definition: null, reason: '' })}>Back</button>
              <button class="pris-qbtn" disabled=${busy} onClick=${generateUpdate}>${busy ? 'Generating…' : 'Generate update curl'}</button>
            </div>
          </div>
        `}

        ${needsInput === 'manual' && html`
          <div class="pris-curls-body">
            <div class="pris-curls-reason">${result.reason}</div>
            <div class="pris-curls-field">
              <label>ipaasIntegrationId</label>
              <input type="text" placeholder="paste the Prismatic integration id"
                     value=${manualIpaasId} onInput=${e => setManualIpaasId(e.target.value)}/>
            </div>
            <div class="pris-curls-row">
              <button class="pris-cred-btn" onClick=${() => setResult(null)}>Back</button>
              <button class="pris-qbtn" disabled=${busy || !manualIpaasId.trim()} onClick=${() => generate(manualIpaasId.trim())}>
                ${busy ? 'Generating…' : 'Generate with this id'}
              </button>
            </div>
          </div>
        `}

        ${needsInput === 'selection' && html`
          <div class="pris-curls-body">
            <div class="pris-curls-reason">${result.reason}</div>
            ${(result.options || []).map(opt => html`
              <button key=${opt.id} class="pris-curls-option" disabled=${busy} onClick=${() => generate(opt.id)}>
                <span class="nm">${opt.name}</span>
                <span class="id">${opt.id}${opt.versionNumber ? ` · v${opt.versionNumber}` : ''}</span>
              </button>
            `)}
            <button class="pris-cred-btn" onClick=${() => setResult(null)}>Back</button>
          </div>
        `}

        ${result && !needsInput && html`
          <div class="pris-curls-body">
            ${result.ipaas && html`
              <div class="pris-curls-reason">
                Resolved via ${result.ipaas.source || '?'} → <b>${result.ipaas.name}</b> (${result.ipaas.id})
              </div>
            `}
            ${result.curls.map((step, i) => html`
              <div key=${i} class="pris-curls-step">
                <div class="pris-curls-step-head">
                  <b>${step.label}</b>
                  <button class="pris-cred-btn" onClick=${() => copy(step.curl)}>Copy</button>
                </div>
                <div class="pris-curls-step-desc">${step.description}</div>
                <pre class="pris-curls-code">${step.curl}</pre>
              </div>
            `)}
            <div class="pris-curls-row">
              <button class="pris-cred-btn" onClick=${() => setResult(null)}>Start over</button>
              <button class="pris-qbtn" onClick=${copyAll}>${copiedAll ? 'Copied all' : 'Copy all'}</button>
            </div>
          </div>
        `}
      </div>
    </div>
  `
}

const ENV_LABELS = { qa: 'QA', prod: 'Prod' }

function CredentialsSection({ status, onChange }) {
  // { kind, env } while a row's input is open, else null. Only one row can
  // be mid-edit at a time — same reasoning as CredentialsView.tsx (cni-cli):
  // a stray abandoned edit shouldn't linger.
  const [editing, setEditing] = useState(null)
  const [value, setValue] = useState('')
  const [busy, setBusy] = useState(false)
  const mutationsEnabled = mutationsEnabledSignal.value

  if (!status) {
    return html`<div style="font-family: var(--mono); font-size: 11.5px; color: var(--muted);">Loading…</div>`
  }

  function startEdit(kind, env) {
    setEditing({ kind, env })
    setValue('')
  }

  async function save(kind, env) {
    if (busy || !value.trim()) return
    setBusy(true)
    try {
      await apiFetch('POST', '/api/prismatic/credentials', { kind, env, value: value.trim() })
      addToast(`Saved ${kind === 'prism' ? 'Prism token' : 'Guru API credentials'} for ${ENV_LABELS[env]}`)
      setEditing(null)
      setValue('')
      onChange()
    } catch (e) {
      // apiFetch already toasts the error message (e.g. the 'user:token' format check).
    } finally {
      setBusy(false)
    }
  }

  async function clear(kind, env) {
    if (busy) return
    setBusy(true)
    try {
      await apiFetch('DELETE', '/api/prismatic/credentials', { kind, env })
      addToast(`Cleared ${kind === 'prism' ? 'Prism token' : 'Guru API credentials'} for ${ENV_LABELS[env]}`)
      onChange()
    } catch (e) {
      // toasted upstream
    } finally {
      setBusy(false)
    }
  }

  function renderRow(kind, env, configured) {
    if (editing && editing.kind === kind && editing.env === env) {
      return html`
        <div key=${env} class="pris-cred-edit">
          <span class=${`env ${env}`}>${ENV_LABELS[env]}</span>
          <input type="password" autofocus placeholder=${kind === 'prism' ? 'paste refresh token' : 'user@getguru.com:token'}
                 value=${value} onInput=${e => setValue(e.target.value)}
                 onKeyDown=${e => { if (e.key === 'Enter') save(kind, env); if (e.key === 'Escape') setEditing(null) }}/>
          <button class="pris-cred-btn" disabled=${busy || !value.trim()} onClick=${() => save(kind, env)}>Save</button>
          <button class="pris-cred-btn" onClick=${() => setEditing(null)}>Cancel</button>
        </div>
      `
    }
    return html`
      <div key=${env} class="pris-cred-row">
        <span class=${`env ${env}`}>${ENV_LABELS[env]}</span>
        <span class=${`val ${configured ? 'set' : ''}`}>${configured ? '•••••••••• configured' : 'not configured'}</span>
        ${mutationsEnabled && html`
          <button class="pris-cred-btn" onClick=${() => startEdit(kind, env)}>${configured ? 'Replace' : 'Set'}</button>
        `}
        ${mutationsEnabled && configured && html`
          <button class="pris-cred-btn danger" disabled=${busy} onClick=${() => clear(kind, env)}>Clear</button>
        `}
      </div>
    `
  }

  return html`
    <div class="pris-cred-grid">
      <div class="pris-cred-card">
        <div class="h">Prism refresh tokens</div>
        ${renderRow('prism', 'qa', status.prism.qa)}
        ${renderRow('prism', 'prod', status.prism.prod)}
      </div>
      <div class="pris-cred-card">
        <div class="h">Guru API credentials</div>
        ${renderRow('guru', 'qa', status.guru.qa)}
        ${renderRow('guru', 'prod', status.guru.prod)}
      </div>
    </div>
  `
}
