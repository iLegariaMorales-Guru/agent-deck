// RightRail.js -- Configurable session detail rail (right side).
//
// Cards: Overview, Usage, MCPs, Skills, Children, Events, Timeline. User
// toggles which are visible in the rail-add picker at the bottom.
//
// The Children card renders the conductor child-session topology. The
// tree is built client-side from the same menuModelSignal that drives
// the session list, so it stays in sync with SSE menu updates for free.
// The Go endpoint at GET /api/sessions/{id}/children exposes the same
// shape for direct API consumers and lives in handlers_children.go.
//
// The Timeline card is different from every other card here: it's the
// only one that fetches its own data (GET /api/sessions/{id}/timeline)
// instead of reading off menuModelSignal, because a merged commit/push/
// prompt/edit feed isn't part of the menu snapshot's shape and doesn't
// need to be — it's only ever needed while this one card is open. See
// handlers_timeline.go for what it merges (git log/reflog + the Claude
// transcript) and why compacting a session's context doesn't lose any of
// it.
//
// MCPs / Skills / Events still render an informative "TUI-only" hint
// because their underlying APIs are not yet wired through the right rail.
import { html } from 'htm/preact'
import { signal } from '@preact/signals'
import { useState, useEffect } from 'preact/hooks'
import { menuModelSignal } from './dataModel.js'
import { selectedIdSignal } from './state.js'
import { rightRailPanelsSignal } from './uiState.js'
import { apiFetch } from './api.js'

// Module-scope signal so collapsed state survives RightRail re-mounts
// (e.g. when the user switches between sessions and back). Keyed by
// session id so each conductor remembers its own expanded set.
const collapsedNodesSignal = signal({})

const AVAIL_PANELS = [
  { id: 'overview', label: 'Overview' },
  { id: 'usage',    label: 'Usage & activity' },
  { id: 'mcps',     label: 'MCPs' },
  { id: 'skills',   label: 'Skills' },
  { id: 'children', label: 'Children (conductor)' },
  { id: 'events',   label: 'Events (watcher)' },
  { id: 'timeline', label: 'Timeline' },
]

function Card({ title, badge, testid, children }) {
  return html`
    <div class="card" data-testid=${testid}>
      <div class="card-head">
        <span class="name">${title}</span>
        ${badge && html`<span class="pill">${badge}</span>`}
      </div>
      <div class="card-body">${children}</div>
    </div>
  `
}

function NoData({ msg }) {
  return html`<div style="font-family: var(--mono); font-size: 11px; color: var(--muted);">${msg}</div>`
}

// Build the conductor → children adjacency map once per render. Cycles
// (corrupt parent pointers) are broken by a visited set so the tree
// builder cannot loop forever — mirrors the Go handler's defense.
function buildChildrenTree(rootId, sessions) {
  const byParent = new Map()
  for (const s of sessions) {
    const p = s.raw && s.raw.parentSessionId
    if (!p) continue
    if (!byParent.has(p)) byParent.set(p, [])
    byParent.get(p).push(s)
  }
  const visited = new Set([rootId])
  const walk = (id) => {
    const kids = byParent.get(id) || []
    return kids
      .filter((k) => {
        if (visited.has(k.id)) return false
        visited.add(k.id)
        return true
      })
      .map((k) => ({ session: k, children: walk(k.id) }))
  }
  return walk(rootId)
}

// Renders one node + its descendants. Leaf nodes hide the disclosure
// triangle; non-leaf nodes show ▾/▸ and toggle via collapsedNodesSignal.
function ChildNode({ node, depth, rootId }) {
  const collapsed = collapsedNodesSignal.value
  const key = rootId + ':' + node.session.id
  const isOpen = !collapsed[key]
  const hasKids = node.children.length > 0
  const toggle = () => {
    collapsedNodesSignal.value = { ...collapsed, [key]: isOpen }
  }
  return html`
    <div class="child-node" data-session-id=${node.session.id} data-depth=${depth}
         style="font-family: var(--mono); font-size: 11px; line-height: 1.7; padding-left: ${depth * 12}px;">
      <span class="child-row" style="display: inline-flex; align-items: center; gap: 4px;">
        <span class="child-toggle"
              onClick=${hasKids ? toggle : null}
              style=${`width: 10px; display: inline-block; cursor: ${hasKids ? 'pointer' : 'default'}; color: var(--muted);`}>
          ${hasKids ? (isOpen ? '▾' : '▸') : ' '}
        </span>
        <span class="child-status pill" data-status=${node.session.status}
              style="font-size: 9px; padding: 0 4px;">${node.session.status}</span>
        <span class="child-title" style="color: var(--text-hi);">${node.session.title}</span>
        ${node.session.tool && html`<span class="child-tool" style="color: var(--muted);">· ${node.session.tool}</span>`}
      </span>
      ${hasKids && isOpen && node.children.map((kid) => html`
        <${ChildNode} key=${kid.session.id} node=${kid} depth=${depth + 1} rootId=${rootId}/>
      `)}
    </div>
  `
}

