// CreateSessionDialog.js -- Modal form for creating a new session.
// Restyled (PR-B) to use the bundle's `.dialog` / `.dh` / `.db` / `.df` /
// `.field` / `.seg-row` / `.btn` classes from app.css.
//
// Extended to parity with the TUI's New Session dialog (internal/ui/
// newdialog.go): group, a searchable working-dir combo, worktree/sandbox/
// multi-repo toggles, and the Claude launch-options panel. See
// internal/web/api_types.go's CreateSessionRequest for the wire shape this
// builds.
import { html } from 'htm/preact'
import { useState } from 'preact/hooks'
import {
  createSessionDialogSignal, mutationsEnabledSignal,
  toolFilterFallbackSignal, pickerToolsSignal,
} from './state.js'
import { menuModelSignal } from './dataModel.js'
import { Icon, ICONS } from './icons.js'
import { apiFetch } from './api.js'
import { displayLabelForTool, resolveCreateSessionPickerTools } from './pickerTools.js'

const CUSTOM_MODEL = '__custom__'

const REASONING_EFFORT_CATALOG = {
  claude: [
    { value: 'low', label: 'Low' },
    { value: 'medium', label: 'Medium' },
    { value: 'high', label: 'High' },
    { value: 'xhigh', label: 'Extra high' },
    { value: 'max', label: 'Max' },
  ],
  codex: [
    { value: 'minimal', label: 'Minimal' },
    { value: 'low', label: 'Low' },
    { value: 'medium', label: 'Medium' },
    { value: 'high', label: 'High' },
    { value: 'xhigh', label: 'Extra high' },
  ],
}

const MODEL_ID_CATALOG = {
  claude: [
    { value: 'claude-opus-5', label: 'Claude Opus 5' },
    { value: 'claude-sonnet-5', label: 'Claude Sonnet 5' },
    { value: 'claude-fable-5', label: 'Claude Fable 5' },
    { value: 'claude-sonnet-4-6', label: 'Claude Sonnet 4.6' },
    { value: 'claude-opus-4-8', label: 'Claude Opus 4.8' },
    { value: 'claude-opus-4-7', label: 'Claude Opus 4.7' },
    { value: 'claude-haiku-4-5', label: 'Claude Haiku 4.5 alias' },
    { value: 'claude-haiku-4-5-20251001', label: 'Claude Haiku 4.5 pinned' },
  ],
  codex: [
    { value: 'gpt-5.6-sol', label: 'GPT-5.6 Sol' },
    { value: 'gpt-5.6-terra', label: 'GPT-5.6 Terra' },
    { value: 'gpt-5.6-luna', label: 'GPT-5.6 Luna' },
    { value: 'gpt-5.5', label: 'GPT-5.5' },
    { value: 'gpt-5.5-pro', label: 'GPT-5.5 Pro' },
    { value: 'gpt-5.4', label: 'GPT-5.4' },
    { value: 'gpt-5.4-pro', label: 'GPT-5.4 Pro' },
    { value: 'gpt-5.4-mini', label: 'GPT-5.4 Mini' },
    { value: 'gpt-5.4-nano', label: 'GPT-5.4 Nano' },
    { value: 'gpt-5.3-codex', label: 'GPT-5.3 Codex' },
    { value: 'gpt-5.2', label: 'GPT-5.2' },
    { value: 'gpt-5.2-pro', label: 'GPT-5.2 Pro' },
    { value: 'gpt-5.1', label: 'GPT-5.1' },
    { value: 'gpt-5-pro', label: 'GPT-5 Pro' },
    { value: 'gpt-5', label: 'GPT-5' },
    { value: 'gpt-5-mini', label: 'GPT-5 Mini' },
    { value: 'gpt-5-nano', label: 'GPT-5 Nano' },
    { value: 'gpt-4.1', label: 'GPT-4.1' },
    { value: 'gpt-4.1-mini', label: 'GPT-4.1 Mini' },
    { value: 'gpt-4o', label: 'GPT-4o' },
    { value: 'gpt-4o-mini', label: 'GPT-4o Mini' },
    { value: 'o3-pro', label: 'o3 Pro' },
    { value: 'o3', label: 'o3' },
  ],
  gemini: [
    { value: 'gemini-3.1-pro-preview', label: 'Gemini 3.1 Pro preview' },
    { value: 'gemini-3.1-pro-preview-customtools', label: 'Gemini 3.1 Pro custom tools' },
    { value: 'gemini-3-flash-preview', label: 'Gemini 3 Flash preview' },
    { value: 'gemini-3.1-flash-lite', label: 'Gemini 3.1 Flash Lite' },
    { value: 'gemini-3.1-flash-lite-preview', label: 'Gemini 3.1 Flash Lite preview' },
    { value: 'gemini-2.5-pro', label: 'Gemini 2.5 Pro' },
    { value: 'gemini-2.5-flash', label: 'Gemini 2.5 Flash' },
    { value: 'gemini-2.5-flash-lite', label: 'Gemini 2.5 Flash Lite' },
  ],
  opencode: [
    { value: 'openai/gpt-5.5', label: 'OpenAI GPT-5.5' },
    { value: 'openai/gpt-5.5-pro', label: 'OpenAI GPT-5.5 Pro' },
    { value: 'openai/gpt-5.4', label: 'OpenAI GPT-5.4' },
    { value: 'openai/gpt-5.4-pro', label: 'OpenAI GPT-5.4 Pro' },
    { value: 'openai/gpt-5.4-mini', label: 'OpenAI GPT-5.4 Mini' },
    { value: 'openai/gpt-5.3-codex', label: 'OpenAI GPT-5.3 Codex' },
    { value: 'openai/gpt-5', label: 'OpenAI GPT-5' },
    { value: 'openai/o3', label: 'OpenAI o3' },
    { value: 'anthropic/claude-opus-5', label: 'Anthropic Claude Opus 5' },
    { value: 'anthropic/claude-sonnet-5', label: 'Anthropic Claude Sonnet 5' },
    { value: 'anthropic/claude-fable-5', label: 'Anthropic Claude Fable 5' },
    { value: 'anthropic/claude-sonnet-4-6', label: 'Anthropic Claude Sonnet 4.6' },
    { value: 'anthropic/claude-opus-4-8', label: 'Anthropic Claude Opus 4.8' },
    { value: 'anthropic/claude-opus-4-7', label: 'Anthropic Claude Opus 4.7' },
    { value: 'anthropic/claude-haiku-4-5', label: 'Anthropic Claude Haiku 4.5' },
  ],
}

