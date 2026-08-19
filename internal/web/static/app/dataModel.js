// dataModel.js -- Adapt the GET /api/menu response shape into the bundle's session/group model.
//
// The API menu returns interleaved {type:'group'|'session', ...} items. The
// bundle's design treats sessions and groups as separate flat arrays with
// extra fields (kind, mcps, skills, cost, tokens, branch, worktree, sandbox).
//
// We project the API into that shape, defaulting absent fields to safe zeros
// so the design renders without inventing data. Components that need richer
// data (e.g. RightRail Usage card) fall back to "no data" placeholders.
import { computed } from '@preact/signals'
import {
  sessionsSignal, sessionCostsSignal, sessionContextSignal,
  sessionLiveModelSignal, sessionEstimatedCostSignal,
} from './state.js'

// kind heuristic from session metadata (no API field today).
// `tool` is `claude|codex|gemini|shell|webhook|...`; treat anything not in
// the agent set as a watcher. Conductor is detected by group convention.
function deriveKind(s) {
  if (!s || !s.tool) return 'agent'
  if (s.groupPath === 'conductor' || /conductor/i.test(s.title || '')) return 'conductor'
  if (['webhook', 'ntfy', 'slack-watcher'].includes(s.tool)) return 'watcher'
  return 'agent'
}

function projectSession(item) {
  const s = item.session || {}
  const id = s.id || ''
  const groupPath = s.groupPath || ''
  return {
    id,
    kind: deriveKind(s),
    title: s.title || id,
    group: groupPath,
    tool: s.tool || '',
    modelId: s.modelId || '',
    model: s.model || '',
    modelVersion: s.modelVersion || '',
    canFork: !!s.canFork,
    // Server-computed (session.ToolSupportsMCPManager). Default true so a
    // payload predating the field does not hide the MCP pane.
    mcpSupported: s.mcpSupported !== false,
    status: s.status || 'idle',
    branch: s.branch || '—',
    path: s.projectPath || '',
    cost: 0,            // hydrated separately via sessionCostsSignal
    // contextPercent: Gemini's rides in on the snapshot itself (s.contextPercent,
    // cheap — no file I/O). Claude's is hydrated separately below via
    // sessionContextSignal, since it needs a JSONL parse. null means "no bar".
    contextPercent: typeof s.contextPercent === 'number' && s.contextPercent > 0 ? s.contextPercent : null,
    tokens: 0,          // not exposed by API
    mcps: [],           // not exposed by API (TUI-only feature; pane shows stub)
    skills: [],         // not exposed by API (TUI-only feature; pane shows stub)
    children: [],       // not exposed by API
    // worktree: derived from MenuSession.worktreeBranch (issue #1126).
    // When truthy, the UI shows the "Finish worktree" action button so
    // users can merge + clean up from the browser instead of dropping
    // back to the TUI.
    worktree: !!(s.worktreeBranch && s.worktreeRepoRoot),
    worktreeBranch: s.worktreeBranch || '',
    lastAccessedAt: s.lastAccessedAt || '',
    createdAt: s.createdAt || '',
    sandbox: false,     // not exposed by API
    parent: null,
    pendingNeeds: 0,
    watcherType: null,
    routes: '',
    events1h: 0,
    meta: '',
    raw: s,
  }
}

function projectGroup(item) {
  const g = item.group || {}
  return {
    path: g.path || '',
    label: (g.name || g.path || '').toUpperCase(),
    expanded: !!g.expanded,
    sessionCount: g.sessionCount || 0,
    order: g.order || 0,
    kind: g.path === 'conductor' ? 'conductor' : g.path === 'watchers' ? 'watcher' : null,
  }
}

// Computed derived view: { groups: [...], sessions: [...], byGroup: { path -> sessions[] } }
export const menuModelSignal = computed(() => {
  const items = sessionsSignal.value || []
  const costs = sessionCostsSignal.value || {}
  const contexts = sessionContextSignal.value || {}
  const liveModels = sessionLiveModelSignal.value || {}
  const estimatedCosts = sessionEstimatedCostSignal.value || {}
  const groups = []
  const sessions = []
  for (const it of items) {
    if (!it) continue
    if (it.type === 'group') {
      groups.push(projectGroup(it))
    } else if (it.type === 'session') {
      const s = projectSession(it)
      const c = costs[s.id]
      if (typeof c === 'number' && c > 0) {
        s.cost = c
      } else if (typeof estimatedCosts[s.id] === 'number') {
        // No cost-ledger event for this session (hooks never wired up, or
        // just no billed event yet) — fall back to the transcript-derived
        // estimate so a plainly-used session doesn't read as free.
        // costEstimated flags it for the sidebar to render as "~$X".
        s.cost = estimatedCosts[s.id]
        s.costEstimated = true
      }
      const ctx = contexts[s.id]
      if (typeof ctx === 'number') s.contextPercent = ctx
      // Backend only sends a liveModel entry for sessions that had no
      // explicit launch model in the first place, but re-check here too —
      // belt and suspenders against ever letting a detected model steal
      // the chip from the one the user actually picked.
      if (!s.model && liveModels[s.id]) {
        s.model = liveModels[s.id].model || ''
        s.modelVersion = liveModels[s.id].version || ''
      }
      sessions.push(s)
    }
  }
  // ensure groups encountered via sessionPath also render even if API omitted them
  const seen = new Set(groups.map(g => g.path))
  for (const s of sessions) {
    if (s.group && !seen.has(s.group)) {
      groups.push({ path: s.group, label: s.group.toUpperCase(), expanded: true, sessionCount: 0, order: 999, kind: null })
      seen.add(s.group)
    }
  }
  groups.sort((a, b) => a.order - b.order)
  const byGroup = {}
  for (const s of sessions) (byGroup[s.group] ||= []).push(s)
  return { groups, sessions, byGroup }
})