function ChildrenTree({ rootId, sessions }) {
  const tree = buildChildrenTree(rootId, sessions)
  if (tree.length === 0) {
    return html`<${NoData} msg="No child sessions yet."/>`
  }
  return html`
    <div class="children-tree" data-children-count=${tree.length}>
      ${tree.map((node) => html`
        <${ChildNode} key=${node.session.id} node=${node} depth=${0} rootId=${rootId}/>
      `)}
    </div>
  `
}

// ---------- Timeline card ----------
// See handlers_timeline.go for the merge this fetches: git commits/pushes
// on the session's worktree+branch, plus prompts/edits/PR opens-merges/
// compact markers parsed out of the Claude transcript.

function fmtTimelineTime(iso) {
  try {
    return new Date(iso).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })
  } catch (_) {
    return ''
  }
}

// shortenPath keeps a long file path from blowing out the ~280px rail —
// full path still lives in the title attr for hover.
function shortenPath(p) {
  if (!p) return ''
  const parts = p.split('/')
  return parts.length <= 2 ? p : '…/' + parts.slice(-2).join('/')
}

// groupTimelineEvents turns the API's flat newest-first array into
// {prompt, rows} sections: walking newest-first, every non-prompt event
// belongs to the PRECEDING prompt in time (the one that triggered it) —
// which is the NEXT prompt event this walk reaches, since it's older.
// Events before the session's first-ever prompt (or a non-Claude session
// with no prompts at all) land in a trailing group with prompt: null.
function groupTimelineEvents(events) {
  const groups = []
  let pending = []
  for (const ev of events) {
    if (ev.kind === 'prompt') {
      groups.push({ prompt: ev, rows: pending.reverse() })
      pending = []
    } else {
      pending.push(ev)
    }
  }
  if (pending.length > 0) groups.push({ prompt: null, rows: pending.reverse() })
  return groups
}

const TIMELINE_FILTERS = [
  { id: 'all', label: 'all' },
  { id: 'git', label: 'git' },
  { id: 'prompts', label: 'prompts' },
]

function timelineRowMatchesFilter(ev, filter) {
  if (filter === 'all') return true
  if (filter === 'git') return ev.kind === 'commit' || ev.kind === 'push' || ev.kind === 'pr'
  if (filter === 'prompts') return ev.kind === 'compact' // prompts themselves are the group headers, always shown
  return true
}

function TimelineRow({ ev }) {
  if (ev.kind === 'compact') {
    return html`<div class="tl-compact">compacted · ${fmtTimelineTime(ev.time)}</div>`
  }
  let body
  if (ev.kind === 'commit') {
    body = html`<span class="hash">${ev.hash?.slice(0, 7)}</span> ${ev.text}${ev.files ? html` <span class="tl-filecount">${ev.files} files</span>` : ''}`
  } else if (ev.kind === 'push') {
    body = html`<span class="push-lbl">↑</span> pushed to origin${ev.hash ? html` <span class="hash" style="color:var(--tn-cyan)">${ev.hash.slice(0, 7)}</span>` : ''}`
  } else if (ev.kind === 'pr') {
    body = html`<span class="pr-lbl">⑂</span> ${ev.text}`
  } else if (ev.kind === 'edit') {
    body = html`<span class="path" title=${ev.path}>${shortenPath(ev.path)}</span>`
  } else {
    body = ev.text
  }
  return html`
    <div class=${`tl-row ${ev.kind}`}>
      <span class="tl-dot"></span>
      <span class="tl-txt">${body}</span>
    </div>
  `
}

function TimelineCard({ sessionId }) {
  const [state, setState] = useState({ loading: true, events: [], hasMore: false, error: null })
  const [filter, setFilter] = useState('all')

  useEffect(() => {
    let cancelled = false
    setState({ loading: true, events: [], hasMore: false, error: null })
    apiFetch('GET', `/api/sessions/${encodeURIComponent(sessionId)}/timeline`)
      .then(data => {
        if (cancelled) return
        setState({ loading: false, events: data.events || [], hasMore: !!data.hasMore, error: null })
      })
      .catch(err => {
        if (cancelled) return
        setState({ loading: false, events: [], hasMore: false, error: err.message || 'failed to load timeline' })
      })
    return () => { cancelled = true }
  }, [sessionId])

  if (state.loading) {
    return html`<${NoData} msg="loading timeline…"/>`
  }
  if (state.error) {
    return html`<${NoData} msg=${`failed to load: ${state.error}`}/>`
  }
  if (state.events.length === 0) {
    return html`<${NoData} msg="No commits, pushes, or prompts recorded for this session yet."/>`
  }

  const promptCount = state.events.filter(ev => ev.kind === 'prompt').length
  const groups = groupTimelineEvents(state.events)

  return html`
    <div class="tl-filters">
      ${TIMELINE_FILTERS.map(f => html`
        <span key=${f.id} class=${`f ${filter === f.id ? 'on' : ''}`} onClick=${() => setFilter(f.id)}>${f.label}</span>
      `)}
    </div>
    ${groups.map((g, gi) => {
      const rows = g.rows.filter(ev => timelineRowMatchesFilter(ev, filter))
      return html`
        <div key=${gi}>
          ${g.prompt && html`
            <div class="tl-prompt">
              <div class="lbl"><span class="time">${fmtTimelineTime(g.prompt.time)}</span>you asked</div>
              <div class="txt">"${g.prompt.text}"</div>
            </div>
          `}
          ${rows.length > 0 && html`
            <div class="tl-group">
              ${rows.map((ev, ri) => html`<${TimelineRow} key=${ri} ev=${ev}/>`)}
            </div>
          `}
        </div>
      `
    })}
    ${state.hasMore && html`
      <div class="tl-more">showing the ${state.events.length} most recent events</div>
    `}
    ${promptCount === 0 && html`
      <div class="tl-more" style="color:var(--muted)">no prompts on this transcript yet — showing git activity only</div>
    `}
  `
}

