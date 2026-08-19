// Sidebar.js -- REWRITE. Status filters + groups + sessions list.
//
// Drops the old Tailwind Sidebar (still present in SessionList.js / SessionRow.js
// / GroupRow.js but no longer mounted). New design: bundle's `.sidebar` class
// stack with side-head / side-filter / side-list / sess rows.
//
// Action handlers route through apiFetch; mutations gated by mutationsEnabledSignal.
import { html } from 'htm/preact'
import { useState, useMemo } from 'preact/hooks'
import { Icon, ICONS, Dot, kindSigil, toolAvatar } from './icons.js'
import { menuModelSignal } from './dataModel.js'
import {
  selectedIdSignal, mutationsEnabledSignal, confirmDialogSignal,
  createSessionDialogSignal, editSessionDialogSignal,
  sidebarWidthSignal, clampSidebarWidth,
} from './state.js'
import { statusFiltersSignal, showColsSignal, activeTabSignal } from './uiState.js'
import { apiFetch } from './api.js'
import { addToast } from './Toast.js'
import { formatRelativeTime } from './timeFmt.js'

const STATUS_CHIPS = [
  { id: 'running', sym: '●' },
  { id: 'waiting', sym: '◐' },
  { id: 'error',   sym: '✕' },
  { id: 'idle',    sym: '○' },
]

const SHOW_COL_OPTIONS = [
  { id: 'tool',     label: 'Tool badge' },
  { id: 'cost',     label: 'Cost' },
  { id: 'branch',   label: 'Git branch' },
  { id: 'attach',   label: 'MCPs / skills' },
  { id: 'sandbox',  label: 'Docker / worktree' },
  { id: 'lastSeen', label: 'Last activity' },
]

function doAction(action, s) {
  if (!mutationsEnabledSignal.value) {
    addToast('mutations disabled')
    return
  }
  const id = s.id
  if (action === 'start')   return apiFetch('POST', `/api/sessions/${id}/start`).catch(() => {})
  if (action === 'stop')    return apiFetch('POST', `/api/sessions/${id}/stop`).catch(() => {})
  if (action === 'restart') return apiFetch('POST', `/api/sessions/${id}/restart`).catch(() => {})
  if (action === 'fork')    return apiFetch('POST', `/api/sessions/${id}/fork`, { title: s.title + '-fork' }).catch(() => {})
  if (action === 'archive') {
    confirmDialogSignal.value = {
      message: `Archive session "${s.title}"? The process will be stopped and hidden from the active list.`,
      onConfirm: () => apiFetch('POST', `/api/sessions/${id}/archive`)
        .then(() => {
          if (selectedIdSignal.value === id) {
            selectedIdSignal.value = null
            if (window.location.pathname.startsWith('/s/')) {
              history.replaceState(null, '', '/')
            }
          }
        })
        .catch(() => {}),
    }
  }
  if (action === 'delete') {
    confirmDialogSignal.value = {
      message: `Delete session "${s.title}"? This stops the tmux session and removes metadata.`,
      onConfirm: () => apiFetch('DELETE', `/api/sessions/${id}`).catch(() => {}),
    }
  }
  if (action === 'worktreeFinish') {
    // Issue #1126 — POST /api/sessions/{id}/worktree/finish. Mirrors TUI
    // W/shift+w. Body left empty so the backend auto-detects target
    // branch and uses default flags (merge + delete branch).
    const branch = s.worktreeBranch || s.branch
    confirmDialogSignal.value = {
      message: `Finish worktree for "${s.title}"? Merges branch "${branch}" into default branch, removes worktree, deletes branch, and removes session.`,
      onConfirm: () => apiFetch('POST', `/api/sessions/${id}/worktree/finish`).catch(() => {}),
    }
  }
  if (action === 'edit') {
    editSessionDialogSignal.value = { sessionId: id }
  }
}

// STATUS_ACCENT maps a session's status to the left-accent-bar token. Falls
// back to idle for statuses without their own accent (stopped/starting/
// queued) — the .dot already carries the finer distinction there.
const STATUS_ACCENT = {
  running: 'var(--status-running)',
  waiting: 'var(--status-waiting)',
  error:   'var(--status-error)',
  starting: 'var(--status-start)',
}

