<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { Call, Events } from '@wailsio/runtime'

interface AppSettings {
  settingsVersion: number
  engineBackend: string
  runtimeProvider: string
  dockerEndpoint: string
  wslDistro: string
  closeToTray: boolean
  startAtLogin: boolean
  startHidden: boolean
  autoReconnectEngine: boolean
  startEngineOnLaunch: boolean
}

interface PlatformInfo {
  os: string
  arch: string
  dockerCli?: string
  dockerContext?: string
  dockerHost?: string
}

interface EngineStatus {
  backend: string
  platform: string
  supported: boolean
  helperInstalled: boolean
  engineInstalled: boolean
  running: boolean
  endpoint?: string
  message?: string
  wslDistros?: string[]
}

interface EngineActionResult {
  status: EngineStatus
  output?: string
}

interface RuntimeCapabilities {
  containers: boolean
  lifecycle: boolean
  stats: boolean
  logs: boolean
  terminal: boolean
  images: boolean
  pullImages: boolean
  createContainer: boolean
  volumes: boolean
  networks: boolean
  portPublishing: boolean
  dns: boolean
  compose: boolean
}

interface RuntimeOverview {
  active: {
    provider: string
    displayName: string
    endpoint?: string
    connected: boolean
    experimental: boolean
    capabilities: RuntimeCapabilities
    message?: string
  }
  candidates: Array<{
    provider: string
    displayName: string
    available: boolean
    detected: boolean
    selectable: boolean
    experimental: boolean
    endpoint?: string
    message?: string
  }>
}

interface VersionInfo {
  version: string
  commit: string
  buildDate: string
  channel: string
}

interface UpdateInfo {
  currentVersion: string
  latestVersion: string
  available: boolean
  releaseName: string
  releaseUrl: string
  publishedAt: string
  notes: string
  message: string
  artifactName: string
  artifactSize: number
}

interface UpdateProgress {
  written: number
  total: number
  rate: number
}

interface DockerMigrationPreview {
  available: boolean
  reachable: boolean
  volumeMigrationSupported: boolean
  endpoint?: string
  containers: number
  running: number
  images: number
  volumes: number
  message?: string
}

const props = defineProps<{
  platform: PlatformInfo | null
  migration: DockerMigrationPreview | null
  migrationSourceBusy: boolean
}>()
const emit = defineEmits<{
  engineChanged: []
  reviewMigration: []
  startMigrationSource: []
  scanMigrationSource: []
}>()

const settings = ref<AppSettings>({
  settingsVersion: 2,
  engineBackend: 'external',
  runtimeProvider: 'docker',
  dockerEndpoint: '',
  wslDistro: '',
  closeToTray: true,
  startAtLogin: false,
  startHidden: false,
  autoReconnectEngine: true,
  startEngineOnLaunch: true,
})
const status = ref<EngineStatus | null>(null)
const loading = ref(true)
const busy = ref('')
const dockerIdentity = ref<{
  endpoint: string
  kind: string
  displayName: string
} | null>(null)
const error = ref('')
const output = ref('')
const runtimeOverview = ref<RuntimeOverview | null>(null)
const versionInfo = ref<VersionInfo | null>(null)
const updateInfo = ref<UpdateInfo | null>(null)
const updateBusy = ref(false)
const updateError = ref('')
const updateStage = ref('')
const updateProgress = ref<UpdateProgress | null>(null)
const updateUnsubscribers: Array<() => void> = []

const updateProgressPercent = computed(() => {
  const progress = updateProgress.value
  if (!progress?.total) return 0
  return Math.min(100, Math.round((progress.written / progress.total) * 100))
})

function formatUpdateBytes(value: number) {
  if (!Number.isFinite(value) || value <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1)
  return `${(value / (1024 ** index)).toFixed(index === 0 ? 0 : 1)} ${units[index]}`
}

const backendOptions = computed(() => {
  const native = props.platform?.os === 'darwin'
    ? { value: 'vz', title: 'Dockiva Linux VM', desc: 'Run a minimal Linux guest with Apple Virtualization.framework.' }
    : props.platform?.os === 'windows'
      ? { value: 'wsl2', title: 'Dockiva Native', desc: 'Run the dedicated Dockiva engine with WSL2.' }
      : { value: 'native', title: 'Dockiva Native', desc: 'Use direct containerd in the dedicated dockiva namespace.' }

  return [
    native,
    ...(props.platform?.os === "darwin" ? [{ value: "vmfree", title: "VM-free — Experimental", desc: "Test supported ARM64 images without a VM. Starts automatically when selected." }] : []),
    { value: 'external', title: 'External Docker', desc: 'Use the saved Docker Desktop, System Docker, rootless Docker, or custom endpoint.' },
  ]
})

const nativeBackend = computed(() => {
  if (props.platform?.os === 'darwin') return 'vz'
  if (props.platform?.os === 'windows') return 'wsl2'
  return 'native'
})