export function RightRail() {
  const { sessions } = menuModelSignal.value
  const selected = selectedIdSignal.value
  const session = sessions.find(s => s.id === selected) || sessions[0]
  const panels = rightRailPanelsSignal.value

  if (!session) {
    return html`
      <div class="rightrail" data-testid="right-rail">
        <div class="rail-head"><span class="t">SESSION</span></div>
        <div class="rail-body">
          <div style="padding: 18px; font-family: var(--mono); font-size: 11px; color: var(--muted);">
            no session selected
          </div>
        </div>
      </div>
    `
  }

  const togglePanel = (id) => {
    rightRailPanelsSignal.value = { ...panels, [id]: !panels[id] }
  }

  return html`
    <div class="rightrail" data-testid="right-rail">
      <div class="rail-head">
        <span class="t">SESSION</span>
        <div class="spacer"/>
        <span class="t" style="color: var(--text-hi);">${session.title}</span>
      </div>
      <div class="rail-body">
        ${panels.overview && html`
          <${Card} title="OVERVIEW" badge=${session.status} testid="rail-card-overview">
            <div class="kv"><span class="k">kind</span><span class="v">${session.kind}</span></div>
            <div class="kv"><span class="k">tool</span><span class="v">${session.tool || '—'}</span></div>
            ${session.model && html`
              <div class="kv"><span class="k">model</span><span class="v">${session.model}</span></div>`}
            ${session.modelVersion && html`
              <div class="kv"><span class="k">version</span><span class="v">${session.modelVersion}</span></div>`}
            ${session.modelId && html`
              <div class="kv"><span class="k">model id</span><span class="v" title=${session.modelId}>${session.modelId}</span></div>`}
            <div class="kv"><span class="k">group</span><span class="v">${session.group || '—'}</span></div>
            ${session.branch && session.branch !== '—' && html`
              <div class="kv"><span class="k">branch</span><span class="v">${session.branch}</span></div>`}
            ${session.path && html`
              <div class="kv"><span class="k">path</span><span class="v" title=${session.path}>${session.path}</span></div>`}
            ${session.sandbox && html`<div class="kv"><span class="k">sandbox</span><span class="v warn">docker</span></div>`}
            ${session.worktree && html`<div class="kv"><span class="k">worktree</span><span class="v ok">yes</span></div>`}
          </${Card}>
        `}
        ${panels.usage && html`
          <${Card} title="USAGE" testid="rail-card-usage">
            ${session.cost > 0
              ? html`<div class="kv"><span class="k">cost</span><span class="v ok">$${session.cost.toFixed(2)}</span></div>`
              : html`<${NoData} msg="cost data not available for this session"/>`}
            ${session.tokens > 0 && html`<div class="kv"><span class="k">tokens</span><span class="v">${(session.tokens/1000).toFixed(1)}k</span></div>`}
          </${Card}>
        `}
        ${panels.mcps && html`
          <${Card} title="MCPS" testid="rail-card-mcps">
            <${NoData} msg="MCP attachments not exposed via web API. Use TUI (m key)."/>
          </${Card}>
        `}
        ${panels.skills && html`
          <${Card} title="SKILLS" testid="rail-card-skills">
            <${NoData} msg="Skill attachments not exposed via web API. Use TUI (s key)."/>
          </${Card}>
        `}
        ${panels.children && session.kind === 'conductor' && html`
          <${Card} title="CHILDREN" badge="conductor" testid="rail-card-children">
            <${ChildrenTree} rootId=${session.id} sessions=${sessions}/>
          </${Card}>
        `}
        ${panels.events && session.kind === 'watcher' && html`
          <${Card} title="EVENTS" testid="rail-card-events">
            <${NoData} msg="Watcher event stream not exposed via web API."/>
          </${Card}>
        `}
        ${panels.timeline && html`
          <${Card} title="TIMELINE" testid="rail-card-timeline">
            <${TimelineCard} sessionId=${session.id}/>
          </${Card}>
        `}
        <div class="rail-add">
          <div>Right-rail panels</div>
          <div class="opts">
            ${AVAIL_PANELS.map(p => html`
              <span key=${p.id}
                    data-testid=${`rail-panel-toggle-${p.id}`}
                    class=${`opt ${panels[p.id] ? 'on' : ''}`}
                    onClick=${() => togglePanel(p.id)}>
                ${p.label}
              </span>
            `)}
          </div>
        </div>
      </div>
    </div>
  `
}