// sessionContextPct / sessionNeedsAttention are shared between SessionItem
// (per-row pill) and the fleet summary strip (top-of-sidebar "N need you"
// count) so the two never drift on what "needs attention" means.
function sessionContextPct(s) {
  return typeof s.contextPercent === 'number' ? Math.min(100, s.contextPercent) : null
}
function sessionNeedsAttention(s) {
  const ctxPct = sessionContextPct(s)
  return s.status === 'error' || (ctxPct != null && ctxPct >= 80)
}

// healthBadge turns s.health (GET /api/sessions/health/batch, cheap local
// git checks — internal/git/health.go) into a single compact row badge.
// Deliberately ONE badge, not one per flag: the sidebar is narrow and a row
// can have several issues at once, so the label picks the single most
// actionable word and the tooltip carries the full detail. Returns null
// when there's nothing to badge (health absent, or IsClean).
function healthBadge(s) {
  const h = s.health
  if (!h) return null
  if (h.worktreeMissing) {
    return {
      cls: 'err', text: 'worktree gone',
      title: 'Worktree directory no longer exists on disk — recreate it or point the session at a new path before starting.',
    }
  }
  const issues = []
  if (h.uncommittedChanges) issues.push('uncommitted changes')
  if (h.upstreamGone) issues.push('remote branch deleted (PR merged?)')
  if (h.behind > 0) issues.push(`${h.behind} behind base`)
  if (h.ahead > 0) issues.push(`${h.ahead} ahead of base`)
  if (issues.length === 0) return null
  const text = h.upstreamGone ? 'merged?'
    : h.uncommittedChanges ? 'uncommitted'
    : `${h.behind}↓ ${h.ahead}↑`
  return { cls: 'warn', text, title: issues.join(' · ') }
}

// shortModelLabel compacts "Claude Opus" + "4.6" into "Opus 4.6" for the
// row chip — the sidebar is ~280px wide, and the tool avatar already says
// "this is Claude", so the repeated word there just steals room from the
// title. Falls back to the full model string for non-Claude tools, which
// don't carry the same "<Tool> <Family>" shape.
function shortModelLabel(s) {
  if (!s.model) return ''
  const short = s.model.replace(/^Claude\s+/i, '')
  return s.modelVersion ? `${short} ${s.modelVersion}` : short
}

