<script setup lang="ts">
import {
  nextTick,
  onBeforeUnmount,
  onMounted,
  ref,
} from 'vue'
import { Call, Events } from '@wailsio/runtime'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'

interface Container {
  id: string
  name: string
  shortId: string
}

interface StreamOutputEvent {
  streamId: string
  containerId: string
  dataBase64?: string
  error?: string
  closed?: boolean
  seq: number
}

const props = defineProps<{
  container: Container
}>()

const emit = defineEmits<{
  close: []
}>()

const terminalHost = ref<HTMLElement | null>(null)
const status = ref<'connecting' | 'connected' | 'closed'>('connecting')
const commandDraft = ref('')
const commandInput = ref<HTMLTextAreaElement | null>(null)

let terminal: Terminal | undefined
let fitAddon: FitAddon | undefined
let sessionID = ''
let unsubscribeEvent: (() => void) | undefined
let resizeObserver: ResizeObserver | undefined
let inputDisposable: { dispose(): void } | undefined
let binaryDisposable: { dispose(): void } | undefined
let resizeDisposable: { dispose(): void } | undefined
let resizeTimer: ReturnType<typeof setTimeout> | undefined

async function start() {
  await nextTick()

  if (!terminalHost.value) {
    return
  }

  terminal = new Terminal({
    cursorBlink: true,
    cursorStyle: 'block',
    convertEol: false,
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
    fontSize: 13,
    lineHeight: 1.2,
    scrollback: 5000,
    allowProposedApi: false,
    theme: {
      background: '#000000',
      foreground: '#d4d4d8',
      cursor: '#e4e4e7',
      selectionBackground: '#3f3f46',
      black: '#18181b',
      red: '#f87171',
      green: '#4ade80',
      yellow: '#facc15',
      blue: '#60a5fa',
      magenta: '#c084fc',
      cyan: '#00e5ff',
      white: '#e4e4e7',
    },
  })

  fitAddon = new FitAddon()
  terminal.loadAddon(fitAddon)
  terminal.open(terminalHost.value)
  fitAddon.fit()

  terminal.writeln('\x1b[90mDockiva: connecting to /bin/sh...\x1b[0m')

  unsubscribeEvent = Events.On(
    'dockiva:terminal-output',
    (payload: any) => {
      // Wails v3 delivers a WailsEvent wrapper. Its custom event payload is
      // under `.data`, not on the wrapper itself.
      const data = (payload?.data ?? payload) as StreamOutputEvent

      const matchesSession = sessionID
        ? data?.streamId === sessionID
        : data?.containerId === props.container.id

      if (!data || !matchesSession) {
        return
      }

      if (data.dataBase64) {
        // Raw bytes: xterm.js decodes UTF-8 itself, across chunk boundaries.
        terminal?.write(Uint8Array.from(atob(data.dataBase64), (c) => c.charCodeAt(0)))
      }

      if (data.error) {
        terminal?.writeln(`\r\n\x1b[31m[Dockiva] ${data.error}\x1b[0m`)
      }

      if (data.closed) {
        status.value = 'closed'
        terminal?.writeln('\r\n\x1b[90m[session closed]\x1b[0m')
      }
    },
  )

  try {
    sessionID = await Call.ByName(
      'main.DockerService.StartTerminal',
      props.container.id,
    ) as string

    status.value = 'connected'

    inputDisposable = terminal.onData((data) => {
      if (!sessionID || status.value !== 'connected') {
        return
      }

      void Call.ByName(
        'main.DockerService.SendTerminalInput',
        sessionID,
        data,
      )
    })

    // Non-UTF-8 input (X10 mouse reports) arrives as a binary string, one char per byte.
    binaryDisposable = terminal.onBinary((data) => {
      if (!sessionID || status.value !== 'connected') {
        return
      }

      void Call.ByName(
        'main.DockerService.SendTerminalBytes',
        sessionID,
        btoa(data),
      )
    })

    resizeDisposable = terminal.onResize(({ cols, rows }) => {
      scheduleResize(cols, rows)
    })

    resizeObserver = new ResizeObserver(() => {
      fitAddon?.fit()
    })

    resizeObserver.observe(terminalHost.value)

    fitAddon.fit()
    terminal.focus()

    scheduleResize(terminal.cols, terminal.rows)
  } catch (err) {
    status.value = 'closed'
    terminal.writeln(
      `\r\n\x1b[31m[Dockiva] ${
        err instanceof Error ? err.message : String(err)
      }\x1b[0m`,
    )
  }
}