async function refresh() {
  loading.value = true
  error.value = ''
  try {
    const [engineStatus, runtimeStatus, identity] = await Promise.all([
      Call.ByName(
        'main.DockerService.GetEngineStatus',
        settings.value.engineBackend,
        settings.value.wslDistro,
      ),
      Call.ByName('main.DockerService.GetRuntimeOverview'),
      Call.ByName('main.DockerService.DockerEndpointIdentity'),
    ])

    status.value = engineStatus as EngineStatus
    runtimeOverview.value = runtimeStatus as RuntimeOverview
    dockerIdentity.value = identity as {
      endpoint: string
      kind: string
      displayName: string
    }
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    loading.value = false
  }
}

async function save() {
  await Call.ByName('main.AppService.UpdateSettings', settings.value)
}

async function chooseBackend(value: string) {
  if (busy.value) return
  busy.value = `backend:${value}`
  error.value = ''
  try {
    const provider = await Call.ByName(
      'main.DockerService.SelectEngineBackend',
      value,
      settings.value.wslDistro,
      settings.value.dockerEndpoint,
    ) as string
    settings.value.engineBackend = value
    settings.value.runtimeProvider = provider
    if (value === 'external') {
      const identity = await Call.ByName(
        'main.DockerService.DockerEndpointIdentity',
      ) as { endpoint: string }
      settings.value.dockerEndpoint = identity.endpoint || settings.value.dockerEndpoint
    }
    await save()
    await refresh()
    emit('engineChanged')
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    busy.value = ''
  }
}

async function chooseDistro(value: string) {
  settings.value.wslDistro = value
  await save()
  await refresh()
}

async function runAction(action: 'start' | 'stop' | 'delete' | 'provision') {
  if (busy.value) return

  if (action === 'delete' && !window.confirm('Delete the Dockiva native VM and its local container data?')) return
  if (action === 'provision' && !window.confirm(`Install Docker Engine and socat inside WSL distro "${settings.value.wslDistro}"?`)) return

  busy.value = action
  error.value = ''
  output.value = ''
  try {
    let result: EngineActionResult
    if (action === 'start') {
      result = await Call.ByName('main.DockerService.StartManagedEngine', settings.value.engineBackend, settings.value.wslDistro) as EngineActionResult
    } else if (action === 'stop') {
      result = await Call.ByName('main.DockerService.StopManagedEngine', settings.value.engineBackend, settings.value.wslDistro) as EngineActionResult
    } else if (action === 'delete') {
      result = await Call.ByName('main.DockerService.DeleteManagedEngine', settings.value.engineBackend, settings.value.wslDistro) as EngineActionResult
    } else {
      result = await Call.ByName('main.DockerService.ProvisionWSLEngine', settings.value.wslDistro) as EngineActionResult
    }
    status.value = result.status
    output.value = result.output || ''
    emit('engineChanged')
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
    await refresh()
  } finally {
    busy.value = ''
  }
}

async function reconnectDocker() {
  if (busy.value) return

  busy.value = 'reconnect'
  error.value = ''
  output.value = ''

  try {
    const result = await Call.ByName(
      'main.DockerService.RecoverDockerConnection',
    ) as EngineActionResult

    status.value = result.status
    output.value = result.output || ''
    const identity = await Call.ByName(
      'main.DockerService.DockerEndpointIdentity',
    ) as { endpoint: string }
    if (identity.endpoint && identity.endpoint !== settings.value.dockerEndpoint) {
      settings.value.dockerEndpoint = identity.endpoint
      await save()
    }
    await refresh()
    emit('engineChanged')
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
    await refresh()
  } finally {
    busy.value = ''
  }
}

async function selectRuntime(provider: string) {
  await chooseBackend(provider === 'vmfree' ? 'vmfree' : provider === 'docker' ? 'external' : nativeBackend.value)
}

async function toggle(
  key:
    | 'closeToTray'
    | 'startAtLogin'
    | 'startHidden'
    | 'autoReconnectEngine'
    | 'startEngineOnLaunch',
  value: boolean,
) {
  settings.value[key] = value
  try { await save() } catch (err) { error.value = err instanceof Error ? err.message : String(err) }
}

async function checkForUpdates() {
  if (updateBusy.value) return
  updateBusy.value = true
  updateError.value = ''
  try {
    updateInfo.value = await Call.ByName('main.AppService.CheckForUpdates') as UpdateInfo
  } catch (err) {
    updateError.value = err instanceof Error ? err.message : String(err)
  } finally {
    updateBusy.value = false
  }
}

async function applyUpdate() {
  if (updateBusy.value || !updateInfo.value?.available) return
  updateBusy.value = true
  updateError.value = ''
  updateStage.value = 'Preparing download…'
  updateProgress.value = null
  try {
    await Call.ByName('main.AppService.ApplyUpdate')
  } catch (err) {
    updateError.value = err instanceof Error ? err.message : String(err)
    updateStage.value = ''
    updateBusy.value = false
  }
}