function SessionItem({ s, sel, onSelect, showCols }) {
  const mcpCount = (s.mcps || []).length
  const skillCount = (s.skills || []).length

  const avatar = toolAvatar(s.tool)
  const rowAccent = STATUS_ACCENT[s.status] || 'var(--status-idle)'

  // ctxPct rides in on s.contextPercent — Gemini's arrives free on the menu
  // snapshot, Claude's is hydrated separately (menuModelSignal merges
  // sessionContextSignal; see dataModel.js). null means "no bar to draw"
  // rather than a misleading 0%.
  const ctxPct = sessionContextPct(s)
  const ctxColor = ctxPct == null ? null
    : ctxPct >= 80 ? 'var(--status-error)'
    : ctxPct >= 60 ? 'var(--status-waiting)'
    : 'var(--status-running)'
  const needsAttention = sessionNeedsAttention(s)
  const attentionLabel = s.status === 'error' ? (s.raw?.substate || 'error') : 'near limit'

  // Single line of branch · time · cost · attachments, "·"-separated —
  // built as a list and joined so a hidden/absent field never leaves a
  // dangling separator behind it.
  const metaBits = []
  if (showCols.branch && s.branch && s.branch !== '—') {
    metaBits.push(html`<span class="branch">⌟ ${s.branch}</span>`)
  }
  const health = healthBadge(s)
  if (health) {
    metaBits.push(html`<span class=${`att-count ${health.cls}`} title=${health.title}>${health.text}</span>`)
  }
  if (showCols.lastSeen) {
    metaBits.push(html`<span>${s.status === 'running' ? 'active now' : formatRelativeTime(s.lastAccessedAt)}</span>`)
  }
  if (showCols.cost && s.cost > 0) {
    const prefix = s.costEstimated ? '~$' : '$'
    metaBits.push(html`<span class="cost" title=${s.costEstimated ? 'Estimated from the transcript — no cost-event ledger entry yet' : ''}>${prefix}${s.cost.toFixed(2)}</span>`)
  }
  if (showCols.attach && mcpCount > 0) {
    metaBits.push(html`<span class="att-count">${mcpCount} mcp${mcpCount > 1 ? 's' : ''}</span>`)
  }
  if (showCols.attach && skillCount > 0) {
    metaBits.push(html`<span class="att-count skill">${skillCount} skill${skillCount > 1 ? 's' : ''}</span>`)
  }
  if (showCols.sandbox && s.sandbox) {
    metaBits.push(html`<span class="att-count warn">docker</span>`)
  }
  if (showCols.sandbox && s.worktree) {
    metaBits.push(html`<span class="att-count">worktree</span>`)
  }
  const metaRow = metaBits.flatMap((bit, i) => i === 0 ? [bit] : [html`<span class="sep">·</span>`, bit])

  return html`
    <div class=${`sess ${sel ? 'sel' : ''} ${s.kind}`}
         style=${{ '--row-accent': rowAccent }} onClick=${() => onSelect(s.id)}>
      ${s.kind === 'agent'
        ? html`<span class="avatar" style=${{ '--avatar-color': avatar.color }} title=${s.tool}>${avatar.glyph}</span>`
        : html`<span class="sig">${kindSigil(s.kind)}</span>`}
      <div class="card-body">
        <div class="card-top">
          <${Dot} status=${s.status} size=${6}/>
          <span class="tt" title=${s.title}>${s.title}</span>
          ${showCols.tool && s.tool && html`<span class="tag">${s.tool}</span>`}
          ${s.model && html`<span class="model-chip" title=${s.model}>${shortModelLabel(s)}</span>`}
          ${needsAttention && html`<span class="attn-pill">${attentionLabel}</span>`}
        </div>
        ${metaRow.length > 0 && html`<div class="card-meta">${metaRow}</div>`}
        ${ctxPct != null && html`
          <div class="ctx-row" title=${`Context window: ${Math.round(ctxPct)}%`}>
            <div class="ctx-bar"><span style=${{ width: ctxPct + '%', '--ctx-color': ctxColor }}/></div>
            <span class="ctx-pct" style=${{ '--ctx-color': ctxColor }}>${Math.round(ctxPct)}%</span>
          </div>
        `}
      </div>
      <div class="actions" onClick=${e => e.stopPropagation()}>
        ${(s.status === 'running' || s.status === 'waiting')
          ? html`<button class="mini" title="Stop" data-testid="session-stop-btn" onClick=${() => doAction('stop', s)}><${Icon} d=${ICONS.stop} size=${12}/></button>`
          : html`<button class="mini good" title="Start" data-testid="session-start-btn" onClick=${() => doAction('start', s)}><${Icon} d=${ICONS.play} size=${12}/></button>`}
        <button class="mini good" title="Restart" data-testid="session-restart-btn" onClick=${() => doAction('restart', s)}><${Icon} d=${ICONS.restart} size=${12}/></button>
        <button class="mini" title="Edit" data-testid="edit-session-btn" onClick=${() => doAction('edit', s)}><${Icon} d=${ICONS.edit} size=${12}/></button>
        ${s.canFork && html`<button class="mini fork" title="Fork" data-testid="session-fork-btn" onClick=${() => doAction('fork', s)}><${Icon} d=${ICONS.fork} size=${12}/></button>`}
        ${s.worktree && html`<button class="mini" title="Finish worktree (merge + cleanup)" onClick=${() => doAction('worktreeFinish', s)} data-action="worktree-finish" data-testid="session-worktree-finish-btn">⎇✓</button>`}
        <button class="mini" title="Archive" onClick=${() => doAction('archive', s)}>⌂</button>
        <button class="mini danger" title="Delete" data-testid="session-delete-btn" onClick=${() => doAction('delete', s)}><${Icon} d=${ICONS.trash} size=${12}/></button>
      </div>
    </div>
  `
}