function scheduleResize(cols: number, rows: number) {
  if (!sessionID || status.value !== 'connected') {
    return
  }

  if (resizeTimer) {
    clearTimeout(resizeTimer)
  }

  resizeTimer = setTimeout(() => {
    void Call.ByName(
      'main.DockerService.ResizeTerminal',
      sessionID,
      cols,
      rows,
    )
  }, 80)
}

function focusTerminal() {
  commandInput.value?.focus()
}

function clearTerminal() {
  terminal?.clear()
  commandInput.value?.focus()
}

async function runDraftCommand() {
  const command = commandDraft.value
  if (!sessionID || status.value !== 'connected' || command.trim() === '') {
    return
  }

  commandDraft.value = ''
  try {
    await Call.ByName(
      'main.DockerService.SendTerminalInput',
      sessionID,
      `${command}\n`,
    )
  } catch (err) {
    terminal?.writeln(
      `\r\n\x1b[31m[Dockiva] ${err instanceof Error ? err.message : String(err)}\x1b[0m`,
    )
  } finally {
    commandInput.value?.focus()
  }
}

async function closeSession() {
  const currentSession = sessionID
  sessionID = ''
  status.value = 'closed'

  if (currentSession) {
    try {
      await Call.ByName(
        'main.DockerService.CloseTerminal',
        currentSession,
      )
    } catch {
      // Closing is best-effort.
    }
  }

  emit('close')
}

onMounted(start)

onBeforeUnmount(() => {
  if (resizeTimer) {
    clearTimeout(resizeTimer)
  }

  resizeObserver?.disconnect()
  unsubscribeEvent?.()
  inputDisposable?.dispose()
  binaryDisposable?.dispose()
  resizeDisposable?.dispose()
  terminal?.dispose()

  const currentSession = sessionID
  sessionID = ''

  if (currentSession) {
    void Call.ByName(
      'main.DockerService.CloseTerminal',
      currentSession,
    )
  }
})
</script>

<template>
  <div
    class="terminal-backdrop fixed inset-0 z-[70] flex items-center justify-center p-5"
    @click.self="closeSession"
  >
    <section
      class="terminal-shell flex h-[88vh] w-full max-w-7xl flex-col overflow-hidden bg-black"
    >
      <header
        class="terminal-header flex items-center justify-between gap-4 px-4 py-3"
      >
        <div class="min-w-0">
          <div class="flex items-center gap-2">
            <span class="terminal-mark">›_</span>
            <strong class="truncate font-mono text-sm text-zinc-100">
              {{ container.name || container.shortId }}
            </strong>

            <span
              class="rounded-full px-2 py-0.5 text-[10px]"
              :class="
                status === 'connected'
                  ? 'bg-emerald-500/10 text-emerald-400'
                  : status === 'connecting'
                    ? 'bg-amber-500/10 text-amber-400'
                    : 'bg-red-500/10 text-red-400'
              "
            >
              {{ status }}
            </span>
          </div>

          <div class="mt-1 flex items-center gap-2 text-[11px] text-zinc-500">
            <span>Native container shell</span>
            <span class="text-zinc-700">•</span>
            <code>/bin/sh</code>
            <span v-if="status === 'connected'" class="terminal-live">live</span>
          </div>
        </div>

        <div class="flex shrink-0 items-center gap-2">
          <button class="terminal-action" title="Focus terminal" @click="focusTerminal">
            Focus
          </button>
          <button class="terminal-action" title="Clear terminal output" @click="clearTerminal">
            Clear
          </button>
          <button class="terminal-close" @click="closeSession">Close</button>
        </div>
      </header>

      <div class="terminal-canvas min-h-0 flex-1 overflow-hidden p-3" @click="focusTerminal">
        <div ref="terminalHost" class="h-full" />
      </div>

      <div class="terminal-composer px-4 py-3">
        <span class="composer-prompt" aria-hidden="true">›</span>
        <textarea
          ref="commandInput"
          v-model="commandDraft"
          class="composer-input"
          rows="1"
          :disabled="status !== 'connected'"
          placeholder="Write a command…"
          spellcheck="false"
          @keydown.enter.exact.prevent="runDraftCommand"
        />
        <button
          class="composer-run"
          :disabled="status !== 'connected' || commandDraft.trim() === ''"
          @click="runDraftCommand"
        >
          Run <kbd>↵</kbd>
        </button>
      </div>

      <footer class="terminal-footer flex items-center justify-between px-4 py-2 text-[10px] text-zinc-500">
        <span>Editable command bar · output stays in this session only</span>
        <span class="font-mono text-zinc-600">Shift + Enter for a new line</span>
      </footer>
    </section>
  </div>