function modelIDsForTool(tool) {
  return MODEL_ID_CATALOG[tool] || []
}

function reasoningEffortsForTool(tool) {
  return REASONING_EFFORT_CATALOG[tool] || []
}

// slugifyBranch turns a session title into a reasonable default worktree
// branch suffix — same shape git.SanitizeBranchName produces server-side
// (lowercased, non-alnum runs collapsed to '-'), so the prefilled value
// rarely needs editing.
function slugifyBranch(title) {
  return title.trim().toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '')
}

// recentProjectPaths returns known project paths (from the live session
// list), most-recently-used first, deduplicated — the same source and order
// the TUI's own path-suggestion dropdown uses (session.LastAccessedAt).
function recentProjectPaths() {
  const sessions = menuModelSignal.value.sessions || []
  const byPath = new Map()
  for (const s of sessions) {
    if (!s.path) continue
    const stamp = s.lastAccessedAt || s.createdAt || ''
    const existing = byPath.get(s.path)
    if (!existing || stamp > existing.stamp) byPath.set(s.path, { path: s.path, group: s.group, stamp })
  }
  return [...byPath.values()].sort((a, b) => (a.stamp < b.stamp ? 1 : a.stamp > b.stamp ? -1 : 0))
}

export function CreateSessionDialog() {
  const [title, setTitle] = useState('')
  const [groupPath, setGroupPath] = useState('')
  const [tool, setTool] = useState('claude')
  const [modelId, setModelId] = useState('')
  const [customModel, setCustomModel] = useState('')
  const [reasoningEffort, setReasoningEffort] = useState('')
  const [path, setPath] = useState('')
  const [pathMenuOpen, setPathMenuOpen] = useState(false)
  const [worktreeEnabled, setWorktreeEnabled] = useState(false)
  const [branch, setBranch] = useState('')
  const [branchTouched, setBranchTouched] = useState(false)
  const [sandboxEnabled, setSandboxEnabled] = useState(false)
  const [multiRepoEnabled, setMultiRepoEnabled] = useState(false)
  const [additionalPaths, setAdditionalPaths] = useState([''])
  // Claude launch options (internal/ui/claudeoptions.go parity).
  const [sessionMode, setSessionMode] = useState('new')
  const [resumeSessionId, setResumeSessionId] = useState('')
  const [skipPermissions, setSkipPermissions] = useState(false)
  const [autoMode, setAutoMode] = useState(false)
  const [useChrome, setUseChrome] = useState(false)
  const [useTeammateMode, setUseTeammateMode] = useState(false)
  const [extraArgs, setExtraArgs] = useState('')
  const [startQuery, setStartQuery] = useState('')
  const [error, setError] = useState(null)
  const [submitting, setSubmitting] = useState(false)

  // WEB-P0-4 prevention layer: when mutations are disabled (server
  // webMutations=false), do not render the dialog at all. Hooks order is
  // preserved by placing this guard AFTER all useState calls.
  if (!mutationsEnabledSignal.value) return null

  function toggleWorktree() {
    const next = !worktreeEnabled
    setWorktreeEnabled(next)
    if (next && !branchTouched) setBranch('feature/' + (slugifyBranch(title) || 'session'))
  }

  async function handleSubmit(e) {
    e.preventDefault()
    setError(null)
    setSubmitting(true)
    try {
      const payload = { title, tool, projectPath: path }
      if (groupPath) payload.groupPath = groupPath
      const modelIdValue = selectedModelId()
      if (modelIdValue) payload.modelId = modelIdValue
      if (reasoningEffort) payload.reasoningEffort = reasoningEffort
      if (worktreeEnabled && branch.trim()) {
        payload.worktree = true
        payload.branch = branch.trim()
      }
      if (sandboxEnabled) payload.sandbox = true
      const repos = additionalPaths.map(p => p.trim()).filter(Boolean)
      if (multiRepoEnabled && repos.length > 0) {
        payload.multiRepo = true
        payload.additionalPaths = repos
      }
      if (tool === 'claude') {
        const claude = {}
        if (sessionMode !== 'new') claude.sessionMode = sessionMode
        if (sessionMode === 'resume' && resumeSessionId.trim()) claude.resumeSessionId = resumeSessionId.trim()
        if (skipPermissions) claude.skipPermissions = true
        if (autoMode) claude.autoMode = true
        if (useChrome) claude.useChrome = true
        if (useTeammateMode) claude.useTeammateMode = true
        if (extraArgs.trim()) claude.extraArgs = extraArgs.trim()
        if (startQuery.trim()) claude.startQuery = startQuery.trim()
        if (Object.keys(claude).length > 0) payload.claude = claude
      }
      await apiFetch('POST', '/api/sessions', payload)
      createSessionDialogSignal.value = false
    } catch (err) {
      setError(err.message)
    } finally {
      setSubmitting(false)
    }
  }

  function selectTool(nextTool) {
    setTool(nextTool)
    setModelId('')
    setCustomModel('')
    setReasoningEffort('')
  }

  function selectedModelId() {
    if (modelId === CUSTOM_MODEL) return customModel.trim()
    return modelId || ''
  }

  function pickPath(p) {
    setPath(p)
    setPathMenuOpen(false)
  }

  function setAdditionalPath(i, value) {
    setAdditionalPaths(prev => prev.map((p, idx) => (idx === i ? value : p)))
  }
  function addAdditionalPath() {
    setAdditionalPaths(prev => [...prev, ''])
  }
  function removeAdditionalPath(i) {
    setAdditionalPaths(prev => (prev.length > 1 ? prev.filter((_, idx) => idx !== i) : ['']))
  }

  const close = () => (createSessionDialogSignal.value = false)
  const handleBackdropClick = (e) => { if (e.target === e.currentTarget) close() }
  const modelIDs = modelIDsForTool(tool)
  const reasoningEfforts = reasoningEffortsForTool(tool)
  const shownTools = resolveCreateSessionPickerTools(pickerToolsSignal.value)
  const needsCustomModel = modelId === CUSTOM_MODEL
  const groups = menuModelSignal.value.groups || []
  const allPaths = recentProjectPaths()
  const pathQuery = path.trim().toLowerCase()
  const filteredPaths = pathQuery
    ? allPaths.filter(p => p.path.toLowerCase().includes(pathQuery))
    : allPaths
  const submitDisabled = submitting || !title || !path
    || (needsCustomModel && !customModel.trim())
    || (worktreeEnabled && !branch.trim())

  return html`
    <div class="overlay" onClick=${handleBackdropClick}>
      <form class="dialog" onClick=${e => e.stopPropagation()} onSubmit=${handleSubmit}>
        <div class="dh">
          <span class="kicker">NEW</span>
          <div class="t">New session</div>
          <button type="button" class="icon-btn" onClick=${close} aria-label="Close">
            <${Icon} d=${ICONS.x}/>
          </button>
        </div>
        <div class="db">
          <div class="field">
            <label>TITLE</label>
            <input autofocus required value=${title} onInput=${e => setTitle(e.target.value)} placeholder="my-session"/>
          </div>

          ${groups.length > 0 && html`
            <div class="field">
              <label>GROUP</label>
              <select value=${groupPath} onInput=${e => setGroupPath(e.target.value)}>
                <option value="">default</option>
                ${groups.filter(g => g.path && g.path !== 'default').map(g => html`
                  <option key=${g.path} value=${g.path}>${g.label || g.path}</option>
                `)}
              </select>
            </div>
          `}

          <div class="field">
            <label>WORKING DIR</label>
            <div class="combo">
              <input required value=${path}
                     onInput=${e => { setPath(e.target.value); setPathMenuOpen(true) }}
                     onFocus=${() => setPathMenuOpen(true)}
                     onBlur=${() => setTimeout(() => setPathMenuOpen(false), 120)}
                     placeholder="/absolute/path/to/project"/>
              ${allPaths.length > 0 && html`
                <button type="button" class="combo-chev" aria-label="Show recent paths"
                        onClick=${() => setPathMenuOpen(v => !v)}>▾</button>
              `}
            </div>
            ${pathMenuOpen && filteredPaths.length > 0 && html`
              <div class="combo-menu">
                ${filteredPaths.slice(0, 8).map(p => html`
                  <div key=${p.path} class="cm-row" onMouseDown=${e => e.preventDefault()} onClick=${() => pickPath(p.path)}>
                    <span class="cm-path">${p.path}</span>
                    ${p.group && html`<span class="cm-group">${p.group}</span>`}
                  </div>
                `)}
                <div class="cm-foot">from your existing sessions · keep typing for any other path</div>
              </div>
            `}
          </div>

          <div class="field">
            <label>TOOL</label>
            <div class="seg-row">
              ${shownTools.map(t => html`
                <button type="button" key=${t}
                        class=${`seg-btn ${tool === t ? 'on' : ''}`}
                        onClick=${() => selectTool(t)}>${displayLabelForTool(t)}</button>
              `)}
            </div>
            ${toolFilterFallbackSignal.value && html`
              <div style="font-family: var(--mono); font-size: 11px; color: var(--tn-comment, #888);
                          margin-top: 6px;">
                No tools matched PATH; showing all. Set <code>show_only_installed_tools = false</code> to silence.
              </div>
            `}
          </div>

          ${modelIDs.length > 0 && html`
            <div class="opts-row2">
              <div class="field">
                <label>MODEL ID</label>
                <select value=${modelId} onInput=${e => setModelId(e.target.value)}>
                  <option value="">Tool default</option>
                  ${modelIDs.map(m => html`
                    <option key=${m.value} value=${m.value}>${m.value} — ${m.label}</option>
                  `)}
                  <option value=${CUSTOM_MODEL}>Custom model ID…</option>
                </select>
              </div>
              ${reasoningEfforts.length > 0 ? html`
                <div class="field">
                  <label>REASONING EFFORT</label>
                  <select value=${reasoningEffort} onInput=${e => setReasoningEffort(e.target.value)}>
                    <option value="">Tool default</option>
                    ${reasoningEfforts.map(effort => html`
                      <option key=${effort.value} value=${effort.value}>${effort.label} — ${effort.value}</option>
                    `)}
                  </select>
                </div>
              ` : html`<div></div>`}
            </div>
            ${needsCustomModel && html`
              <div class="field">
                <label>MODEL ID</label>
                <input required value=${customModel} onInput=${e => setCustomModel(e.target.value)} placeholder="provider/model-or-version"/>
              </div>
            `}
          `}

          <div class="opts-grid">
            <label class=${`check-row ${worktreeEnabled ? 'on' : ''}`}>
              <input type="checkbox" style="display:none" checked=${worktreeEnabled} onChange=${toggleWorktree}/>
              <span class="cbox">${worktreeEnabled ? '✓' : ''}</span>
              <span class="lbl">Create in worktree</span>
              <span class="hint">isolated git worktree + branch</span>
            </label>
            ${worktreeEnabled && html`
              <div class="reveal">
                <div class="field">
                  <label>BRANCH</label>
                  <input required value=${branch}
                         onInput=${e => { setBranch(e.target.value); setBranchTouched(true) }}
                         placeholder="feature/branch-name"/>
                </div>
              </div>
            `}

            <label class=${`check-row ${sandboxEnabled ? 'on' : ''}`}>
              <input type="checkbox" style="display:none" checked=${sandboxEnabled} onChange=${() => setSandboxEnabled(v => !v)}/>
              <span class="cbox">${sandboxEnabled ? '✓' : ''}</span>
              <span class="lbl">Run in Docker sandbox</span>
              <span class="hint">isolated container</span>
            </label>

            <label class=${`check-row ${multiRepoEnabled ? 'on' : ''}`}>
              <input type="checkbox" style="display:none" checked=${multiRepoEnabled} onChange=${() => setMultiRepoEnabled(v => !v)}/>
              <span class="cbox">${multiRepoEnabled ? '✓' : ''}</span>
              <span class="lbl">Multi-repo mode</span>
              <span class="hint">attach several repos to one session</span>
            </label>
            ${multiRepoEnabled && html`
              <div class="reveal">
                ${additionalPaths.map((p, i) => html`
                  <div class="field" key=${i} style="flex-direction: row; align-items: center; gap: 6px;">
                    <input style="flex:1" value=${p} onInput=${e => setAdditionalPath(i, e.target.value)}
                           placeholder="/absolute/path/to/other-repo"/>
                    <button type="button" class="icon-btn" onClick=${() => removeAdditionalPath(i)} aria-label="Remove repo">
                      <${Icon} d=${ICONS.x}/>
                    </button>
                  </div>
                `)}
                <button type="button" class="btn ghost" onClick=${addAdditionalPath} style="align-self: flex-start;">+ Add repo</button>
              </div>
            `}
          </div>

          ${tool === 'claude' && html`
            <div class="sec-head"><span class="line"></span><span class="label">Claude options</span><span class="line"></span></div>

            <div class="field">
              <label>SESSION</label>
              <div class="seg-row">
                <button type="button" class=${`seg-btn ${sessionMode === 'new' ? 'on' : ''}`} onClick=${() => setSessionMode('new')}>New</button>
                <button type="button" class=${`seg-btn ${sessionMode === 'continue' ? 'on' : ''}`} onClick=${() => setSessionMode('continue')}>Continue</button>
                <button type="button" class=${`seg-btn ${sessionMode === 'resume' ? 'on' : ''}`} onClick=${() => setSessionMode('resume')}>Resume</button>
              </div>
            </div>
            ${sessionMode === 'resume' && html`
              <div class="field">
                <label>RESUME SESSION ID</label>
                <input value=${resumeSessionId} onInput=${e => setResumeSessionId(e.target.value)} placeholder="claude conversation UUID"/>
              </div>
            `}

            <div class="opts-row2">
              <label class=${`check-row ${skipPermissions ? 'on' : ''}`}>
                <input type="checkbox" style="display:none" checked=${skipPermissions} onChange=${() => setSkipPermissions(v => !v)}/>
                <span class="cbox">${skipPermissions ? '✓' : ''}</span>
                <span class="lbl">Skip permissions</span>
              </label>
              <label class=${`check-row ${autoMode ? 'on' : ''}`}>
                <input type="checkbox" style="display:none" checked=${autoMode} onChange=${() => setAutoMode(v => !v)}/>
                <span class="cbox">${autoMode ? '✓' : ''}</span>
                <span class="lbl">Auto mode</span>
              </label>
              <label class=${`check-row ${useChrome ? 'on' : ''}`}>
                <input type="checkbox" style="display:none" checked=${useChrome} onChange=${() => setUseChrome(v => !v)}/>
                <span class="cbox">${useChrome ? '✓' : ''}</span>
                <span class="lbl">Chrome mode</span>
              </label>
              <label class=${`check-row ${useTeammateMode ? 'on' : ''}`}>
                <input type="checkbox" style="display:none" checked=${useTeammateMode} onChange=${() => setUseTeammateMode(v => !v)}/>
                <span class="cbox">${useTeammateMode ? '✓' : ''}</span>
                <span class="lbl">Teammate mode</span>
              </label>
            </div>

            <div class="field">
              <label>EXTRA ARGS</label>
              <input value=${extraArgs} onInput=${e => setExtraArgs(e.target.value)} placeholder="--agent reviewer --model opus"/>
            </div>

            <div class="field">
              <label>START QUERY</label>
              <input value=${startQuery} onInput=${e => setStartQuery(e.target.value)} placeholder="initial prompt (not split on spaces)"/>
            </div>
          `}

          ${error && html`
            <div style="font-family: var(--mono); font-size: 11.5px; color: var(--tn-red); padding: 8px 10px;
                        border: 1px solid rgba(247,118,142,0.3); border-radius: 4px; background: rgba(247,118,142,0.06);">
              ${error}
            </div>
          `}
        </div>
        <div class="df">
          <button type="button" class="btn ghost" onClick=${close}>Cancel</button>
          <button type="submit" class="btn primary" disabled=${submitDisabled}>
            ${submitting ? 'Creating…' : html`Create session <span class="kbd">⏎</span>`}
          </button>
        </div>
      </form>
    </div>
  `
}