function setupUpdateListeners() {
  const listen = (name: string, handler: (data: any) => void) => {
    updateUnsubscribers.push(Events.On(name, (payload: any) => handler(payload?.data ?? payload)))
  }
  listen('wails:updater:download-started', () => { updateStage.value = 'Downloading update…' })
  listen('wails:updater:download-progress', (data: UpdateProgress) => {
    updateStage.value = 'Downloading update…'
    updateProgress.value = data
  })
  listen('wails:updater:verifying', () => { updateStage.value = 'Verifying download…' })
  listen('wails:updater:installing', () => { updateStage.value = 'Preparing installation…' })
  listen('wails:updater:update-ready', () => { updateStage.value = 'Restarting to install…' })
  listen('wails:updater:error', (data: { message?: string }) => {
    updateError.value = data?.message || 'The update could not be installed.'
    updateStage.value = ''
    updateBusy.value = false
  })
}

async function openUpdatePage() {
  if (!updateInfo.value?.releaseUrl) return
  updateError.value = ''
  try {
    await Call.ByName('main.AppService.OpenUpdatePage', updateInfo.value.releaseUrl)
  } catch (err) {
    updateError.value = err instanceof Error ? err.message : String(err)
  }
}

onMounted(async () => {
  setupUpdateListeners()
  try {
    const [loadedSettings, loadedVersion] = await Promise.all([
      Call.ByName('main.AppService.GetSettings'),
      Call.ByName('main.AppService.GetVersionInfo'),
    ])
    settings.value = loadedSettings as AppSettings
    versionInfo.value = loadedVersion as VersionInfo
    await refresh()
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
    loading.value = false
  }
})

onBeforeUnmount(() => {
  updateUnsubscribers.splice(0).forEach(unsubscribe => unsubscribe())
})
</script>