// SidebarResizer drags --sidebar-w (AppShell.js sets it inline from
// sidebarWidthSignal) between SIDEBAR_WIDTH_MIN/MAX. The signal and its
// clamp already existed in state.js for this — nothing consumed it before,
// so the sidebar was stuck at the 300px default no matter what a user
// dragged. window-level listeners (not onPointerMove on the handle itself)
// so the drag keeps tracking even if the cursor outruns the thin handle.
function SidebarResizer() {
  const onPointerDown = (e) => {
    e.preventDefault()
    const startX = e.clientX
    const startWidth = sidebarWidthSignal.value
    const onMove = (ev) => {
      sidebarWidthSignal.value = clampSidebarWidth(startWidth + (ev.clientX - startX))
    }
    const onUp = () => {
      window.removeEventListener('pointermove', onMove)
      window.removeEventListener('pointerup', onUp)
      try { localStorage.setItem('sidebar-width', String(sidebarWidthSignal.value)) } catch (_) {
        // localStorage may throw in incognito/privacy modes; width still
        // applies for the rest of this session, just doesn't persist.
      }
    }
    window.addEventListener('pointermove', onMove)
    window.addEventListener('pointerup', onUp)
  }
  return html`
    <div class="sidebar-resizer" onPointerDown=${onPointerDown}
         role="separator" aria-orientation="vertical" aria-label="Resize sidebar"/>
  `
}