</template>

<style scoped>
.terminal-backdrop { background:rgb(3 4 8/.84); backdrop-filter:blur(14px); }
.terminal-shell { border:1px solid rgb(255 255 255/.12); border-radius:1rem; box-shadow:0 30px 90px rgb(0 0 0/.6),inset 0 1px rgb(255 255 255/.05); }
.terminal-header { border-bottom:1px solid rgb(255 255 255/.07); background:linear-gradient(90deg,#121722,#0c1018); }
.terminal-mark { display:grid; width:1.65rem; height:1.65rem; place-items:center; border-radius:.42rem; background:rgb(34 211 238/.12); color:#67e8f9; font:700 .77rem/1 ui-monospace,monospace; }
.terminal-live { border:1px solid rgb(74 222 128/.2); border-radius:999px; padding:.08rem .36rem; color:#4ade80; }
.terminal-action,.terminal-close { border-radius:.4rem; padding:.36rem .62rem; font-size:.72rem; transition:background .15s,border-color .15s; }
.terminal-action { border:1px solid rgb(255 255 255/.1); background:#161b25; color:#a1a1aa; }
.terminal-action:hover { background:#232a38; color:#e4e4e7; }
.terminal-close { border:1px solid rgb(248 113 113/.23); background:rgb(127 29 29/.22); color:#fca5a5; }
.terminal-close:hover { background:rgb(127 29 29/.42); }
.terminal-canvas { background:radial-gradient(circle at 50% -30%,rgb(34 211 238/.05),transparent 42%),#05070a; }
.terminal-composer { display:flex; align-items:center; gap:.7rem; border-top:1px solid rgb(255 255 255/.07); background:#10151e; }
.composer-prompt { color:#67e8f9; font:700 1.2rem/1 ui-monospace,monospace; }
.composer-input { min-width:0; flex:1; resize:none; border:1px solid transparent; border-radius:.5rem; outline:none; background:#090d13; padding:.55rem .7rem; color:#e4e4e7; font:.8rem/1.35 ui-monospace,SFMono-Regular,Menlo,monospace; }
.composer-input:focus { border-color:rgb(34 211 238/.5); box-shadow:0 0 0 3px rgb(34 211 238/.09); }
.composer-input:disabled { cursor:not-allowed; opacity:.5; }
.composer-run { border:1px solid rgb(34 211 238/.28); border-radius:.45rem; background:rgb(8 145 178/.2); padding:.48rem .65rem; color:#a5f3fc; font-size:.72rem; }
.composer-run:hover:not(:disabled) { background:rgb(8 145 178/.38); }
.composer-run:disabled { cursor:not-allowed; opacity:.4; }
.composer-run kbd { margin-left:.22rem; color:#67e8f9; font-family:ui-monospace,monospace; }
.terminal-footer { border-top:1px solid rgb(255 255 255/.055); background:#0b0f15; }
:deep(.xterm) {
  height: 100%;
}

:deep(.xterm-viewport) {
  scrollbar-color: #3f3f46 #09090b;
}
</style>