<template>
  <div class="engine-settings space-y-5">
    <section class="panel p-6">
      <div class="flex items-start justify-between gap-4">
        <div>
          <h3 class="font-semibold">Container Engine</h3>
          <p class="mt-1 text-sm text-zinc-500">Connect to an existing Docker endpoint or let Dockiva manage a lightweight backend.</p>
        </div>
        <button class="toolbar-button" :disabled="loading || !!busy" @click="refresh">{{ loading ? 'Checking…' : 'Refresh Status' }}</button>
      </div>

      <div class="mt-5 grid gap-3 lg:grid-cols-2">
        <button
          v-for="item in backendOptions"
          :key="item.value"
          :disabled="!!busy"
          class="backend-card rounded-xl border p-4 text-left transition"
          :class="settings.engineBackend === item.value ? 'border-sky-700 bg-sky-950/20' : 'border-zinc-800 bg-zinc-900/40 hover:border-zinc-700'"
          @click="chooseBackend(item.value)"
        >
          <div class="font-medium">{{ item.title }}</div>
          <div class="mt-1 text-xs leading-5 text-zinc-500">{{ item.desc }}</div>
        </button>
      </div>

      <div v-if="settings.engineBackend === 'external'" class="config-box mt-4 rounded-lg border border-zinc-800 bg-zinc-900/50 p-4">
        <label class="text-xs text-zinc-400">Saved Docker endpoint</label>
        <div class="mt-2 flex gap-2">
          <input
            v-model="settings.dockerEndpoint"
            class="field min-w-0 flex-1 font-mono"
            placeholder="unix:///var/run/docker.sock or tcp://host:2375"
          />
          <button class="toolbar-button" :disabled="!settings.dockerEndpoint.trim() || !!busy" @click="chooseBackend('external')">
            Apply endpoint
          </button>
        </div>
        <p class="mt-2 text-xs text-zinc-600">Dockiva keeps this engine separate from Native. If a saved local socket no longer exists, it uses the active Docker CLI context.</p>
      </div>

      <div v-if="settings.engineBackend === 'wsl2' && status" class="config-box mt-5 rounded-lg border border-zinc-800 bg-zinc-900/50 p-4">
        <label class="text-xs text-zinc-400">WSL distribution</label>
        <div class="mt-2 flex flex-wrap gap-2">
          <select class="field min-w-64" :value="settings.wslDistro" @change="chooseDistro(($event.target as HTMLSelectElement).value)">
            <option value="">Choose distribution…</option>
            <option v-for="distro in status.wslDistros || []" :key="distro" :value="distro">{{ distro }}</option>
          </select>
          <button class="toolbar-button" :disabled="!settings.wslDistro || !!busy" @click="runAction('provision')">{{ busy === 'provision' ? 'Provisioning…' : 'Provision Docker' }}</button>
        </div>
        <p class="mt-2 text-xs text-zinc-600">A dedicated Ubuntu/Debian WSL distro is recommended. Dockiva never unregisters it.</p>
      </div>

      <div v-if="status" class="engine-status mt-5 rounded-xl border border-zinc-800 bg-black/20 p-5">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div class="flex items-center gap-3">
            <span class="h-3 w-3 rounded-full" :class="status.running ? 'bg-emerald-500' : 'bg-zinc-600'" />
            <div>
              <div class="font-medium">{{ status.running ? 'Engine running' : settings.engineBackend === 'vmfree' ? 'VM-free engine offline' : settings.engineBackend === 'external' ? 'External Docker offline' : 'Dockiva engine stopped' }}</div>
              <div class="mt-0.5 text-xs text-zinc-500">{{ status.message }}</div>
              <div
                v-if="settings.engineBackend === 'external' && dockerIdentity?.endpoint"
                class="mt-1 text-[11px] text-zinc-600"
              >
                {{ dockerIdentity.displayName }} ·
                <span class="font-mono">{{ dockerIdentity.endpoint }}</span>
              </div>
              <div
                v-if="settings.engineBackend === 'external' && dockerIdentity?.kind === 'docker-desktop'"
                class="mt-1 text-[11px] text-amber-400/90"
              >
                Docker Desktop and System Docker use separate container stores.
              </div>
            </div>
          </div>
          <div class="flex gap-2">
            <button
              v-if="!status.running && settings.engineBackend === 'external'"
              class="toolbar-button"
              :disabled="!!busy"
              @click="reconnectDocker"
            >
              {{ busy === 'reconnect' ? 'Connecting…' : dockerIdentity?.kind === 'docker-desktop' ? 'Start Docker Desktop' : 'Reconnect' }}
            </button>
            <button
              v-if="!status.running && settings.engineBackend !== 'external'"
              class="primary-button"
              :disabled="!!busy"
              @click="runAction('start')"
            >
              {{ busy === 'start' ? 'Starting engine…' : 'Start Dockiva Engine' }}
            </button>
            <button v-if="status.running && ['vz','wsl2'].includes(settings.engineBackend)" class="toolbar-button" :disabled="!!busy" @click="runAction('stop')">{{ busy === 'stop' ? 'Stopping…' : 'Stop Managed Engine' }}</button>
            <button v-if="settings.engineBackend === 'vz' && status.engineInstalled" class="danger-button" :disabled="!!busy" @click="runAction('delete')">Delete VM</button>
          </div>
        </div>

        <div class="mt-4 grid gap-3 text-xs md:grid-cols-4">
          <div class="status-cell"><span>Backend</span><strong>{{ settings.engineBackend }}</strong></div>
          <div class="status-cell"><span>Helper</span><strong>{{ status.helperInstalled ? 'available' : 'missing' }}</strong></div>
          <div class="status-cell"><span>Engine</span><strong>{{ status.engineInstalled ? 'installed' : 'not installed' }}</strong></div>
          <div class="status-cell"><span>Endpoint</span><strong class="truncate font-mono" :title="status.endpoint">{{ status.endpoint || '—' }}</strong></div>
        </div>

        <pre v-if="output" class="mt-4 max-h-56 overflow-auto whitespace-pre-wrap rounded-lg bg-black p-3 font-mono text-xs text-zinc-400">{{ output }}</pre>
      </div>

      <div v-if="settings.engineBackend === 'vz' && status && !status.helperInstalled" class="mt-4 rounded-lg border border-amber-900/60 bg-amber-950/20 p-4 text-sm text-amber-300">
        Build/install the native VMM helper and guest assets. See <code class="ml-2 rounded bg-black/30 px-2 py-1 font-mono text-xs">docs/MILESTONES.md (Milestone 13)</code>
      </div>

      <div v-if="error" class="mt-4 whitespace-pre-wrap rounded-lg border border-red-900 bg-red-950/30 p-4 text-sm text-red-300">{{ error }}</div>
    </section>

    <section v-if="!['external', 'vmfree'].includes(settings.engineBackend)" class="panel migration-panel p-6">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <div class="flex items-center gap-2">
            <h3 class="font-semibold">Migrate from Docker</h3>
            <span
              class="migration-source-badge"
              :class="props.migration?.reachable ? 'online' : 'offline'"
            >
              {{ props.migration?.reachable ? 'Docker connected' : 'Docker offline' }}
            </span>
          </div>
          <p class="mt-1 text-sm text-zinc-500">
            Copy containers and images into Dockiva Native without deleting anything from Docker.
          </p>
          <p v-if="props.migration?.endpoint" class="migration-endpoint mt-2" :title="props.migration.endpoint">
            {{ props.migration.endpoint }}
          </p>
        </div>

        <button
          v-if="!props.migration?.reachable"
          class="primary-button"
          :disabled="props.migrationSourceBusy"
          @click="emit('startMigrationSource')"
        >
          {{ props.migrationSourceBusy ? 'Starting Docker…' : 'Start Docker & Scan' }}
        </button>
        <button
          v-else-if="props.migration.available"
          class="primary-button"
          :disabled="props.migrationSourceBusy"
          @click="emit('reviewMigration')"
        >
          Review Migration
        </button>
        <button
          v-else
          class="toolbar-button"
          :disabled="props.migrationSourceBusy"
          @click="emit('scanMigrationSource')"
        >
          {{ props.migrationSourceBusy ? 'Scanning…' : 'Scan Again' }}
        </button>
      </div>

      <div v-if="props.migration?.reachable" class="migration-resource-grid mt-5">
        <div><strong>{{ props.migration.containers }}</strong><span>Containers</span></div>
        <div><strong>{{ props.migration.images }}</strong><span>Images</span></div>
        <div><strong>{{ props.migration.volumes }}</strong><span>Volumes</span></div>
        <div><strong>{{ props.migration.running }}</strong><span>Running</span></div>
      </div>

      <p class="migration-message mt-4">{{ props.migration?.message || 'Scanning the saved external Docker endpoint…' }}</p>
      <p v-if="props.migration?.volumes && !props.migration.volumeMigrationSupported" class="migration-note mt-2">
        Image and stopped-container migration are available here. Named-volume transfer currently requires Linux.
      </p>
    </section>

    <section v-if="runtimeOverview" class="panel p-6">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h3 class="font-semibold">Container Runtime</h3>
          <p class="mt-1 text-sm text-zinc-500">
            Container lifecycle is now behind a runtime-neutral Go interface.
          </p>
        </div>

        <span
          class="rounded-full px-2 py-1 text-xs"
          :class="
            runtimeOverview.active.connected
              ? 'bg-emerald-500/10 text-emerald-400'
              : 'bg-red-500/10 text-red-400'
          "
        >
          {{ runtimeOverview.active.connected ? 'connected' : 'offline' }}
        </span>
      </div>

      <div class="runtime-card mt-4 rounded-lg border border-zinc-800 bg-black/20 p-4">
        <div class="font-medium">{{ runtimeOverview.active.displayName }}</div>
        <div class="mt-1 text-xs leading-5 text-zinc-500">
          {{ runtimeOverview.active.message }}
        </div>
        <div
          v-if="runtimeOverview.active.endpoint"
          class="mt-2 truncate font-mono text-[10px] text-zinc-600"
          :title="runtimeOverview.active.endpoint"
        >
          {{ runtimeOverview.active.endpoint }}
        </div>

        <div class="mt-3 flex flex-wrap gap-1">
          <span
            v-for="(enabled, capability) in runtimeOverview.active.capabilities"
            :key="capability"
            v-show="enabled"
            class="rounded-full border border-zinc-700 px-2 py-1 text-[10px] text-zinc-400"
          >
            {{ capability }}
          </span>
        </div>
      </div>

      <div class="mt-4 space-y-2">
        <div
          v-for="candidate in runtimeOverview.candidates"
          :key="candidate.provider"
          class="runtime-option flex items-start justify-between gap-3 rounded-lg border border-zinc-800 p-3"
        >
          <div class="min-w-0">
            <div class="flex items-center gap-2">
              <span class="font-medium">{{ candidate.displayName }}</span>
              <span
                v-if="candidate.experimental"
                class="rounded bg-amber-500/10 px-1.5 py-0.5 text-[10px] text-amber-400"
              >
                experimental
              </span>
            </div>
            <div class="mt-1 text-xs leading-5 text-zinc-500">
              {{ candidate.message }}
            </div>
            <div
              v-if="candidate.endpoint"
              class="mt-1 truncate font-mono text-[10px] text-zinc-700"
              :title="candidate.endpoint"
            >
              {{ candidate.endpoint }}
            </div>
          </div>

          <div class="flex shrink-0 items-center gap-2">
            <span
              v-if="runtimeOverview.active.provider === candidate.provider"
              class="rounded-full bg-emerald-500/10 px-2 py-1 text-[10px] text-emerald-400"
            >
              active
            </span>

            <button
              v-else-if="candidate.selectable"
              class="toolbar-button"
              :disabled="!!busy"
              @click="selectRuntime(candidate.provider)"
            >
              {{
                busy === `backend:${candidate.provider === 'vmfree' ? 'vmfree' : candidate.provider === 'docker' ? 'external' : nativeBackend}`
                  ? 'Switching…'
                  : 'Use Runtime'
              }}
            </button>

            <span
              v-else
              class="rounded-full px-2 py-1 text-[10px]"
              :class="
                candidate.detected
                  ? 'bg-amber-500/10 text-amber-400'
                  : 'bg-zinc-800 text-zinc-500'
              "
            >
              {{ candidate.detected ? 'detected' : 'not detected' }}
            </span>
          </div>
        </div>
      </div>

      <p class="mt-4 text-xs leading-5 text-zinc-600">
        On Linux, direct containerd uses the dedicated dockiva namespace. CNI bridge networking, host DNS, and localhost TCP port publishing become available after the one-time networking helper is installed.
      </p>
    </section>

    <section class="panel p-6">
      <h3 class="font-semibold">Desktop Behavior</h3>
      <div class="settings-list mt-4 divide-y divide-zinc-800 rounded-lg border border-zinc-800">
        <label class="setting-row">
          <div>
            <div class="font-medium">Auto reconnect engine</div>
            <div class="text-xs text-zinc-500">
              External Docker keeps its saved endpoint unless that local socket was removed; Native never falls through to another daemon.
            </div>
          </div>
          <input
            type="checkbox"
            :checked="settings.autoReconnectEngine"
            @change="toggle('autoReconnectEngine', ($event.target as HTMLInputElement).checked)"
          />
        </label>
        <label class="setting-row">
          <div>
            <div class="font-medium">Start engine when Dockiva starts</div>
            <div class="text-xs text-zinc-500">
              Start or reconnect only the selected Dockiva-managed backend. External Docker is never launched implicitly.
            </div>
          </div>
          <input
            type="checkbox"
            :checked="settings.startEngineOnLaunch"
            @change="toggle('startEngineOnLaunch', ($event.target as HTMLInputElement).checked)"
          />
        </label>
        <label class="setting-row"><div><div class="font-medium">Close to tray</div><div class="text-xs text-zinc-500">Hide instead of quitting when the main window closes.</div></div><input type="checkbox" :checked="settings.closeToTray" @change="toggle('closeToTray', ($event.target as HTMLInputElement).checked)" /></label>
        <label class="setting-row"><div><div class="font-medium">Start at login</div><div class="text-xs text-zinc-500">Use native Wails autostart integration.</div></div><input type="checkbox" :checked="settings.startAtLogin" @change="toggle('startAtLogin', ($event.target as HTMLInputElement).checked)" /></label>
        <label class="setting-row"><div><div class="font-medium">Start hidden</div><div class="text-xs text-zinc-500">Launch directly into the tray.</div></div><input type="checkbox" :checked="settings.startHidden" @change="toggle('startHidden', ($event.target as HTMLInputElement).checked)" /></label>
      </div>
    </section>

    <section class="panel update-panel p-6">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <div class="flex items-center gap-2">
            <h3 class="font-semibold">Dockiva Updates</h3>
            <span v-if="versionInfo" class="version-badge">v{{ versionInfo.version }}</span>
          </div>
          <p class="mt-1 text-sm text-zinc-500">Check official releases published by Tomnerb/Dockiva.</p>
        </div>
        <button class="toolbar-button" :disabled="updateBusy" @click="checkForUpdates">
          {{ updateBusy ? 'Checking…' : 'Check for Updates' }}
        </button>
      </div>

      <div v-if="versionInfo" class="version-details mt-4">
        <div><span>Installed version</span><strong>v{{ versionInfo.version }}</strong></div>
        <div><span>Channel</span><strong>{{ versionInfo.channel }}</strong></div>
        <div><span>Build</span><strong class="font-mono">{{ versionInfo.commit || 'development' }}</strong></div>
        <div><span>Built</span><strong>{{ versionInfo.buildDate || 'local build' }}</strong></div>
      </div>

      <div
        v-if="updateInfo"
        class="update-result mt-4"
        :class="updateInfo.available ? 'available' : 'current'"
      >
        <div class="min-w-0">
          <strong>{{ updateInfo.message }}</strong>
          <p v-if="updateInfo.available && updateInfo.releaseName" class="mt-1">{{ updateInfo.releaseName }}</p>
          <p v-if="updateInfo.available" class="mt-1">
            {{ updateInfo.artifactName }} · {{ formatUpdateBytes(updateInfo.artifactSize) }}
          </p>
          <div v-if="updateBusy" class="update-progress mt-3">
            <div class="update-progress-track"><span :style="{ width: `${updateProgressPercent}%` }" /></div>
            <div class="mt-1 flex justify-between gap-3">
              <span>{{ updateStage }}</span>
              <span v-if="updateProgress?.total">
                {{ updateProgressPercent }}% · {{ formatUpdateBytes(updateProgress.written) }} / {{ formatUpdateBytes(updateProgress.total) }}
              </span>
            </div>
          </div>
        </div>
        <button
          v-if="updateInfo.available && updateInfo.releaseUrl"
          class="primary-button shrink-0"
          :disabled="updateBusy"
          @click="applyUpdate"
        >
          {{ updateBusy ? 'Updating…' : `Update & Restart to v${updateInfo.latestVersion}` }}
        </button>
      </div>

      <button
        v-if="updateInfo?.available && updateInfo.releaseUrl"
        class="mt-3 text-xs text-zinc-500 underline decoration-zinc-700 underline-offset-4 hover:text-cyan-400"
        :disabled="updateBusy"
        @click="openUpdatePage"
      >
        Open release page instead
      </button>

      <div v-if="updateError" class="mt-4 whitespace-pre-wrap rounded-lg border border-red-900 bg-red-950/30 p-4 text-sm text-red-300">
        {{ updateError }}
      </div>
    </section>
  </div>