export function Sidebar() {
  const { groups, byGroup, sessions } = menuModelSignal.value
  const selected = selectedIdSignal.value
  const statusFilters = statusFiltersSignal.value
  const showCols = showColsSignal.value
  const [filter, setFilter] = useState('')
  const [showMenu, setShowMenu] = useState(false)
  const [expanded, setExpanded] = useState(() => Object.fromEntries(groups.map(g => [g.path, g.expanded !== false])))

  const matches = (s) => {
    if (statusFilters.length && !statusFilters.includes(s.status)) return false
    if (!filter) return true
    const t = filter.toLowerCase()
    return ((s.title || '') + ' ' + (s.group || '') + ' ' + (s.path || '') + ' ' + (s.tool || '') + ' ' + (s.branch || ''))
      .toLowerCase().includes(t)
  }

  const totalVisible = useMemo(() => sessions.filter(matches).length, [sessions, filter, statusFilters])
  // Fleet-wide summary strip (total spend + how many need you) — the
  // sidebar-header equivalent of the mockup's "6 sessions · $12.47 today ·
  // 1 needs you" line. Derived from `sessions`, not `matches`-filtered, so
  // it always reflects the whole fleet regardless of the active filter.
  const fleetCost = useMemo(() => sessions.reduce((sum, s) => sum + (s.cost || 0), 0), [sessions])
  const fleetCostEstimated = useMemo(() => sessions.some(s => s.costEstimated), [sessions])
  const fleetNeedsYou = useMemo(() => sessions.filter(sessionNeedsAttention).length, [sessions])
  const toggleStatus = (id) => {
    const cur = statusFiltersSignal.value
    statusFiltersSignal.value = cur.includes(id) ? cur.filter(x => x !== id) : [...cur, id]
  }
  // Open is defined as `expanded[p] !== false` (undefined counts as open: groups
  // arrive after the initial render, so most paths are never seeded). The toggle
  // must mirror that read — plain `!s[p]` maps undefined → true, which is still
  // "open", making the first click on a never-toggled group a silent no-op.
  const toggleGroup = (p) => setExpanded(s => ({ ...s, [p]: s[p] === false }))
  const onSelect = (id) => {
    selectedIdSignal.value = id
    activeTabSignal.value = 'terminal'
  }
  const setShowCol = (id) => {
    showColsSignal.value = { ...showCols, [id]: !showCols[id] }
  }

  return html`
    <div class="sidebar">
      <div class="side-head">
        <span class="label">SESSIONS</span>
        <span class="count">${totalVisible}</span>
        <div class="spacer"/>
        <div style="position: relative;">
          <button class=${`icon-btn ${showMenu ? 'active' : ''}`} title="Show columns" aria-label="Show columns"
                  data-testid="show-cols-btn"
                  onClick=${() => setShowMenu(m => !m)}>
            <${Icon} d=${ICONS.filter}/>
          </button>
          ${showMenu && html`
            <div class="show-menu" data-testid="show-cols-menu" onClick=${e => e.stopPropagation()}>
              <div class="sm-head">SHOW IN ROW</div>
              ${SHOW_COL_OPTIONS.map(c => html`
                <label key=${c.id} class="sm-row" data-testid=${`show-col-${c.id}`}>
                  <input type="checkbox" checked=${!!showCols[c.id]} onChange=${() => setShowCol(c.id)}/>
                  <span>${c.label}</span>
                </label>
              `)}
              <div class="sm-foot" onClick=${() => setShowMenu(false)}>done</div>
            </div>
          `}
        </div>
        ${mutationsEnabledSignal.value && html`
          <button class="icon-btn" title="New session (n)" aria-label="New session"
                  onClick=${() => (createSessionDialogSignal.value = true)}>
            <${Icon} d=${ICONS.plus}/>
          </button>
        `}
      </div>
      ${(fleetCost > 0 || fleetNeedsYou > 0) && html`
        <div class="side-stats">
          ${fleetCost > 0 && html`<span class="ss-cost">${fleetCostEstimated ? '~$' : '$'}${fleetCost.toFixed(2)} today</span>`}
          ${fleetNeedsYou > 0 && html`<span class="ss-needs">${fleetNeedsYou} need${fleetNeedsYou > 1 ? '' : 's'} you</span>`}
        </div>
      `}
      <div class="side-filter">
        <input
          placeholder="/ filter"
          data-testid="sidebar-filter-input"
          value=${filter}
          onInput=${e => setFilter(e.target.value)}
        />
        ${STATUS_CHIPS.map(s => html`
          <span key=${s.id}
                class=${`side-chip ${statusFilters.includes(s.id) ? 'on' : ''}`}
                data-testid=${`status-chip-${s.id}`}
                onClick=${() => toggleStatus(s.id)}
                title=${s.id}>
            ${s.sym}
          </span>
        `)}
      </div>
      <div class="side-list">
        ${groups.map(g => {
          const members = (byGroup[g.path] || []).filter(matches)
          if (filter && members.length === 0) return null
          const open = expanded[g.path] !== false
          // Rollup: total cost + how many are waiting on the user, computed
          // from the same members array already built for rendering — no
          // extra signal or backend round-trip needed.
          const groupCost = members.reduce((sum, s) => sum + (s.cost || 0), 0)
          const groupCostEstimated = members.some(s => s.costEstimated)
          const waitingCount = members.filter(s => s.status === 'waiting').length
          return html`
            <div key=${g.path}>
              <div class=${`side-group-head ${g.kind || ''}`} data-testid=${`group-head-${g.path}`} onClick=${() => toggleGroup(g.path)}>
                <span class="chev">${open ? '▾' : '▸'}</span>
                <span class="name">${g.label}</span>
                <span class="badge">(${members.length})</span>
                <span class="g-stats">
                  ${waitingCount > 0 && html`<span class="g-waiting">${waitingCount} waiting</span>`}
                  ${groupCost > 0 && html`<span class="g-cost">${groupCostEstimated ? '~$' : '$'}${groupCost.toFixed(2)}</span>`}
                </span>
              </div>
              ${open && members.map(s => html`
                <${SessionItem} key=${s.id} s=${s} sel=${selected === s.id} onSelect=${onSelect} showCols=${showCols}/>
              `)}
            </div>
          `
        })}
        ${sessions.length === 0 && html`
          <div style="padding: 16px; font-family: var(--mono); font-size: 11px; color: var(--muted); text-align: center;">
            No sessions yet. Press <span class="kbd" style="border:1px solid var(--border); padding: 0 4px; border-radius: 3px;">n</span> to create one.
          </div>
        `}
      </div>
      <${SidebarResizer}/>
    </div>
  `
}
