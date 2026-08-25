// VoiceRecorder.js -- Mic button for the web terminal toolbar (term-strip).
//
// Tap to start recording, tap again to stop (not push-to-talk -- awkward
// one-handed on a phone for a multi-sentence voice command). On stop, the
// recorded clip uploads to POST /api/sessions/{id}/transcribe (local
// whisper.cpp server-side -- see internal/voice); the response text is
// handed to the caller via onTranscript, which TerminalPanel.js wires to
// the same sendInput() path paste already uses. No speech ever reaches a
// third party -- transcription happens on the user's own Mac.
//
// iOS Safari has no in-browser SpeechRecognition API at all, so this is a
// record-then-transcribe flow rather than live dictation. getUserMedia +
// MediaRecorder (the recording mechanism) DO work on iOS Safari, given a
// secure context (https, or a Tailscale-HTTPS-served PWA -- see CLAUDE.md).
import { html } from 'htm/preact'
import { useEffect, useRef, useState } from 'preact/hooks'
import { authHeaders } from './api.js'
import { addToast } from './Toast.js'
import { Icon, ICONS } from './icons.js'
import { voiceInputAvailableSignal } from './state.js'

// MediaRecorder MIME candidates in preference order. Safari only accepts
// audio/mp4 (AAC); Chrome/Android and Firefox accept the webm/ogg opus
// variants. The backend's ffmpeg conversion step accepts whichever one the
// browser actually picks, so the client just needs to find one that works.
const MIME_CANDIDATES = [
  'audio/mp4',
  'audio/webm;codecs=opus',
  'audio/webm',
  'audio/ogg;codecs=opus',
]

function pickMimeType() {
  if (typeof MediaRecorder === 'undefined' || !MediaRecorder.isTypeSupported) return ''
  return MIME_CANDIDATES.find((t) => MediaRecorder.isTypeSupported(t)) || ''
}

function browserSupportsRecording() {
  return !!(navigator.mediaDevices && navigator.mediaDevices.getUserMedia) &&
    typeof MediaRecorder !== 'undefined'
}

// Auto-stop safeguard: a stuck-open recording (e.g. the user forgets it's
// running) shouldn't record indefinitely or grow an unbounded upload.
const MAX_RECORDING_MS = 60_000

export function VoiceMicButton({ sessionId, disabled, onTranscript }) {
  const [state, setState] = useState('idle') // idle | recording | transcribing
  const recorderRef = useRef(null)
  const streamRef = useRef(null)
  const chunksRef = useRef([])
  const autoStopTimerRef = useRef(null)
  const mountedRef = useRef(true)

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      clearTimeout(autoStopTimerRef.current)
      // Unmounting mid-recording (session switch, panel close) must release
      // the mic -- otherwise the browser's recording indicator stays lit
      // for a session no longer on screen.
      if (recorderRef.current && recorderRef.current.state !== 'inactive') {
        try { recorderRef.current.stop() } catch (_e) { /* already stopped */ }
      }
      if (streamRef.current) {
        streamRef.current.getTracks().forEach((t) => t.stop())
        streamRef.current = null
      }
    }
  }, [])

  if (!voiceInputAvailableSignal.value || !browserSupportsRecording()) {
    return null
  }

  async function startRecording() {
    let stream
    try {
      stream = await navigator.mediaDevices.getUserMedia({ audio: true })
    } catch (err) {
      const denied = err && (err.name === 'NotAllowedError' || err.name === 'PermissionDeniedError')
      addToast(denied ? 'Microphone permission denied' : 'Could not access microphone')
      return
    }
    if (!mountedRef.current) {
      stream.getTracks().forEach((t) => t.stop())
      return
    }

    streamRef.current = stream
    chunksRef.current = []
    const mimeType = pickMimeType()
    let recorder
    try {
      recorder = mimeType ? new MediaRecorder(stream, { mimeType }) : new MediaRecorder(stream)
    } catch (_err) {
      addToast('Recording is not supported in this browser')
      stream.getTracks().forEach((t) => t.stop())
      return
    }
    recorderRef.current = recorder

    recorder.ondataavailable = (event) => {
      if (event.data && event.data.size > 0) chunksRef.current.push(event.data)
    }
    recorder.onstop = () => {
      streamRef.current?.getTracks().forEach((t) => t.stop())
      streamRef.current = null
      const blob = new Blob(chunksRef.current, { type: recorder.mimeType || mimeType || 'audio/webm' })
      chunksRef.current = []
      if (mountedRef.current) uploadAndTranscribe(blob)
    }

    recorder.start()
    setState('recording')
    autoStopTimerRef.current = setTimeout(() => stopRecording(), MAX_RECORDING_MS)
  }

  function stopRecording() {
    clearTimeout(autoStopTimerRef.current)
    const recorder = recorderRef.current
    if (recorder && recorder.state !== 'inactive') {
      setState('transcribing')
      recorder.stop()
    }
  }

  async function uploadAndTranscribe(blob) {
    try {
      const res = await fetch(`/api/sessions/${encodeURIComponent(sessionId)}/transcribe`, {
        method: 'POST',
        // authHeaders() defaults to application/json; the override here
        // must come after the spread (api.js's authHeaders supports this --
        // `extra` spreads after the defaults) so the real audio content
        // type wins.
        headers: authHeaders({ 'Content-Type': blob.type || 'application/octet-stream' }),
        body: blob,
      })
      const data = await res.json().catch(() => null)
      if (!res.ok) {
        addToast(data?.error?.message || 'Transcription failed')
        return
      }
      const text = data?.text || ''
      if (!text) {
        addToast('No speech detected', 'info')
        return
      }
      onTranscript?.(text)
    } catch (_err) {
      addToast('Network error while transcribing')
    } finally {
      if (mountedRef.current) setState('idle')
    }
  }

  function handleClick() {
    if (disabled || state === 'transcribing') return
    if (state === 'recording') {
      stopRecording()
    } else {
      startRecording()
    }
  }

  const title = disabled
    ? 'Voice input disabled (read-only)'
    : state === 'recording' ? 'Stop recording'
    : state === 'transcribing' ? 'Transcribing...'
    : 'Voice input'

  return html`
    <button
      type="button"
      class=${`mic-btn${state === 'recording' ? ' recording' : ''}${state === 'transcribing' ? ' busy' : ''}`}
      disabled=${disabled || state === 'transcribing'}
      title=${title}
      aria-label=${title}
      aria-pressed=${state === 'recording'}
      onClick=${handleClick}
    >
      <${Icon} d=${ICONS.mic} size=${13}/>
    </button>
  `
}