</template>

<style scoped>
.engine-settings {
  --settings-border:rgb(255 255 255/.075);
  --settings-border-strong:rgb(255 255 255/.12);
  --settings-surface:rgb(255 255 255/.026);
  --settings-surface-raised:rgb(255 255 255/.045);
  --settings-input:rgb(6 7 12/.48);
  --settings-text:#ededf2;
  --settings-muted:#a0a0ab;
  --settings-faint:#71717e;
}
.panel { border:1px solid var(--settings-border); border-radius:1.1rem; background:linear-gradient(145deg,rgb(27 28 40/.9),rgb(19 20 29/.86)); box-shadow:inset 0 1px rgb(255 255 255/.035),0 14px 38px rgb(0 0 0/.12); }
.panel h3 { color:var(--settings-text); font-size:1rem; letter-spacing:-.012em; }
.toolbar-button,.primary-button,.danger-button { border-radius:.65rem; padding:.52rem .82rem; font-size:.75rem; transition:160ms ease; }
.toolbar-button { border:1px solid var(--settings-border-strong); background:var(--settings-surface-raised); color:var(--settings-text); }
.toolbar-button:hover { border-color:rgb(0 229 255/.3); background:rgb(10 104 255/.085); transform:translateY(-1px); }
.primary-button { border:1px solid rgb(0 229 255/.42); background:linear-gradient(135deg,#0a68ff,#00e5ff); color:#f7f9fc; font-weight:600; box-shadow:0 7px 18px rgb(6 62 155/.26),inset 0 1px rgb(255 255 255/.18); }
.danger-button { border:1px solid rgb(127 29 29); background:rgb(69 10 10/.35); color:rgb(248 113 113); }
.toolbar-button:disabled,.primary-button:disabled,.danger-button:disabled { opacity:.4; cursor:not-allowed; }
.field { border:1px solid var(--settings-border-strong); border-radius:.65rem; background:var(--settings-input); padding:.55rem .75rem; font-size:.78rem; color:var(--settings-text); outline:none; }
.field:focus { border-color:rgb(0 229 255/.75); box-shadow:0 0 0 3px rgb(10 104 255/.14); }
.backend-card { position:relative; overflow:hidden; border-color:var(--settings-border)!important; background:var(--settings-surface)!important; color:var(--settings-text); box-shadow:inset 0 1px rgb(255 255 255/.025); }
.backend-card:hover { border-color:rgb(0 229 255/.28)!important; background:var(--settings-surface-raised)!important; transform:translateY(-1px); }
.backend-card.border-sky-700 { border-color:rgb(0 229 255/.5)!important; background:linear-gradient(135deg,rgb(10 104 255/.18),rgb(0 229 255/.05))!important; box-shadow:inset 0 1px rgb(255 255 255/.05),0 9px 26px rgb(6 62 155/.16); }
.backend-card.border-sky-700::after { content:'✓'; position:absolute; top:.85rem; right:.9rem; display:grid; width:1.4rem; height:1.4rem; place-items:center; border-radius:999px; background:#0a68ff; color:#f7f9fc; font-size:.7rem; }
.config-box,.runtime-card,.runtime-option,.settings-list { border-color:var(--settings-border)!important; }
.config-box,.runtime-card { background:var(--settings-surface)!important; }
.engine-status { border-color:var(--settings-border)!important; background:linear-gradient(135deg,rgb(9 10 17/.5),rgb(44 35 82/.16))!important; }
.status-cell { min-width:0; border:1px solid var(--settings-border); border-radius:.7rem; background:var(--settings-surface); padding:.8rem; display:flex; flex-direction:column; gap:.25rem; }
.status-cell span { color:var(--settings-faint); }
.status-cell strong { color:var(--settings-muted); }
.runtime-option { background:transparent; transition:160ms ease; }
.runtime-option:hover { border-color:rgb(0 229 255/.2)!important; background:var(--settings-surface); }
.migration-panel { background:linear-gradient(135deg,rgb(10 104 255/.1),rgb(0 229 255/.025) 52%,rgb(19 20 29/.88))!important; }
.migration-source-badge { border:1px solid var(--settings-border); border-radius:999px; padding:.2rem .5rem; font-size:.62rem; font-weight:600; }
.migration-source-badge.online { border-color:rgb(52 211 153/.24); background:rgb(16 185 129/.09); color:#6ee7b7; }
.migration-source-badge.offline { border-color:rgb(251 191 36/.2); background:rgb(245 158 11/.08); color:#fcd34d; }
.migration-endpoint { max-width:42rem; overflow:hidden; color:var(--settings-faint); font-family:ui-monospace,SFMono-Regular,Menlo,monospace; font-size:.68rem; text-overflow:ellipsis; white-space:nowrap; }
.migration-resource-grid { display:grid; grid-template-columns:repeat(4,minmax(0,1fr)); gap:.65rem; }
.migration-resource-grid div { border:1px solid var(--settings-border); border-radius:.75rem; background:var(--settings-surface); padding:.85rem; }
.migration-resource-grid strong,.migration-resource-grid span { display:block; }
.migration-resource-grid strong { color:var(--settings-text); font-size:1.1rem; }
.migration-resource-grid span { margin-top:.2rem; color:var(--settings-faint); font-size:.68rem; }
.migration-message { color:var(--settings-muted); font-size:.76rem; line-height:1.5; }
.migration-note { color:#fcd34d; font-size:.7rem; line-height:1.5; }
@media (max-width:700px) { .migration-resource-grid { grid-template-columns:repeat(2,minmax(0,1fr)); } }
.settings-list { overflow:hidden; background:var(--settings-surface); }
.setting-row { display:flex; cursor:pointer; align-items:center; justify-content:space-between; gap:1rem; padding:1rem 1.1rem; font-size:.85rem; transition:background 150ms ease; }
.setting-row + .setting-row { border-color:var(--settings-border)!important; }
.setting-row:hover { background:var(--settings-surface-raised); }
.setting-row input[type="checkbox"] { position:relative; width:2.15rem; height:1.25rem; flex:none; appearance:none; border:1px solid var(--settings-border-strong); border-radius:999px; background:rgb(255 255 255/.08); transition:160ms ease; }
.setting-row input[type="checkbox"]::after { content:''; position:absolute; top:2px; left:2px; width:.9rem; height:.9rem; border-radius:999px; background:#9a9aa5; box-shadow:0 2px 5px rgb(0 0 0/.3); transition:160ms ease; }
.setting-row input[type="checkbox"]:checked { border-color:rgb(0 229 255/.55); background:linear-gradient(135deg,#0a68ff,#00e5ff); }
.setting-row input[type="checkbox"]:checked::after { left:calc(100% - 1.02rem); background:white; }
.version-badge { border:1px solid rgb(0 229 255/.24); border-radius:999px; background:rgb(10 104 255/.1); padding:.2rem .48rem; color:#68eaff; font-size:.62rem; font-weight:650; }
.version-details { display:grid; grid-template-columns:repeat(4,minmax(0,1fr)); gap:.55rem; }
.version-details > div { display:flex; min-width:0; flex-direction:column; gap:.25rem; border:1px solid var(--settings-border); border-radius:.68rem; background:var(--settings-surface); padding:.72rem .8rem; }
.version-details span { color:var(--settings-faint); font-size:.62rem; }
.version-details strong { overflow:hidden; color:var(--settings-muted); font-size:.7rem; text-overflow:ellipsis; white-space:nowrap; }
.update-result { display:flex; align-items:center; justify-content:space-between; gap:1rem; border:1px solid var(--settings-border); border-radius:.75rem; padding:.85rem 1rem; color:var(--settings-muted); }
.update-result.available { border-color:rgb(0 229 255/.24); background:linear-gradient(135deg,rgb(10 104 255/.11),rgb(0 229 255/.04)); }
.update-result.current { border-color:rgb(49 201 145/.18); background:rgb(49 201 145/.045); }
.update-result strong { color:var(--settings-text); font-size:.78rem; }
.update-result p { color:var(--settings-faint); font-size:.65rem; }
.update-progress { min-width:18rem; color:var(--settings-faint); font-size:.62rem; }
.update-progress-track { height:.32rem; overflow:hidden; border-radius:999px; background:rgb(255 255 255/.08); }
.update-progress-track span { display:block; height:100%; border-radius:inherit; background:linear-gradient(90deg,#0a68ff,#00e5ff); transition:width 120ms linear; }

@media (max-width: 760px) {
  .version-details { grid-template-columns:repeat(2,minmax(0,1fr)); }
  .update-result { align-items:stretch; flex-direction:column; }
}

:global(html[data-theme="light"] .engine-settings) {
  --settings-border:rgb(38 39 52/.09);
  --settings-border-strong:rgb(38 39 52/.14);
  --settings-surface:rgb(52 49 68/.034);
  --settings-surface-raised:rgb(255 255 255/.76);
  --settings-input:rgb(255 255 255/.82);
  --settings-text:#292a34;
  --settings-muted:#555661;
  --settings-faint:#777884;
}
:global(html[data-theme="light"] .engine-settings .panel) { background:linear-gradient(145deg,rgb(255 255 255/.97),rgb(246 247 251/.91)); box-shadow:inset 0 1px white,0 14px 34px rgb(64 61 90/.08); }
:global(html[data-theme="light"] .engine-settings .backend-card) { box-shadow:inset 0 1px rgb(255 255 255/.75); }
:global(html[data-theme="light"] .engine-settings .backend-card.border-sky-700) { border-color:rgb(10 104 255/.34)!important; background:linear-gradient(135deg,rgb(10 104 255/.13),rgb(0 229 255/.035))!important; box-shadow:inset 0 1px white,0 9px 26px rgb(6 62 155/.09); }
:global(html[data-theme="light"] .engine-settings .engine-status) { background:linear-gradient(135deg,rgb(246 246 250/.9),rgb(10 104 255/.07))!important; }
:global(html[data-theme="light"] .engine-settings .setting-row input[type="checkbox"]) { background:rgb(39 40 52/.1); }
:global(html[data-theme="light"] .engine-settings .setting-row input[type="checkbox"]:checked) { border-color:rgb(10 104 255/.45); background:linear-gradient(135deg,#0a68ff,#00e5ff); }
:global(html[data-theme="light"] .engine-settings .danger-button) { border-color:rgb(220 92 92/.3); background:rgb(239 68 68/.07); color:#b83232; }
</style>
