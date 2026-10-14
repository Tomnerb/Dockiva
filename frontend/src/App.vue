<script setup lang="ts">
import {
  computed,
  nextTick,
  onBeforeUnmount,
  onMounted,
  ref,
  watch,
} from 'vue'
import { Call, Events } from '@wailsio/runtime'
import XtermTerminal from './components/XtermTerminal.vue'
import ContainerDetailsModal from './components/ContainerDetailsModal.vue'
import EngineSettingsPanel from './components/EngineSettingsPanel.vue'
import brandSymbol from '../../assets/dockiva_icon.png'
import brandAppIcon from '../../assets/dockiva_icon2.png'

type TabName = 'overview' | 'containers' | 'images' | 'volumes' | 'networks' | 'storage' | 'engine'
type ContainerAction = 'start' | 'stop' | 'restart' | 'delete'
type ProjectAction = 'start' | 'stop' | 'restart'
type ComposeAction = 'up' | 'down' | 'build' | 'rebuild'
type ColorTheme = 'light' | 'dark'

interface DockerStatus {
  connected: boolean
  apiVersion: string
  osType: string
  error?: string
}

interface PortInfo {
  privatePort: number
  publicPort: number
  type: string
  display: string
  url?: string
}

interface Container {
  id: string
  shortId: string
  name: string
  image: string
  state: string
  status: string
  composeProject?: string
  composeService?: string
  ipAddress?: string
  ports: PortInfo[]
}

interface ResourceStats {
  id: string
  cpuPercent: number
  memoryUsage: number
  memoryLimit: number
  memoryPercent: number
  pids: number
  error?: string
}

interface ImageInfo {
  id: string
  shortId: string
  tags: string[]
  size: number
  created: number
  containers: number
}

interface VolumeInfo {
  name: string
  driver: string
  scope: string
  mountpoint: string
  createdAt?: string
  labels: Record<string, string>
}

interface NetworkInfo {
  id: string
  shortId: string
  name: string
  driver: string
  scope: string
  internal: boolean
  attachable: boolean
  ingress: boolean
}

interface ContainerGroup {
  key: string
  name: string
  compose: boolean
  containers: Container[]
  running: number
}

interface StreamOutputEvent {
  streamId: string
  containerId: string
  data?: string
  error?: string
  closed?: boolean
  seq: number
}

interface DiskUsageItem {
  type: string
  totalCount: number
  active: number
  size: string
  reclaimable: string
}

interface HostContainerStorageItem {
  id: string
  name: string
  description: string
  path: string
  sizeBytes: number
  size: string
  canReveal: boolean
  canClean: boolean
  engineRunning: boolean
  cleanupLabel: string
}

type HostCleanupMode = 'cache' | 'safe' | 'deep'

interface CLIResult {
  output: string
}

interface PlatformInfo {
  os: string
  arch: string
  dockerCli?: string
  dockerContext?: string
  dockerHost?: string
  hostSource?: string
}

interface ComposeImportResult {
  project: string
  workingDir: string
  configFiles: string[]
  output: string
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
}

interface RuntimeOperationResult {
  message: string
  detail?: string
}

interface RuntimePortMapping {
  hostPort: number
  containerPort: number
  protocol: string
  hostIp: string
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

interface DockerMigrationStatus {
  kind?: string
  state: 'idle' | 'running' | 'complete' | 'failed' | string
  message: string
  importedImages?: number
  totalImages?: number
  currentImage?: string
  error?: string
}

const activeTab = ref<TabName>('overview')
const systemTheme = window.matchMedia('(prefers-color-scheme: dark)')
const savedTheme = window.localStorage.getItem('dockiva-color-theme')
const colorTheme = ref<ColorTheme>(
  savedTheme === 'light' || savedTheme === 'dark'
    ? savedTheme
    : systemTheme.matches ? 'dark' : 'light',
)
const savedSidebarMode = window.localStorage.getItem('dockiva-sidebar-mode')
const sidebarCompact = ref(savedSidebarMode !== 'expanded')
const dockerStatus = ref<DockerStatus | null>(null)
const platformInfo = ref<PlatformInfo | null>(null)
const runtimeOverview = ref<RuntimeOverview | null>(null)
const searchQuery = ref('')
const importBusy = ref(false)
const smartCheckBusy = ref(false)

const containers = ref<Container[]>([])
const images = ref<ImageInfo[]>([])
const volumes = ref<VolumeInfo[]>([])
const networks = ref<NetworkInfo[]>([])
const diskUsage = ref<DiskUsageItem[]>([])
const hostContainerStorage = ref<HostContainerStorageItem[]>([])
const stats = ref<Record<string, ResourceStats>>({})

const loading = ref(false)
const statsLoading = ref(false)
const error = ref('')
const success = ref('')
const commandOutput = ref('')

const busyId = ref('')
const busyAction = ref<ContainerAction | ''>('')
const busyProject = ref('')
const busyProjectAction = ref<ProjectAction | ''>('')
const composeBusy = ref('')
const resourceBusy = ref('')
const storageCleanupTarget = ref<HostContainerStorageItem | null>(null)
const storageCleanupMode = ref<HostCleanupMode | null>(null)
const storageCleanupError = ref('')

const collapsedGroups = ref<Record<string, boolean>>({})

const logsOpen = ref(false)
const logsContainer = ref<Container | null>(null)
const logsText = ref('')
const logsStreamID = ref('')
const logsFollowing = ref(false)
const logTail = ref(200)
const logsViewport = ref<HTMLElement | null>(null)

const terminalContainer = ref<Container | null>(null)
const detailsContainer = ref<Container | null>(null)

const pullImageRef = ref('')
const newVolumeName = ref('')
const newVolumeDriver = ref('local')
const newNetworkName = ref('')
const newNetworkDriver = ref('bridge')

const runtimeImageRef = ref('alpine:latest')
const runtimeContainerName = ref('')
const runtimeCommand = ref('')
const runtimePorts = ref('')
const runtimeAutoStart = ref(true)
const runtimeOperationBusy = ref('')
const dockerMigrationPreview = ref<DockerMigrationPreview | null>(null)
const migrationPromptOpen = ref(false)
const migrationBusy = ref(false)
const migrationSourceBusy = ref(false)
const volumeMigrationBusy = ref(false)
const volumeMigrationStatus = ref<DockerMigrationStatus>({ state: 'idle', message: '' })
const migrationStatus = ref<DockerMigrationStatus>({ state: 'idle', message: '' })
const migrationCheck = ref<DockerMigrationPreview | null>(null)
const migrationProgressStatus = computed(() => (
  volumeMigrationBusy.value ? volumeMigrationStatus.value : migrationStatus.value
))

let statsTimer: ReturnType<typeof setInterval> | undefined
let unsubscribeLogs: (() => void) | undefined
let unsubscribeDockerEvents: (() => void) | undefined
let unsubscribeFileDrops: (() => void) | undefined
let unsubscribeTrayRefresh: (() => void) | undefined
let unsubscribeMigrationStatus: (() => void) | undefined
let dockerRefreshTimer: ReturnType<typeof setTimeout> | undefined
let migrationStatusTimer: ReturnType<typeof setTimeout> | undefined
const scrollFadeTimers = new Map<Element, ReturnType<typeof setTimeout>>()

function applyColorTheme() {
  document.documentElement.dataset.theme = colorTheme.value
  document.documentElement.style.colorScheme = colorTheme.value
}

function toggleColorTheme() {
  colorTheme.value = colorTheme.value === 'dark' ? 'light' : 'dark'
  window.localStorage.setItem('dockiva-color-theme', colorTheme.value)
  applyColorTheme()
}

function toggleSidebar() {
  sidebarCompact.value = !sidebarCompact.value
  window.localStorage.setItem(
    'dockiva-sidebar-mode',
    sidebarCompact.value ? 'compact' : 'expanded',
  )
}

function handleScroll(event: Event) {
  const target = event.target instanceof Element
    ? event.target
    : document.scrollingElement ?? document.documentElement

  target.classList.add('is-scrolling')
  const existing = scrollFadeTimers.get(target)
  if (existing) clearTimeout(existing)

  scrollFadeTimers.set(target, setTimeout(() => {
    target.classList.remove('is-scrolling')
    scrollFadeTimers.delete(target)
  }, 850))
}

applyColorTheme()

const runningCount = computed(
  () => containers.value.filter(
    (container) => container.state === 'running',
  ).length,
)

const composeProjectCount = computed(() => {
  return new Set(
    containers.value
      .map((container) => container.composeProject)
      .filter(Boolean),
  ).size
})

const totalCPU = computed(() => {
  return Object.values(stats.value).reduce(
    (sum, item) => sum + (item.cpuPercent || 0),
    0,
  )
})

const totalMemory = computed(() => {
  return Object.values(stats.value).reduce(
    (sum, item) => sum + (item.memoryUsage || 0),
    0,
  )
})

const groups = computed<ContainerGroup[]>(() => {
  const map = new Map<string, Container[]>()

  for (const container of containers.value) {
    const key = container.composeProject
      ? `compose:${container.composeProject}`
      : 'standalone'

    const existing = map.get(key) ?? []
    existing.push(container)
    map.set(key, existing)
  }

  const result: ContainerGroup[] = []

  for (const [key, groupedContainers] of map.entries()) {
    const compose = key.startsWith('compose:')
    const name = compose
      ? groupedContainers[0]?.composeProject || 'Compose project'
      : 'Standalone Containers'

    groupedContainers.sort((a, b) => {
      const serviceA = a.composeService || a.name
      const serviceB = b.composeService || b.name
      return serviceA.localeCompare(serviceB)
    })

    result.push({
      key,
      name,
      compose,
      containers: groupedContainers,
      running: groupedContainers.filter(
        (container) => container.state === 'running',
      ).length,
    })
  }

  return result.sort((a, b) => {
    if (a.compose !== b.compose) {
      return a.compose ? -1 : 1
    }

    return a.name.localeCompare(b.name)
  })
})

const normalizedSearch = computed(
  () => searchQuery.value.trim().toLowerCase(),
)

const filteredGroups = computed<ContainerGroup[]>(() => {
  const query = normalizedSearch.value
  if (!query) {
    return groups.value
  }

  return groups.value
    .map((group) => ({
      ...group,
      containers: group.containers.filter((container) => {
        const haystack = [
          group.name,
          container.name,
          container.image,
          container.state,
          container.status,
          container.composeProject,
          container.composeService,
          container.shortId,
          ...(container.ports || []).map((port) => port.display),
        ]
          .filter(Boolean)
          .join(' ')
          .toLowerCase()

        return haystack.includes(query)
      }),
    }))
    .filter(
      (group) =>
        group.name.toLowerCase().includes(query) ||
        group.containers.length > 0,
    )
})

const filteredImages = computed(() => {
  const query = normalizedSearch.value
  if (!query) {
    return images.value
  }

  return images.value.filter((image) =>
    [...(image.tags || []), image.shortId]
      .join(' ')
      .toLowerCase()
      .includes(query),
  )
})

const filteredVolumes = computed(() => {
  const query = normalizedSearch.value
  if (!query) {
    return volumes.value
  }

  return volumes.value.filter((volume) =>
    [volume.name, volume.driver, volume.scope, volume.mountpoint]
      .join(' ')
      .toLowerCase()
      .includes(query),
  )
})

const filteredNetworks = computed(() => {
  const query = normalizedSearch.value
  if (!query) {
    return networks.value
  }

  return networks.value.filter((network) =>
    [network.name, network.driver, network.scope, network.shortId]
      .join(' ')
      .toLowerCase()
      .includes(query),
  )
})

const activeRuntime = computed(
  () => runtimeOverview.value?.active,
)

const runtimeConnected = computed(
  () => activeRuntime.value?.connected ?? dockerStatus.value?.connected ?? false,
)

const isDockerRuntime = computed(
  () => (activeRuntime.value?.provider ?? 'docker') === 'docker',
)

const runtimeCapabilities = computed<RuntimeCapabilities>(() => {
  return activeRuntime.value?.capabilities ?? {
    containers: true,
    lifecycle: true,
    stats: true,
    logs: true,
    terminal: true,
    images: true,
    pullImages: false,
    createContainer: false,
    volumes: true,
    networks: true,
    portPublishing: true,
    dns: true,
    compose: true,
  }
})

const tabs = computed(() => {
  const result = [
    { key: 'overview' as TabName, label: 'Smart Check', icon: 'overview', count: 0, section: 'Care', first: true },
    { key: 'containers' as TabName, label: 'Containers', icon: 'containers', count: containers.value.length, section: 'Manage', first: true },
  ]

  if (runtimeCapabilities.value.images || runtimeCapabilities.value.volumes || runtimeCapabilities.value.networks) {
    if (runtimeCapabilities.value.images) {
      result.push({ key: 'images' as TabName, label: 'Images', icon: 'images', count: images.value.length, section: 'Manage', first: false })
    }
    if (runtimeCapabilities.value.volumes) {
      result.push({ key: 'volumes' as TabName, label: 'Volumes', icon: 'volumes', count: volumes.value.length, section: 'Manage', first: false })
    }
    if (runtimeCapabilities.value.networks) {
      result.push({ key: 'networks' as TabName, label: 'Networks', icon: 'networks', count: networks.value.length, section: 'Manage', first: false })
    }
  }

  if (isDockerRuntime.value) {
    result.push({ key: 'storage' as TabName, label: 'Space Cleanup', icon: 'storage', count: diskUsage.value.length, section: 'Maintain', first: true })
  }

  result.push({ key: 'engine' as TabName, label: 'Engine Settings', icon: 'engine', count: 0, section: 'Preferences', first: true })
  return result
})

function clearMessages() {
  error.value = ''
  success.value = ''
}

function showError(err: unknown) {
  success.value = ''
  error.value = errorMessage(err)
}

function errorMessage(err: unknown) {
  if (err instanceof Error) {
    return err.message
  }
  if (typeof err === 'string') {
    return err
  }
  if (err && typeof err === 'object' && 'message' in err) {
    return String((err as { message: unknown }).message)
  }
  return String(err)
}

function showSuccess(message: string, output = '') {
  error.value = ''
  success.value = message
  commandOutput.value = output
}

async function loadStatus() {
  dockerStatus.value = await Call.ByName(
    'main.DockerService.Status',
  ) as DockerStatus
}

async function loadPlatformInfo() {
  platformInfo.value = await Call.ByName(
    'main.DockerService.GetPlatformInfo',
  ) as PlatformInfo
}

async function loadRuntimeOverview() {
  runtimeOverview.value = await Call.ByName(
    'main.DockerService.GetRuntimeOverview',
  ) as RuntimeOverview
}

async function loadContainers() {
  containers.value = await Call.ByName(
    'main.DockerService.ListContainers',
  ) as Container[]

  if (runtimeCapabilities.value.stats) {
    await loadStats()
  } else {
    stats.value = {}
  }
}

async function loadDockerMigrationPreview(openPrompt = false) {
  const preview = await Call.ByName(
    'main.DockerService.GetDockerMigrationPreview',
  ) as DockerMigrationPreview
  dockerMigrationPreview.value = preview
  migrationCheck.value = preview
  if (openPrompt && preview.available) migrationPromptOpen.value = true
}

async function startDockerMigrationSource() {
  if (migrationSourceBusy.value) return
  migrationSourceBusy.value = true
  clearMessages()
  try {
    const preview = await Call.ByName(
      'main.DockerService.StartDockerMigrationSource',
    ) as DockerMigrationPreview
    dockerMigrationPreview.value = preview
    migrationCheck.value = preview
    if (preview.available) migrationPromptOpen.value = true
    else if (!preview.reachable && preview.message) showError(preview.message)
  } catch (err) {
    showError(err)
  } finally {
    migrationSourceBusy.value = false
  }
}

async function scanDockerMigrationSource() {
  if (migrationSourceBusy.value) return
  migrationSourceBusy.value = true
  clearMessages()
  try {
    await loadDockerMigrationPreview(true)
  } catch (err) {
    showError(err)
  } finally {
    migrationSourceBusy.value = false
  }
}

function dismissMigrationPrompt() {
  migrationPromptOpen.value = false
}

async function migrateDockerImages() {
  if (migrationBusy.value) return
  clearMessages()
  try {
    migrationStatus.value = await Call.ByName(
      'main.DockerService.StartDockerImageMigration',
    ) as DockerMigrationStatus
    migrationBusy.value = migrationStatus.value.state === 'running'
    await pollDockerMigrationStatus()
  } catch (err) {
    migrationBusy.value = false
    showError(err)
  }
}

async function migrateDockerContainers() {
  if (migrationBusy.value) return
  clearMessages()
  try {
    migrationStatus.value = await Call.ByName(
      'main.DockerService.StartDockerContainerMigration',
    ) as DockerMigrationStatus
    migrationBusy.value = migrationStatus.value.state === 'running'
    await pollDockerMigrationStatus()
  } catch (err) {
    migrationBusy.value = false
    showError(err)
  }
}

async function migrateDockerVolumes() {
  if (volumeMigrationBusy.value) return
  volumeMigrationBusy.value = true
  clearMessages()
  try {
    const result = await Call.ByName(
      'main.DockerService.MigrateDockerVolumes',
    ) as CLIResult
    showSuccess('Docker named volumes copied into Dockiva Native.', result.output)
  } catch (err) {
    showError(err)
  } finally {
    volumeMigrationBusy.value = false
  }
}

async function pollDockerMigrationStatus() {
  if (migrationStatusTimer) clearTimeout(migrationStatusTimer)
  try {
    migrationStatus.value = await Call.ByName(
      'main.DockerService.GetDockerMigrationStatus',
    ) as DockerMigrationStatus
    migrationBusy.value = migrationStatus.value.state === 'running'
    if (migrationBusy.value) {
      migrationStatusTimer = setTimeout(() => { void pollDockerMigrationStatus() }, 1000)
      return
    }
    if (migrationStatus.value.state === 'complete') {
      if (migrationStatus.value.kind === 'container') {
        showSuccess('Docker containers copied into Dockiva Native.', migrationStatus.value.message)
        await loadContainers()
      } else {
        showSuccess('Docker images copied into Dockiva Native.', migrationStatus.value.message)
        await loadImages()
      }
    } else if (migrationStatus.value.state === 'failed') {
      showError(migrationStatus.value.error || migrationStatus.value.message)
    }
  } catch (err) {
    migrationBusy.value = false
    showError(err)
  }
}

async function loadImages() {
  images.value = await Call.ByName(
    'main.DockerService.ListImages',
  ) as ImageInfo[]
}

async function loadVolumes() {
  volumes.value = await Call.ByName(
    'main.DockerService.ListVolumes',
  ) as VolumeInfo[]
}

async function loadNetworks() {
  networks.value = await Call.ByName(
    'main.DockerService.ListNetworks',
  ) as NetworkInfo[]
}

async function loadDiskUsage() {
  diskUsage.value = await Call.ByName(
    'main.DockerService.GetDiskUsage',
  ) as DiskUsageItem[]
}

async function loadHostContainerStorage() {
  hostContainerStorage.value = await Call.ByName(
    'main.DockerService.GetHostContainerStorage',
  ) as HostContainerStorageItem[]
}

async function loadCurrentTab() {
  loading.value = true
  clearMessages()

  try {
    await Promise.allSettled([
      loadStatus(),
      loadRuntimeOverview(),
    ])

    if (activeTab.value === 'engine') {
      await loadPlatformInfo()
      return
    }

    if (activeTab.value === 'storage') {
      await loadHostContainerStorage()
    }

    if (!runtimeConnected.value) {
      return
    }

    switch (activeTab.value) {
      case 'overview':
        break
      case 'containers':
        await loadContainers()
        break
      case 'images':
        await loadImages()
        break
      case 'volumes':
        await loadVolumes()
        break
      case 'networks':
        await loadNetworks()
        break
      case 'storage':
        await Promise.allSettled([
          loadDiskUsage(),
          loadHostContainerStorage(),
        ])
        break
    }
  } catch (err) {
    showError(err)
  } finally {
    loading.value = false
  }
}

async function runSmartCheck() {
  if (smartCheckBusy.value) return
  smartCheckBusy.value = true
  clearMessages()
  const animationFloor = new Promise((resolve) => setTimeout(resolve, 1400))

  try {
    await Promise.allSettled([loadStatus(), loadRuntimeOverview(), loadPlatformInfo(), loadDockerMigrationPreview()])
    if (runtimeConnected.value) {
      await loadContainers()
      if (isDockerRuntime.value) {
        await Promise.allSettled([loadAllCounts(), loadDiskUsage()])
      }
      showSuccess('Smart Check complete. Your container workspace is up to date.')
    }
  } catch (err) {
    showError(err)
  } finally {
    await animationFloor
    smartCheckBusy.value = false
  }
}

async function loadAllCounts() {
  if (!dockerStatus.value?.connected) {
    return
  }

  const results = await Promise.allSettled([
    Call.ByName('main.DockerService.ListImages'),
    Call.ByName('main.DockerService.ListVolumes'),
    Call.ByName('main.DockerService.ListNetworks'),
  ])

  if (results[0].status === 'fulfilled') {
    images.value = results[0].value as ImageInfo[]
  }
  if (results[1].status === 'fulfilled') {
    volumes.value = results[1].value as VolumeInfo[]
  }
  if (results[2].status === 'fulfilled') {
    networks.value = results[2].value as NetworkInfo[]
  }
}

async function loadStats() {
  if (statsLoading.value || !dockerStatus.value?.connected) {
    return
  }

  const runningIDs = containers.value
    .filter((container) => container.state === 'running')
    .map((container) => container.id)

  if (!runningIDs.length) {
    stats.value = {}
    return
  }

  statsLoading.value = true

  try {
    const result = await Call.ByName(
      'main.DockerService.GetContainerStats',
      runningIDs,
    ) as ResourceStats[]

    stats.value = Object.fromEntries(
      result.map((item) => [item.id, item]),
    )
  } finally {
    statsLoading.value = false
  }
}

function startStatsPolling() {
  stopStatsPolling()

  statsTimer = setInterval(() => {
    if (
      activeTab.value === 'containers' &&
      runtimeCapabilities.value.stats
    ) {
      void loadStats()
    }
  }, 3000)
}

function stopStatsPolling() {
  if (statsTimer) {
    clearInterval(statsTimer)
    statsTimer = undefined
  }
}

function toggleGroup(key: string) {
  collapsedGroups.value[key] = !collapsedGroups.value[key]
}

function statFor(container: Container) {
  return stats.value[container.id]
}

function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) {
    return '0 B'
  }

  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const index = Math.min(
    Math.floor(Math.log(bytes) / Math.log(1024)),
    units.length - 1,
  )

  const value = bytes / Math.pow(1024, index)
  return `${value.toFixed(value >= 100 ? 0 : 1)} ${units[index]}`
}

function formatCPU(value?: number): string {
  if (value === undefined || !Number.isFinite(value)) {
    return '—'
  }

  return value > 0 && value < 0.1 ? '<0.1%' : `${value.toFixed(1)}%`
}

function activeResourcePercent(item: DiskUsageItem): number {
  if (!item.totalCount) return 0
  return Math.min(100, Math.max(0, (item.active / item.totalCount) * 100))
}

function formatDate(timestamp: number): string {
  return timestamp ? new Date(timestamp * 1000).toLocaleString() : '—'
}

function openURL(url?: string) {
  if (url) {
    window.open(url, '_blank', 'noopener,noreferrer')
  }
}

function isBusy(container: Container, action?: ContainerAction) {
  return busyId.value === container.id &&
    (!action || busyAction.value === action)
}

function isProjectBusy(group: ContainerGroup, action?: ProjectAction) {
  return busyProject.value === group.name &&
    (!action || busyProjectAction.value === action)
}

async function runProjectAction(
  group: ContainerGroup,
  action: ProjectAction,
) {
  if (!group.compose || busyProject.value || busyId.value || composeBusy.value) {
    return
  }

  clearMessages()
  busyProject.value = group.name
  busyProjectAction.value = action

  try {
    await Call.ByName(
      'main.DockerService.ComposeProjectAction',
      group.name,
      action,
    )
    await loadContainers()
  } catch (err) {
    showError(err)
  } finally {
    busyProject.value = ''
    busyProjectAction.value = ''
  }
}

async function runCompose(
  group: ContainerGroup,
  action: ComposeAction,
) {
  if (!group.compose || composeBusy.value || busyProject.value || busyId.value) {
    return
  }

  if (action === 'down') {
    const okay = window.confirm(
      `Run "docker compose down" for "${group.name}"?\n\nThis removes the project's containers and networks. Named volumes are not removed.`,
    )
    if (!okay) {
      return
    }
  }

  if (action === 'rebuild') {
    const okay = window.confirm(
      `Rebuild and recreate "${group.name}"?\n\nDockiva will run "docker compose build" followed by "docker compose up -d".`,
    )
    if (!okay) {
      return
    }
  }

  clearMessages()
  commandOutput.value = ''
  composeBusy.value = `${group.name}:${action}`

  try {
    const result = await Call.ByName(
      'main.DockerService.RunComposeCommand',
      group.name,
      action,
    ) as CLIResult

    showSuccess(
      `Compose ${action} completed for ${group.name}.`,
      result.output,
    )

    await loadContainers()
    await loadAllCounts()
  } catch (err) {
    showError(err)
  } finally {
    composeBusy.value = ''
  }
}

async function runContainerAction(
  container: Container,
  action: ContainerAction,
) {
  if (busyId.value || busyProject.value || composeBusy.value) {
    return
  }

  clearMessages()
  busyId.value = container.id
  busyAction.value = action

  try {
    switch (action) {
      case 'start':
        await Call.ByName('main.DockerService.StartContainer', container.id)
        break
      case 'stop':
        await Call.ByName('main.DockerService.StopContainer', container.id)
        break
      case 'restart':
        await Call.ByName('main.DockerService.RestartContainer', container.id)
        break
      case 'delete': {
        const force = container.state === 'running'
        const okay = window.confirm(
          `Delete container "${container.name || container.shortId}"?\n\nNamed volumes are not deleted.`,
        )
        if (!okay) {
          return
        }
        await Call.ByName(
          'main.DockerService.RemoveContainer',
          container.id,
          force,
        )
        break
      }
    }

    await loadContainers()
  } catch (err) {
    showError(err)
  } finally {
    busyId.value = ''
    busyAction.value = ''
  }
}

async function pullImage() {
  const reference = pullImageRef.value.trim()
  if (!reference || resourceBusy.value) {
    return
  }

  clearMessages()
  resourceBusy.value = 'pull-image'
  commandOutput.value = ''

  try {
    const result = await Call.ByName(
      'main.DockerService.PullImage',
      reference,
    ) as CLIResult

    showSuccess(`Pulled ${reference}.`, result.output)
    pullImageRef.value = ''
    await loadImages()
  } catch (err) {
    showError(err)
  } finally {
    resourceBusy.value = ''
  }
}

async function createVolume() {
  const name = newVolumeName.value.trim()
  if (!name || resourceBusy.value) {
    return
  }

  clearMessages()
  resourceBusy.value = 'create-volume'

  try {
    const result = await Call.ByName(
      'main.DockerService.CreateVolume',
      name,
      newVolumeDriver.value,
    ) as CLIResult

    showSuccess(`Created volume ${name}.`, result.output)
    newVolumeName.value = ''
    await loadVolumes()
  } catch (err) {
    showError(err)
  } finally {
    resourceBusy.value = ''
  }
}

async function createNetwork() {
  const name = newNetworkName.value.trim()
  if (!name || resourceBusy.value) {
    return
  }

  clearMessages()
  resourceBusy.value = 'create-network'

  try {
    const result = await Call.ByName(
      'main.DockerService.CreateNetwork',
      name,
      newNetworkDriver.value,
    ) as CLIResult

    showSuccess(`Created network ${name}.`, result.output)
    newNetworkName.value = ''
    await loadNetworks()
  } catch (err) {
    showError(err)
  } finally {
    resourceBusy.value = ''
  }
}

async function removeImage(image: ImageInfo) {
  if (!window.confirm(`Delete image "${image.tags?.[0] || image.shortId}"?`)) {
    return
  }

  resourceBusy.value = image.id
  clearMessages()

  try {
    await Call.ByName('main.DockerService.RemoveImage', image.id, false)
    await loadImages()
  } catch (err) {
    showError(err)
  } finally {
    resourceBusy.value = ''
  }
}

async function removeVolume(volume: VolumeInfo) {
  if (!window.confirm(
    `Delete volume "${volume.name}"?\n\nThis permanently deletes its stored data.`,
  )) {
    return
  }

  resourceBusy.value = volume.name
  clearMessages()

  try {
    await Call.ByName('main.DockerService.RemoveVolume', volume.name, false)
    await loadVolumes()
  } catch (err) {
    showError(err)
  } finally {
    resourceBusy.value = ''
  }
}

function isDefaultNetwork(network: NetworkInfo) {
  return ['bridge', 'host', 'none', 'dockiva-net'].includes(network.name)
}

async function removeNetwork(network: NetworkInfo) {
  if (isDefaultNetwork(network)) {
    return
  }

  if (!window.confirm(`Delete network "${network.name}"?`)) {
    return
  }

  resourceBusy.value = network.id
  clearMessages()

  try {
    await Call.ByName('main.DockerService.RemoveNetwork', network.id)
    await loadNetworks()
  } catch (err) {
    showError(err)
  } finally {
    resourceBusy.value = ''
  }
}

async function prune(scope: string) {
  const labels: Record<string, string> = {
    system: 'unused containers, networks, dangling images, and build cache',
    containers: 'all stopped containers',
    images: 'dangling images',
    networks: 'unused networks',
    volumes: 'unused anonymous volumes',
  }

  const okay = window.confirm(
    `Prune ${scope}?\n\nThis will remove ${labels[scope] || 'unused Docker resources'}.`,
  )

  if (!okay) {
    return
  }

  clearMessages()
  resourceBusy.value = `prune:${scope}`

  try {
    const result = await Call.ByName(
      'main.DockerService.PruneDocker',
      scope,
    ) as CLIResult

    showSuccess(`Docker ${scope} prune completed.`, result.output)
    await Promise.all([
      loadDiskUsage(),
      loadHostContainerStorage(),
      loadAllCounts(),
    ])
  } catch (err) {
    showError(err)
  } finally {
    resourceBusy.value = ''
  }
}

async function revealHostContainerStorage(item: HostContainerStorageItem) {
  try {
    await Call.ByName('main.DockerService.RevealHostContainerStorage', item.id)
  } catch (err) {
    showError(err)
  }
}

function reviewHostContainerStorage(item: HostContainerStorageItem) {
  storageCleanupTarget.value = item
  storageCleanupMode.value = null
  storageCleanupError.value = ''
}

function closeStorageCleanup() {
  if (resourceBusy.value) {
    return
  }
  storageCleanupTarget.value = null
  storageCleanupMode.value = null
  storageCleanupError.value = ''
}

function selectHostCleanupMode(mode: HostCleanupMode) {
  storageCleanupMode.value = mode
  storageCleanupError.value = ''
}

function cleanupModeLabel(mode: HostCleanupMode) {
  return mode === 'cache'
    ? 'Clean build cache'
    : mode === 'deep'
      ? 'Run deep cleanup'
      : 'Clean unused resources'
}

async function cleanHostContainerStorage(item: HostContainerStorageItem) {
  const mode = storageCleanupMode.value
  if (!mode) {
    return
  }

  clearMessages()
  storageCleanupError.value = ''
  resourceBusy.value = `host-storage:${item.id}`

  try {
    const result = await Call.ByName(
      'main.DockerService.CleanHostContainerStorage',
      item.id,
      mode,
    ) as CLIResult
    showSuccess(`${item.name} cleanup completed.`, result.output)
    await Promise.allSettled([
      loadHostContainerStorage(),
      loadDiskUsage(),
      loadAllCounts(),
    ])
    storageCleanupTarget.value = null
    storageCleanupMode.value = null
  } catch (err) {
    storageCleanupError.value = errorMessage(err)
    showError(err)
  } finally {
    resourceBusy.value = ''
  }
}

function appendLimited(current: string, chunk: string, limit = 1_000_000) {
  const combined = current + chunk
  return combined.length > limit
    ? combined.slice(combined.length - limit)
    : combined
}

async function scrollLogsToBottom() {
  await nextTick()
  if (logsViewport.value) {
    logsViewport.value.scrollTop = logsViewport.value.scrollHeight
  }
}

async function openLogs(container: Container) {
  await closeLogs()

  logsOpen.value = true
  logsContainer.value = container
  logsText.value = ''
  logsFollowing.value = true

  try {
    logsStreamID.value = await Call.ByName(
      'main.DockerService.StartLogStream',
      container.id,
      logTail.value,
    ) as string
  } catch (err) {
    logsFollowing.value = false
    showError(err)
  }
}

async function restartLogs() {
  if (logsContainer.value) {
    const current = logsContainer.value
    await openLogs(current)
  }
}

async function closeLogs() {
  const streamID = logsStreamID.value
  logsStreamID.value = ''
  logsFollowing.value = false

  if (streamID) {
    try {
      await Call.ByName('main.DockerService.StopLogStream', streamID)
    } catch {
      // best-effort
    }
  }

  logsOpen.value = false
  logsContainer.value = null
  logsText.value = ''
}

function setupLogListener() {
  unsubscribeLogs = Events.On(
    'dockiva:log-output',
    (payload: any) => {
      const data = payload as StreamOutputEvent

      const matchesStream = logsStreamID.value
        ? data?.streamId === logsStreamID.value
        : logsOpen.value && data?.containerId === logsContainer.value?.id

      if (!data || !matchesStream) {
        return
      }

      if (data.data) {
        logsText.value = appendLimited(logsText.value, data.data)
        void scrollLogsToBottom()
      }

      if (data.error) {
        logsText.value = appendLimited(
          logsText.value,
          `\n[Dockiva] ${data.error}\n`,
        )
      }

      if (data.closed) {
        logsFollowing.value = false
      }
    },
  )
}


async function runtimePullImage() {
  const reference = runtimeImageRef.value.trim()
  if (!reference || runtimeOperationBusy.value) return

  runtimeOperationBusy.value = 'pull'
  clearMessages()
  commandOutput.value = ''

  try {
    const result = await Call.ByName(
      'main.DockerService.RuntimePullImage',
      reference,
    ) as RuntimeOperationResult

    showSuccess(result.message, result.detail || '')
  } catch (err) {
    showError(err)
  } finally {
    runtimeOperationBusy.value = ''
  }
}

function parseRuntimePorts(value: string): RuntimePortMapping[] {
  const text = value.trim()
  if (!text) return []

  const mappings: RuntimePortMapping[] = []

  for (const rawPart of text.split(',')) {
    const part = rawPart.trim()
    if (!part) continue

    const segments = part.split(':')
    if (segments.length !== 2) {
      throw new Error(
        `Invalid port mapping "${part}". Use HOST:CONTAINER, e.g. 8080:80.`,
      )
    }

    const hostPort = Number(segments[0])
    const containerPort = Number(segments[1])

    if (
      !Number.isInteger(hostPort) ||
      !Number.isInteger(containerPort) ||
      hostPort < 1024 ||
      hostPort > 65535 ||
      containerPort < 1 ||
      containerPort > 65535
    ) {
      throw new Error(
        `Invalid port mapping "${part}". Host ports must be 1024-65535.`,
      )
    }

    mappings.push({
      hostPort,
      containerPort,
      protocol: 'tcp',
      hostIp: '127.0.0.1',
    })
  }

  return mappings
}

async function runtimeCreateContainer() {
  const name = runtimeContainerName.value.trim()
  const image = runtimeImageRef.value.trim()

  if (!name || !image || runtimeOperationBusy.value) return

  let command: string[] = []
  try {
    if (runtimeCommand.value.trim()) {
      command = activeRuntime.value?.provider === 'vmfree'
        ? JSON.parse(runtimeCommand.value)
        : runtimeCommand.value.trim().split(/\s+/)
      if (!Array.isArray(command) || !command.every(arg => typeof arg === 'string')) throw new Error('Command must be a JSON array of strings.')
    }
  } catch (err) { showError(err); return }

  let portMappings: RuntimePortMapping[] = []

  try {
    portMappings = parseRuntimePorts(runtimeCapabilities.value.portPublishing ? runtimePorts.value : '')
  } catch (err) {
    showError(err)
    return
  }

  runtimeOperationBusy.value = 'create'
  clearMessages()

  try {
    await Call.ByName(
      'main.DockerService.RuntimeCreateContainer',
      name,
      image,
      command,
      runtimeAutoStart.value,
      portMappings,
    )

    runtimeContainerName.value = ''
    runtimePorts.value = ''
    await loadContainers()
    showSuccess(`Created ${name} with ${activeRuntime.value?.displayName || 'runtime'}.`)
  } catch (err) {
    showError(err)
  } finally {
    runtimeOperationBusy.value = ''
  }
}

async function importComposePath(path: string) {
  path = path?.trim()
  if (!path || importBusy.value) {
    return
  }

  clearMessages()
  importBusy.value = true
  commandOutput.value = ''

  try {
    const result = await Call.ByName(
      'main.DockerService.ImportComposePath',
      path,
    ) as ComposeImportResult

    showSuccess(
      `Imported Compose project ${result.project}.`,
      result.output,
    )

    activeTab.value = 'containers'
    await loadContainers()
    await loadAllCounts()
  } catch (err) {
    showError(err)
  } finally {
    importBusy.value = false
  }
}

async function chooseComposeFile() {
  try {
    const path = await Call.ByName(
      'main.DockerService.SelectComposeFile',
    ) as string

    if (path) {
      await importComposePath(path)
    }
  } catch (err) {
    showError(err)
  }
}

async function chooseComposeFolder() {
  try {
    const path = await Call.ByName(
      'main.DockerService.SelectComposeFolder',
    ) as string

    if (path) {
      await importComposePath(path)
    }
  } catch (err) {
    showError(err)
  }
}

function scheduleDockerRefresh() {
  if (dockerRefreshTimer) {
    clearTimeout(dockerRefreshTimer)
  }
  if (migrationStatusTimer) {
    clearTimeout(migrationStatusTimer)
  }

  dockerRefreshTimer = setTimeout(() => {
    void refreshFromDockerEvent()
  }, 450)
}

async function refreshFromDockerEvent() {
  if (!dockerStatus.value?.connected) {
    try {
      await loadStatus()
    } catch {
      return
    }
  }

  try {
    switch (activeTab.value) {
      case 'containers':
        await loadContainers()
        break
      case 'images':
        await loadImages()
        break
      case 'volumes':
        await loadVolumes()
        break
      case 'networks':
        await loadNetworks()
        break
      case 'storage':
        await loadDiskUsage()
        break
      case 'engine':
        await Promise.all([loadStatus(), loadPlatformInfo()])
        break
    }
  } catch {
    // Event-driven refresh is best-effort; manual Refresh remains available.
  }
}

function setupPlatformListeners() {
  unsubscribeDockerEvents = Events.On(
    'dockiva:docker-event',
    () => {
      scheduleDockerRefresh()
    },
  )

  unsubscribeFileDrops = Events.On(
    'dockiva:files-dropped',
    (payload: any) => {
      const data = payload?.data ?? payload
      const files = Array.isArray(data) ? data : data?.files

      if (!Array.isArray(files) || files.length === 0) {
        return
      }

      void importComposePath(String(files[0]))
    },
  )

  unsubscribeTrayRefresh = Events.On(
    'dockiva:tray-refresh',
    () => {
      void loadCurrentTab()
    },
  )

  unsubscribeMigrationStatus = Events.On(
    'dockiva:migration-status',
    (payload: any) => {
      const data = (payload?.data ?? payload) as DockerMigrationStatus
      if (data.kind === 'volume') {
        volumeMigrationStatus.value = data
        volumeMigrationBusy.value = data.state === 'running'
        if (data.state === 'complete') {
          void loadVolumes()
          showSuccess('Docker volumes copied into Dockiva Native.', data.message)
        } else if (data.state === 'failed') {
          showError(data.error || data.message)
        }
        return
      }
      migrationStatus.value = data
      migrationBusy.value = data.state === 'running'
      if (data.state === 'complete') {
        if (data.kind === 'container') {
          showSuccess('Docker containers copied into Dockiva Native.', data.message)
          void loadContainers()
        } else {
          showSuccess('Docker images copied into Dockiva Native.', data.message)
          void loadImages()
        }
      } else if (data.state === 'failed') {
        showError(data.error || data.message)
      }
    },
  )
}

watch(activeTab, () => {
  void loadCurrentTab()
})

onMounted(async () => {
  document.addEventListener('scroll', handleScroll, true)
  setupLogListener()
  setupPlatformListeners()

  try {
    await Promise.allSettled([
      loadStatus(),
      loadPlatformInfo(),
      loadRuntimeOverview(),
      loadDockerMigrationPreview(),
    ])

    if (runtimeConnected.value) {
      await loadContainers()

      if (isDockerRuntime.value) {
        await loadAllCounts()
      }
    }
  } catch (err) {
    showError(err)
  }

  startStatsPolling()
})

onBeforeUnmount(() => {
  document.removeEventListener('scroll', handleScroll, true)
  for (const [target, timer] of scrollFadeTimers) {
    clearTimeout(timer)
    target.classList.remove('is-scrolling')
  }
  scrollFadeTimers.clear()
  stopStatsPolling()
  unsubscribeLogs?.()
  unsubscribeDockerEvents?.()
  unsubscribeFileDrops?.()
  unsubscribeTrayRefresh?.()
  unsubscribeMigrationStatus?.()

  if (dockerRefreshTimer) {
    clearTimeout(dockerRefreshTimer)
  }

  void closeLogs()
})
</script>

<template>
  <div
    class="app-shell flex min-h-screen text-zinc-100"
    :class="{ 'sidebar-compact': sidebarCompact }"
  >
    <div class="ambient ambient-one" />
    <div class="ambient ambient-two" />

    <header class="window-titlebar fixed inset-x-0 top-0 z-30 h-14">
      <div class="window-titlebar-glow" aria-hidden="true" />
      <div class="window-title">
        {{ activeTab === 'overview' ? 'Smart Check' : tabs.find(tab => tab.key === activeTab)?.label }}
      </div>
      <div class="window-title-actions">
        <button
          class="titlebar-theme-toggle"
          type="button"
          :title="`Switch to ${colorTheme === 'dark' ? 'light' : 'dark'} mode`"
          :aria-label="`Switch to ${colorTheme === 'dark' ? 'light' : 'dark'} mode`"
          @click="toggleColorTheme"
        >
          <svg v-if="colorTheme === 'dark'" viewBox="0 0 24 24" aria-hidden="true">
            <circle cx="12" cy="12" r="3.5" />
            <path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" />
          </svg>
          <svg v-else viewBox="0 0 24 24" aria-hidden="true">
            <path d="M20.4 15.3A8.5 8.5 0 0 1 8.7 3.6 8.5 8.5 0 1 0 20.4 15.3Z" />
          </svg>
        </button>
      </div>
    </header>

    <aside class="sidebar fixed bottom-0 left-0 top-14 z-10">
      <button
        class="sidebar-toggle"
        type="button"
        :title="sidebarCompact ? 'Expand sidebar' : 'Compact sidebar'"
        :aria-label="sidebarCompact ? 'Expand sidebar' : 'Compact sidebar'"
        @click="toggleSidebar"
      >
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path :d="sidebarCompact ? 'm9 6 6 6-6 6' : 'm15 6-6 6 6 6'" />
        </svg>
      </button>

      <div class="brand px-5 pb-5 pt-6">
        <img class="brand-mark" :src="brandAppIcon" alt="" aria-hidden="true" />
        <div class="brand-copy">
          <h1 class="text-lg font-semibold tracking-tight">Dockiva</h1>
          <p class="mt-0.5 text-[11px] text-zinc-500">Local container studio</p>
        </div>
      </div>

      <nav class="px-3">
        <template v-for="tab in tabs" :key="tab.key">
          <div v-if="tab.first" class="nav-section px-2 pb-2 pt-5 text-[10px] font-semibold uppercase tracking-[0.18em] text-zinc-600">
            {{ tab.section }}
          </div>
          <button
            class="nav-item mb-1 flex w-full items-center gap-3 rounded-xl px-3 py-2.5 text-left text-sm transition"
            :class="activeTab === tab.key ? 'nav-item-active text-white' : 'text-zinc-500 hover:text-zinc-200'"
            :data-label="tab.label"
            :aria-label="tab.label"
            @click="activeTab = tab.key"
          >
            <span class="nav-icon" :class="`icon-${tab.icon}`" aria-hidden="true">
              <svg v-if="tab.icon === 'overview'" viewBox="0 0 24 24"><path d="M12 3.5 14.1 9l5.4 2-5.4 2L12 18.5 9.9 13l-5.4-2 5.4-2L12 3.5Z" /><path d="m18.5 3 .7 1.8L21 5.5l-1.8.7-.7 1.8-.7-1.8-1.8-.7 1.8-.7.7-1.8Z" /></svg>
              <svg v-else-if="tab.icon === 'containers'" viewBox="0 0 24 24"><path d="M4 7.5 12 3l8 4.5v9L12 21l-8-4.5v-9Z M4.5 7.8 12 12l7.5-4.2M12 12v8.5" /></svg>
              <svg v-else-if="tab.icon === 'images'" viewBox="0 0 24 24"><path d="m12 3 9 5-9 5-9-5 9-5Z M3 12l9 5 9-5M3 16l9 5 9-5" /></svg>
              <svg v-else-if="tab.icon === 'volumes'" viewBox="0 0 24 24"><path d="M5 6c0-1.7 3.1-3 7-3s7 1.3 7 3-3.1 3-7 3-7-1.3-7-3Z M5 6v6c0 1.7 3.1 3 7 3s7-1.3 7-3V6M5 12v6c0 1.7 3.1 3 7 3s7-1.3 7-3v-6" /></svg>
              <svg v-else-if="tab.icon === 'networks'" viewBox="0 0 24 24"><circle cx="12" cy="5" r="2.5" /><circle cx="5" cy="18" r="2.5" /><circle cx="19" cy="18" r="2.5" /><path d="m10.8 7.2-4.6 8.6M13.2 7.2l4.6 8.6M7.5 18h9" /></svg>
              <svg v-else-if="tab.icon === 'storage'" viewBox="0 0 24 24"><path d="M4 5.5h16v13H4z" /><path d="M8 15h.01M12 15h4" /></svg>
              <svg v-else viewBox="0 0 24 24"><path d="M4 7h10M18 7h2M4 17h2M10 17h10M14 4v6M6 14v6" /></svg>
            </span>
            <span class="nav-label flex-1">{{ tab.label }}</span>
            <span v-if="['containers', 'images', 'volumes', 'networks'].includes(tab.key)" class="nav-count rounded-full px-2 py-0.5 text-[10px]">{{ tab.count }}</span>
          </button>
        </template>
      </nav>

      <div v-if="dockerStatus || platformInfo" class="sidebar-status absolute bottom-4 left-3 right-3 rounded-2xl p-3.5">
        <div v-if="dockerStatus" class="flex items-center gap-3 text-xs">
          <span class="status-orb" :class="dockerStatus.connected ? 'is-online' : 'is-offline'"><i /></span>
          <div class="sidebar-status-copy min-w-0">
            <div class="font-medium text-zinc-200">
              {{ dockerStatus.connected ? 'Runtime ready' : 'Runtime offline' }}
            </div>
            <div class="mt-0.5 truncate text-[10px] text-zinc-600">
              {{ activeRuntime?.displayName || 'Container engine' }}
            </div>
          </div>
        </div>

        <div
          v-if="platformInfo"
          class="sidebar-platform mt-3 truncate border-t border-white/[0.05] pt-2.5 text-[10px] text-zinc-600"
          :title="`${platformInfo.dockerContext || ''} ${platformInfo.dockerHost || ''}`"
        >
          {{ platformInfo.os }}/{{ platformInfo.arch }}
          <template v-if="platformInfo.dockerContext">
            · {{ platformInfo.dockerContext }}
          </template>
        </div>
      </div>
    </aside>

    <div class="app-content min-w-0 flex-1 pt-14">
      <main :key="activeTab" class="content-canvas relative z-[1] p-8 pt-6">
        <div v-if="!['overview', 'engine'].includes(activeTab)" class="content-tools">
          <p v-if="activeTab === 'containers'" class="content-context text-xs text-zinc-500">
            {{ containers.length }} total ·
            <span class="text-emerald-500">{{ runningCount }} running</span>
            · {{ activeRuntime?.displayName || 'runtime' }}
            <template v-if="runtimeCapabilities.compose && composeProjectCount">
              · {{ composeProjectCount }} Compose project{{ composeProjectCount === 1 ? '' : 's' }}
            </template>
          </p>
          <span v-else />
          <div class="flex items-center gap-3">
          <div
            v-if="activeTab === 'containers' && runtimeConnected && runtimeCapabilities.stats"
            class="hidden items-center gap-4 text-xs text-zinc-500 lg:flex"
          >
            <span>
              CPU
              <strong class="ml-1 font-medium text-zinc-300">{{ formatCPU(totalCPU) }}</strong>
            </span>

            <span>
              Memory
              <strong class="ml-1 font-medium text-zinc-300">{{ formatBytes(totalMemory) }}</strong>
            </span>
          </div>

          <div v-if="activeTab !== 'storage'" class="search-wrap hidden md:block">
            <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="11" cy="11" r="6.5"/><path d="m16 16 4 4"/></svg>
            <input v-model="searchQuery" class="field w-64" :placeholder="`Search ${activeTab}…`" />
          </div>

          <button
            class="toolbar-button"
            :disabled="loading || !!resourceBusy || !!busyId || !!busyProject || !!composeBusy || importBusy"
            @click="loadCurrentTab"
          >
            {{ loading ? 'Loading…' : 'Refresh' }}
          </button>
          </div>
        </div>

        <div
          v-if="error"
          class="app-notice app-notice-error mb-4 flex items-start justify-between gap-4 rounded-lg border p-4 text-sm"
        >
          <span class="whitespace-pre-wrap break-all">{{ error }}</span>
          <button class="notice-close shrink-0" @click="error = ''">Close</button>
        </div>

        <div
          v-if="success"
          class="app-notice app-notice-success mb-4 rounded-lg border p-4 text-sm"
        >
          <div class="flex items-center justify-between gap-4">
            <span>{{ success }}</span>
            <button class="notice-close" @click="success = ''">Close</button>
          </div>

          <pre
            v-if="commandOutput"
            class="notice-output mt-3 max-h-52 overflow-auto whitespace-pre-wrap rounded-md p-3 font-mono text-xs leading-5"
          >{{ commandOutput }}</pre>
        </div>

        <!-- SMART CHECK -->
        <section v-if="activeTab === 'overview'" class="smart-care">
          <div class="smart-copy">
            <span class="eyebrow">SMART CHECK</span>
            <h3>{{ runtimeConnected ? 'Your workspace looks good.' : 'Let’s check your workspace.' }}</h3>
            <p>
              {{ runtimeConnected
                ? `${runningCount} active container${runningCount === 1 ? '' : 's'} are running on ${activeRuntime?.displayName || 'your runtime'}.`
                : 'Check the engine, containers, resources, and storage in one pass.'
              }}
            </p>
          </div>

          <div class="care-visual" :class="{ 'is-checking': smartCheckBusy }">
            <div class="care-halo halo-one" />
            <div class="care-halo halo-two" />
            <div class="scan-wave wave-one" />
            <div class="scan-wave wave-two" />
            <svg class="energy-streams" viewBox="0 0 500 280" aria-hidden="true">
              <defs>
                <linearGradient id="stream-a" x1="0" y1="0" x2="1" y2="1">
                  <stop offset="0" stop-color="#00e5ff" stop-opacity="0" />
                  <stop offset=".48" stop-color="#00e5ff" stop-opacity=".8" />
                  <stop offset="1" stop-color="#5ce1ef" stop-opacity="0" />
                </linearGradient>
                <linearGradient id="stream-b" x1="1" y1="0" x2="0" y2="1">
                  <stop offset="0" stop-color="#f374bd" stop-opacity="0" />
                  <stop offset=".52" stop-color="#29f3ff" stop-opacity=".65" />
                  <stop offset="1" stop-color="#6fdded" stop-opacity="0" />
                </linearGradient>
              </defs>
              <path class="stream stream-a" d="M35 185C126 41 208 253 292 89c51-99 111-31 174-74" />
              <path class="stream stream-b" d="M15 86c98 105 165-64 257 108 56 105 139 41 217-54" />
            </svg>
            <div class="care-orbit orbit-one"><i /></div>
            <div class="care-orbit orbit-two"><i /></div>
            <div class="care-particles" aria-hidden="true">
              <i v-for="particle in 10" :key="particle" :style="{ '--particle': particle }" />
            </div>
            <div class="care-core">
              <div class="core-glass glass-back" />
              <div class="core-glass glass-front" />
              <img class="core-mark" :src="brandSymbol" alt="" aria-hidden="true" />
            </div>
            <div class="orbit-chip chip-containers"><b>{{ containers.length }}</b><span>Containers</span></div>
            <div class="orbit-chip chip-projects"><b>{{ composeProjectCount }}</b><span>Projects</span></div>
            <div class="orbit-chip chip-runtime"><b :class="runtimeConnected ? 'text-emerald-400' : 'text-amber-300'">{{ runtimeConnected ? 'Ready' : 'Offline' }}</b><span>Runtime</span></div>
          </div>

          <div class="care-summary">
            <div><span class="summary-dot violet" /><p><b>Engine</b><small>{{ activeRuntime?.displayName || 'Not selected' }}</small></p></div>
            <div><span class="summary-dot cyan" /><p><b>Resources</b><small>{{ runtimeConnected ? `${formatCPU(totalCPU)} CPU · ${formatBytes(totalMemory)}` : 'Waiting for runtime' }}</small></p></div>
            <div><span class="summary-dot pink" /><p><b>Storage</b><small>{{ diskUsage.length ? `${diskUsage.length} areas analyzed` : 'Ready to analyze' }}</small></p></div>
          </div>

          <div v-if="migrationCheck?.available" class="migration-check-card">
            <b>Migration readiness</b>
            <span>{{ migrationCheck.images }} images · {{ migrationCheck.volumes }} volumes · {{ migrationCheck.containers }} containers</span>
            <small v-if="migrationCheck.running">Review required: {{ migrationCheck.running }} running container(s) must be stopped before volume migration.</small>
            <small v-else>Ready to review. Docker data remains unchanged until you start migration.</small>
          </div>

          <button class="check-button" :disabled="smartCheckBusy" @click="runSmartCheck">
            <span>{{ smartCheckBusy ? 'Checking…' : 'Check' }}</span>
          </button>
        </section>

        <div
          v-else-if="activeTab !== 'engine' && !runtimeConnected"
          class="offline-state rounded-xl p-10 text-center"
        >
          <div class="offline-icon mx-auto mb-4">
            <img :src="brandSymbol" alt="" aria-hidden="true" />
          </div>
          <div class="text-lg font-medium">Container runtime isn't available</div>
          <div class="mt-2 break-all text-sm text-zinc-500">
            {{ activeRuntime?.message || dockerStatus?.error || 'Open Engine settings to choose a runtime.' }}
          </div>
          <button class="primary-button mt-5" @click="activeTab = 'engine'">Open Engine Settings</button>
        </div>

        <!-- CONTAINERS -->
        <div v-else-if="activeTab === 'containers'" class="space-y-4">
          <section class="overview-grid">
            <div class="overview-card overview-runtime">
              <div class="metric-icon violet"><span class="status-orb" :class="runtimeConnected ? 'is-online' : 'is-offline'"><i /></span></div>
              <div>
                <div class="metric-label">Runtime</div>
                <div class="metric-value text-lg">{{ runtimeConnected ? 'All systems ready' : 'Needs attention' }}</div>
                <div class="metric-detail">{{ activeRuntime?.displayName || 'Container engine' }}</div>
              </div>
            </div>
            <div class="overview-card">
              <div class="metric-icon blue">↗</div>
              <div><div class="metric-label">Running</div><div class="metric-value">{{ runningCount }}</div><div class="metric-detail">of {{ containers.length }} containers</div></div>
            </div>
            <div class="overview-card">
              <div class="metric-icon pink">⌁</div>
              <div><div class="metric-label">CPU load</div><div class="metric-value">{{ formatCPU(totalCPU) }}</div><div class="metric-detail">live usage</div></div>
            </div>
            <div class="overview-card">
              <div class="metric-icon cyan">◇</div>
              <div><div class="metric-label">Memory</div><div class="metric-value">{{ formatBytes(totalMemory) }}</div><div class="metric-detail">active allocation</div></div>
            </div>
          </section>

          <section
            v-if="runtimeCapabilities.compose"
            id="compose-drop-zone"
            data-file-drop-target
            class="panel flex flex-wrap items-center justify-between gap-4 border-dashed p-4 transition"
          >
            <div>
              <div class="text-sm font-medium text-zinc-300">
                Import Docker Compose
              </div>
              <div class="mt-1 text-xs text-zinc-600">
                Drop compose.yaml here, or select a Compose file/project folder.
              </div>
            </div>

            <div class="flex gap-2">
              <button
                class="toolbar-button"
                :disabled="importBusy"
                @click="chooseComposeFile"
              >
                {{ importBusy ? 'Importing…' : 'Open Compose File' }}
              </button>

              <button
                class="toolbar-button"
                :disabled="importBusy"
                @click="chooseComposeFolder"
              >
                Open Folder
              </button>
            </div>
          </section>

          <section
            v-if="
              ['containerd', 'vmfree'].includes(activeRuntime?.provider ?? '') &&
              runtimeCapabilities.createContainer
            "
            class="panel p-4"
          >
            <div class="flex flex-wrap items-start justify-between gap-3">
              <div>
                <div class="text-sm font-medium text-zinc-300">
                  {{ activeRuntime?.provider === "vmfree" ? "Dockiva Engine" : "Direct containerd" }}
                </div>
                <div class="mt-1 text-xs leading-5 text-zinc-600">
                  <template v-if="activeRuntime?.provider === 'vmfree'">Runs ARM64 Linux images natively, with no VM. Command accepts a JSON array, for example [&quot;-c&quot;,&quot;busybox echo hello&quot;].</template>
                  <template v-else>Namespace <span class="font-mono text-zinc-400">dockiva</span>.
                  <template v-if="runtimeCapabilities.networks">
                    CNI bridge, host DNS, and localhost TCP publishing are ready.
                  </template>
                  <template v-else>
                    Networking helper is not ready. Run
                    <span class="font-mono text-zinc-400">scripts/install-containerd-networking.sh</span>.
                  </template>
                  </template>
                </div>
              </div>

              <span class="rounded bg-amber-500/10 px-2 py-1 text-[10px] text-amber-400">
                experimental
              </span>
            </div>

            <div v-if="activeRuntime?.provider === 'vmfree'" class="mt-4 space-y-2 text-xs text-zinc-400">
              <div class="flex gap-2">
                <button class="toolbar-button" @click="runtimeImageRef = 'dockiva/server:demo'; runtimeContainerName = 'web-demo'; runtimeCommand = '[&quot;18080&quot;]'; runtimePorts = ''; runtimeAutoStart = true">HTTP server demo</button>
                <button class="toolbar-button" @click="runtimeImageRef = 'dockiva/redis:preview'; runtimeContainerName = 'redis-preview'; runtimeCommand = ''; runtimePorts = ''; runtimeAutoStart = true">Redis preview</button>
                <button class="toolbar-button" @click="runtimeImageRef = 'dockiva/shell:demo'; runtimeContainerName = 'hello-test'; runtimeCommand = '[&quot;-c&quot;,&quot;busybox echo hello&quot;]'; runtimePorts = ''; runtimeAutoStart = true">Hello demo</button>
              </div>
              <p>The HTTP demo stays running: create it, then open http://127.0.0.1:18080/ in your browser. Leave port mappings empty. If that port is occupied, change the command to another port such as [&quot;18081&quot;].</p>
              <p>Redis preview listens on 127.0.0.1:16379. Leave Command and port mappings empty. Its private data survives Stop/Start and Restart; Remove deletes it. One preview per port. Experimental prepared Redis 7.4.2 only; PostgreSQL and Valkey are not qualified.</p>
              <p>The Hello demo finishes immediately. Exited with code 0 means success. Both demos remain subject to the runtime's execution limits.</p>
            </div>

            <div class="mt-4 grid gap-2 xl:grid-cols-[1.25fr_1fr_1.25fr_1fr_auto_auto]">
              <input
                v-model="runtimeImageRef"
                class="field"
                placeholder="Image, e.g. alpine:latest"
              />

              <input
                v-model="runtimeContainerName"
                class="field"
                placeholder="Container name"
              />

              <input
                v-model="runtimeCommand"
                class="field"
                placeholder="Optional command, e.g. sleep 3600"
              />

              <input
                v-model="runtimePorts"
                class="field"
                :disabled="!runtimeCapabilities.portPublishing"
                placeholder="Ports, e.g. 8080:80,8443:443"
              />

              <button
                class="toolbar-button"
                :disabled="!runtimeImageRef.trim() || !!runtimeOperationBusy"
                v-if="runtimeCapabilities.pullImages"
                @click="runtimePullImage"
              >
                {{ runtimeOperationBusy === 'pull' ? 'Pulling…' : 'Pull Image' }}
              </button>

              <button
                class="primary-button"
                :disabled="
                  !runtimeImageRef.trim() ||
                  !runtimeContainerName.trim() ||
                  !!runtimeOperationBusy
                "
                @click="runtimeCreateContainer"
              >
                {{ runtimeOperationBusy === 'create' ? 'Creating…' : 'Create' }}
              </button>
            </div>

            <label class="mt-3 inline-flex items-center gap-2 text-xs text-zinc-500">
              <input v-model="runtimeAutoStart" type="checkbox" />
              Start task immediately after creating the container
            </label>
          </section>

          <section v-for="group in filteredGroups" :key="group.key" class="container-group">
            <div class="group-header flex items-center justify-between gap-4 px-5 py-4">
              <button
                class="flex min-w-0 flex-1 items-center gap-3 text-left"
                @click="toggleGroup(group.key)"
              >
                <div class="group-icon" :class="group.compose ? 'compose' : 'standalone'">
                  <svg v-if="group.compose" viewBox="0 0 24 24"><path d="m12 3 7.5 4.2v8.6L12 20l-7.5-4.2V7.2L12 3Z M8 9.5l4 2.2 4-2.2M12 11.7v4.7" /></svg>
                  <svg v-else viewBox="0 0 24 24"><rect x="4" y="5" width="16" height="14" rx="3"/><path d="M8 10h8M8 14h5"/></svg>
                </div>

                <div class="min-w-0">
                  <div class="flex items-center gap-2">
                    <div class="truncate font-semibold">{{ group.name }}</div>
                    <span class="group-count">{{ group.containers.length }}</span>
                  </div>
                  <div class="mt-1 flex items-center gap-1.5 text-[11px] text-zinc-500">
                    <span class="mini-status" :class="group.running ? 'online' : ''" />
                    {{ group.running }} running · {{ group.compose ? 'Compose project' : 'Standalone' }}
                  </div>
                </div>
                <svg class="group-chevron" :class="{ collapsed: collapsedGroups[group.key] }" viewBox="0 0 24 24"><path d="m8 10 4 4 4-4"/></svg>
              </button>

              <div class="group-actions flex shrink-0 flex-wrap items-center justify-end gap-1.5">
                <template v-if="group.compose">
                  <button
                    class="compose-button primary-quiet text-emerald-300"
                    :disabled="!!composeBusy || !!busyProject || !!busyId"
                    @click="runCompose(group, 'up')"
                  >
                    {{ composeBusy === `${group.name}:up` ? 'Running…' : 'Up' }}
                  </button>

                  <button
                    class="compose-button text-violet-300"
                    :disabled="!!composeBusy || !!busyProject || !!busyId"
                    @click="runCompose(group, 'build')"
                  >
                    {{ composeBusy === `${group.name}:build` ? 'Building…' : 'Build' }}
                  </button>

                  <button
                    class="compose-button text-sky-300"
                    :disabled="!!composeBusy || !!busyProject || !!busyId"
                    @click="runCompose(group, 'rebuild')"
                  >
                    {{ composeBusy === `${group.name}:rebuild` ? 'Rebuilding…' : 'Rebuild' }}
                  </button>

                  <button
                    class="compose-button text-red-300"
                    :disabled="!!composeBusy || !!busyProject || !!busyId"
                    @click="runCompose(group, 'down')"
                  >
                    {{ composeBusy === `${group.name}:down` ? 'Stopping…' : 'Down' }}
                  </button>
                </template>

                <template v-if="group.compose">
                  <span class="mx-1 h-5 w-px bg-white/[0.07]" />

                  <button
                    v-if="group.running < group.containers.length"
                    class="project-button text-emerald-400"
                    :disabled="!!busyProject || !!busyId || !!composeBusy"
                    @click="runProjectAction(group, 'start')"
                  >
                    {{ isProjectBusy(group, 'start') ? 'Starting…' : 'Start All' }}
                  </button>

                  <button
                    v-if="group.running > 0"
                    class="project-button text-amber-400"
                    :disabled="!!busyProject || !!busyId || !!composeBusy"
                    @click="runProjectAction(group, 'stop')"
                  >
                    {{ isProjectBusy(group, 'stop') ? 'Stopping…' : 'Stop All' }}
                  </button>
                </template>
              </div>
            </div>

            <Transition name="group-reveal">
              <div v-if="!collapsedGroups[group.key]" class="container-list">
                <article v-for="container in group.containers" :key="container.id" class="container-row">
                  <div class="container-identity">
                    <div class="container-avatar" :class="container.state === 'running' ? 'running' : ''">
                      <span /><span /><span />
                    </div>
                    <div class="min-w-0">
                      <div class="flex items-center gap-2">
                        <strong class="truncate text-sm">{{ group.compose ? (container.composeService || container.name) : container.name }}</strong>
                        <span class="state-badge" :class="container.state === 'running' ? 'running' : 'stopped'">{{ container.state }}</span>
                      </div>
                      <div class="mt-1 truncate text-[11px] text-zinc-500">{{ container.image }}</div>
                      <div class="mt-1 font-mono text-[9px] text-zinc-700">{{ container.shortId }}<template v-if="container.ipAddress"> · {{ container.ipAddress }}</template></div>
                    </div>
                  </div>

                  <div class="resource-cluster">
                    <div class="resource-item">
                      <div><span>CPU</span><b>{{ container.state === 'running' ? formatCPU(statFor(container)?.cpuPercent) : '—' }}</b></div>
                      <div class="resource-track"><i class="cpu" :style="{ width: `${Math.min(100, statFor(container)?.cpuPercent || 0)}%` }" /></div>
                    </div>
                    <div class="resource-item">
                      <div><span>Memory</span><b>{{ container.state === 'running' ? formatBytes(statFor(container)?.memoryUsage || 0) : '—' }}</b></div>
                      <div class="resource-track"><i class="memory" :style="{ width: `${Math.min(100, statFor(container)?.memoryPercent || 0)}%` }" /></div>
                    </div>
                  </div>

                  <div class="container-ports">
                    <template v-if="container.ports?.length">
                      <template v-for="port in container.ports.slice(0, 3)" :key="`${port.privatePort}-${port.publicPort}-${port.type}`">
                        <button v-if="port.url" class="port-link" @click.stop="openURL(port.url)">{{ port.display }} ↗</button>
                        <span v-else class="port-label">{{ port.display }}</span>
                      </template>
                      <span v-if="container.ports.length > 3" class="more-ports">+{{ container.ports.length - 3 }}</span>
                    </template>
                    <span v-else class="text-[11px] text-zinc-700">No published ports</span>
                  </div>

                  <div class="container-actions">
                        <button
                          v-if="isDockerRuntime"
                          class="action-button"
                          @click="detailsContainer = container"
                        >
                          Details
                        </button>

                        <button
                          v-if="
                            container.state === 'running' &&
                            runtimeCapabilities.terminal
                          "
                          class="action-button text-violet-300"
                          @click="terminalContainer = container"
                        >
                          Terminal
                        </button>

                        <button
                          v-if="runtimeCapabilities.logs"
                          class="action-button"
                          @click="openLogs(container)"
                        >
                          Logs
                        </button>

                        <button
                          v-if="container.state !== 'running'"
                          class="action-button action-primary text-emerald-300"
                          :disabled="!!busyId || !!busyProject || !!composeBusy"
                          @click="runContainerAction(container, 'start')"
                        >
                          {{ isBusy(container, 'start') ? 'Starting…' : 'Start' }}
                        </button>

                        <button
                          v-if="container.state === 'running'"
                          class="action-button text-amber-300"
                          :disabled="!!busyId || !!busyProject || !!composeBusy"
                          @click="runContainerAction(container, 'stop')"
                        >
                          {{ isBusy(container, 'stop') ? 'Stopping…' : 'Stop' }}
                        </button>

                        <button
                          v-if="container.state === 'running'"
                          class="action-button text-sky-300"
                          :disabled="!!busyId || !!busyProject || !!composeBusy"
                          @click="runContainerAction(container, 'restart')"
                        >
                          {{ isBusy(container, 'restart') ? 'Restarting…' : 'Restart' }}
                        </button>

                        <button
                          class="action-button action-danger text-red-300"
                          :disabled="!!busyId || !!busyProject || !!composeBusy"
                          @click="runContainerAction(container, 'delete')"
                        >
                          {{ isBusy(container, 'delete') ? 'Deleting…' : 'Delete' }}
                        </button>
                  </div>
                </article>
              </div>
            </Transition>
          </section>

          <div
            v-if="!loading && !filteredGroups.length"
            class="empty-state container-empty"
          >
            <div class="empty-stack" aria-hidden="true"><span /><span /><span /></div>
            <h3>{{ searchQuery ? 'No matching containers' : 'Your workspace is clear' }}</h3>
            <p>{{ searchQuery ? 'Try another name, image, state, or port.' : 'Import a Compose project or pull an image to start building.' }}</p>
            <div v-if="!searchQuery" class="mt-5 flex justify-center gap-2">
              <button v-if="runtimeCapabilities.compose" class="primary-button" @click="chooseComposeFile">Import Compose</button>
              <button v-if="isDockerRuntime" class="toolbar-button" @click="activeTab = 'images'">Browse Images</button>
            </div>
          </div>
        </div>

        <!-- IMAGES -->
        <div v-else-if="activeTab === 'images'" class="space-y-4">
          <form v-if="activeRuntime?.provider !== 'vmfree' || runtimeCapabilities.pullImages" class="panel flex flex-wrap items-center gap-2 p-4" @submit.prevent="pullImage">
            <input
              v-model="pullImageRef"
              class="field min-w-64 flex-1"
              placeholder="Image, e.g. postgres:17 or ghcr.io/org/app:latest"
            />
            <button
              class="primary-button"
              :disabled="!pullImageRef.trim() || !!resourceBusy"
            >
              {{ resourceBusy === 'pull-image' ? 'Pulling…' : 'Pull Image' }}
            </button>
          </form>

          <div class="panel overflow-hidden">
            <table class="w-full">
              <thead class="border-b border-zinc-800 bg-zinc-950/30">
                <tr class="table-head">
                  <th class="px-4 py-3">REPOSITORY / TAG</th>
                  <th class="px-4 py-3">SIZE</th>
                  <th class="px-4 py-3">CREATED</th>
                  <th class="px-4 py-3">ID</th>
                  <th class="px-4 py-3 text-right">ACTIONS</th>
                </tr>
              </thead>
              <tbody>
                <tr
                  v-for="image in filteredImages"
                  :key="image.id"
                  class="border-b border-zinc-800/70 last:border-none"
                >
                  <td class="px-4 py-4">
                    <div v-for="tag in image.tags" :key="tag" class="font-mono text-sm">
                      {{ tag }}
                    </div>
                  </td>
                  <td class="px-4 py-4 text-sm text-zinc-400">{{ formatBytes(image.size) }}</td>
                  <td class="px-4 py-4 text-sm text-zinc-500">{{ formatDate(image.created) }}</td>
                  <td class="px-4 py-4 font-mono text-xs text-zinc-600">{{ image.shortId }}</td>
                  <td class="px-4 py-4 text-right">
                    <button v-if="activeRuntime?.provider !== 'vmfree'" class="action-button text-red-400" @click="removeImage(image)">
                      Delete
                    </button>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>

        <!-- VOLUMES -->
        <div v-else-if="activeTab === 'volumes'" class="space-y-4">
          <form class="panel flex flex-wrap items-center gap-2 p-4" @submit.prevent="createVolume">
            <input v-model="newVolumeName" class="field min-w-56 flex-1" placeholder="Volume name" />
            <select v-model="newVolumeDriver" class="field">
              <option value="local">local</option>
            </select>
            <button class="primary-button" :disabled="!newVolumeName.trim() || !!resourceBusy">
              {{ resourceBusy === 'create-volume' ? 'Creating…' : 'Create Volume' }}
            </button>
          </form>

          <div class="panel overflow-hidden">
            <table class="w-full">
              <thead class="border-b border-zinc-800 bg-zinc-950/30">
                <tr class="table-head">
                  <th class="px-4 py-3">NAME</th>
                  <th class="px-4 py-3">DRIVER</th>
                  <th class="px-4 py-3">SCOPE</th>
                  <th class="px-4 py-3">MOUNTPOINT</th>
                  <th class="px-4 py-3 text-right">ACTIONS</th>
                </tr>
              </thead>
              <tbody>
                <tr
                  v-for="volume in filteredVolumes"
                  :key="volume.name"
                  class="border-b border-zinc-800/70 last:border-none"
                >
                  <td class="px-4 py-4 font-mono text-sm">{{ volume.name }}</td>
                  <td class="px-4 py-4 text-sm text-zinc-400">{{ volume.driver }}</td>
                  <td class="px-4 py-4 text-sm text-zinc-500">{{ volume.scope }}</td>
                  <td class="max-w-[480px] truncate px-4 py-4 font-mono text-xs text-zinc-600">
                    {{ volume.mountpoint }}
                  </td>
                  <td class="px-4 py-4 text-right">
                    <button class="action-button text-red-400" @click="removeVolume(volume)">
                      Delete
                    </button>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>

        <!-- NETWORKS -->
        <div v-else-if="activeTab === 'networks'" class="space-y-4">
          <div v-if="!isDockerRuntime" class="panel border border-cyan-400/25 bg-cyan-950/40 p-4 text-sm leading-6 text-cyan-100">
            Native containers use Dockiva's managed <code class="font-mono text-cyan-200">dockiva-net</code> CNI bridge. It provides container-to-container connectivity and localhost port publishing. Custom network creation is not available yet.
          </div>
          <form v-else class="panel flex flex-wrap items-center gap-2 p-4" @submit.prevent="createNetwork">
            <input v-model="newNetworkName" class="field min-w-56 flex-1" placeholder="Network name" />
            <select v-model="newNetworkDriver" class="field">
              <option value="bridge">bridge</option>
              <option value="macvlan">macvlan</option>
              <option value="ipvlan">ipvlan</option>
              <option value="overlay">overlay</option>
            </select>
            <button class="primary-button" :disabled="!newNetworkName.trim() || !!resourceBusy">
              {{ resourceBusy === 'create-network' ? 'Creating…' : 'Create Network' }}
            </button>
          </form>

          <div class="panel overflow-hidden">
            <table class="w-full">
              <thead class="border-b border-zinc-800 bg-zinc-950/30">
                <tr class="table-head">
                  <th class="px-4 py-3">NAME</th>
                  <th class="px-4 py-3">DRIVER</th>
                  <th class="px-4 py-3">SCOPE</th>
                  <th class="px-4 py-3">FLAGS</th>
                  <th class="px-4 py-3">ID</th>
                  <th class="px-4 py-3 text-right">ACTIONS</th>
                </tr>
              </thead>
              <tbody>
                <tr
                  v-for="network in filteredNetworks"
                  :key="network.id"
                  class="border-b border-zinc-800/70 last:border-none"
                >
                  <td class="px-4 py-4 font-medium">{{ network.name }}</td>
                  <td class="px-4 py-4 text-sm text-zinc-400">{{ network.driver }}</td>
                  <td class="px-4 py-4 text-sm text-zinc-500">{{ network.scope }}</td>
                  <td class="px-4 py-4 text-xs text-zinc-500">
                    {{ network.internal ? 'internal ' : '' }}
                    {{ network.attachable ? 'attachable ' : '' }}
                    {{ network.ingress ? 'ingress' : '' }}
                    {{ isDefaultNetwork(network) ? 'system' : '' }}
                  </td>
                  <td class="px-4 py-4 font-mono text-xs text-zinc-600">{{ network.shortId }}</td>
                  <td class="px-4 py-4 text-right">
                    <button
                      v-if="!isDefaultNetwork(network)"
                      class="action-button text-red-400"
                      @click="removeNetwork(network)"
                    >
                      Delete
                    </button>
                    <span v-else class="text-xs text-zinc-700">Protected</span>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>

        <!-- STORAGE -->
        <div v-else-if="activeTab === 'storage'" class="storage-page space-y-5">
          <section class="storage-hero">
            <div class="storage-hero-icon" aria-hidden="true">
              <svg viewBox="0 0 24 24"><path d="M4 5.5h16v13H4z" /><path d="M8 15h.01M12 15h4" /></svg>
            </div>
            <div>
              <span class="storage-eyebrow">WORKSPACE STORAGE</span>
              <h3>Keep local container data under control.</h3>
              <p>Review Docker disk usage and remove only resources you no longer need.</p>
            </div>
            <span class="safe-badge"><i />Conservative cleanup</span>
          </section>

          <section v-if="hostContainerStorage.length" class="host-storage-panel">
            <div class="host-storage-heading">
              <div>
                <span class="storage-eyebrow">STORAGE ON THIS MAC</span>
                <h3>Container data folders</h3>
              </div>
              <p>Review large container folders, clean unused resources, or reveal their location in Finder.</p>
            </div>

            <div class="host-storage-list">
              <article v-for="item in hostContainerStorage" :key="item.id" class="host-storage-row">
                <div class="host-storage-folder" aria-hidden="true">
                  <svg viewBox="0 0 24 24"><path d="M3.5 7.5h6l1.7 2H20.5v9.5h-17z" /><path d="M3.5 7.5V5h6l1.7 2" /></svg>
                </div>
                <div class="host-storage-copy">
                  <h4>{{ item.name }}</h4>
                  <p>{{ item.description }}</p>
                  <button class="host-storage-path" :title="item.path" @click="revealHostContainerStorage(item)">
                    {{ item.path }}
                  </button>
                </div>
                <strong class="host-storage-size">{{ item.size }}</strong>
                <div class="host-storage-actions">
                  <button
                    class="storage-clean-button"
                    :disabled="!item.canClean || !!resourceBusy"
                    :title="item.canClean ? 'Remove conservative unused resources from this engine' : 'Start this engine to clean its unused data'"
                    @click="reviewHostContainerStorage(item)"
                  >
                    {{ resourceBusy === `host-storage:${item.id}` ? 'Cleaning…' : item.cleanupLabel }}
                  </button>
                  <button class="storage-reveal-button" :disabled="!item.canReveal" @click="revealHostContainerStorage(item)">
                    Reveal
                  </button>
                </div>
              </article>
            </div>
          </section>

          <div v-if="diskUsage.length" class="storage-grid">
            <article
              v-for="item in diskUsage"
              :key="item.type"
              class="storage-card"
            >
              <div class="storage-card-head">
                <span class="storage-type-icon" aria-hidden="true">{{ item.type.slice(0, 1).toUpperCase() }}</span>
                <div>
                  <div class="storage-type">{{ item.type }}</div>
                  <div class="storage-count">{{ item.totalCount }} total</div>
                </div>
              </div>
              <div class="storage-size">{{ item.size }}</div>
              <div class="storage-track" :title="`${item.active} of ${item.totalCount} active`">
                <i :style="{ width: `${activeResourcePercent(item)}%` }" />
              </div>
              <div class="storage-meta">
                <span><b>{{ item.active }}</b> active</span>
                <span><b>{{ item.reclaimable }}</b> reclaimable</span>
              </div>
            </article>
          </div>

          <div v-else class="storage-empty">
            <div class="empty-stack"><span /><span /><span /></div>
            <h3>No storage data available</h3>
            <p>Refresh after connecting to a Docker runtime to analyze local disk usage.</p>
          </div>

          <section class="cleanup-panel">
            <div class="cleanup-heading">
              <div>
                <span class="storage-eyebrow">CLEANUP TOOLS</span>
                <h3>Choose what to remove</h3>
              </div>
              <p>
                Conservative prune actions. Named volumes and unused tagged images are not deleted automatically.
              </p>
            </div>

            <div class="cleanup-grid">
              <button class="cleanup-action cleanup-danger" :disabled="!!resourceBusy" @click="prune('system')">
                <span class="cleanup-icon">✦</span><span><b>{{ resourceBusy === 'prune:system' ? 'Cleaning…' : 'Prune System' }}</b><small>Containers, networks, dangling images, and build cache</small></span>
              </button>
              <button class="cleanup-action" :disabled="!!resourceBusy" @click="prune('containers')">
                <span class="cleanup-icon">□</span><span><b>{{ resourceBusy === 'prune:containers' ? 'Cleaning…' : 'Stopped Containers' }}</b><small>Remove containers that are no longer running</small></span>
              </button>
              <button class="cleanup-action" :disabled="!!resourceBusy" @click="prune('images')">
                <span class="cleanup-icon">◇</span><span><b>{{ resourceBusy === 'prune:images' ? 'Cleaning…' : 'Dangling Images' }}</b><small>Remove untagged image layers only</small></span>
              </button>
              <button class="cleanup-action" :disabled="!!resourceBusy" @click="prune('networks')">
                <span class="cleanup-icon">⌁</span><span><b>{{ resourceBusy === 'prune:networks' ? 'Cleaning…' : 'Unused Networks' }}</b><small>Remove networks with no attached containers</small></span>
              </button>
              <button class="cleanup-action" :disabled="!!resourceBusy" @click="prune('volumes')">
                <span class="cleanup-icon">▱</span><span><b>{{ resourceBusy === 'prune:volumes' ? 'Cleaning…' : 'Anonymous Volumes' }}</b><small>Remove unused anonymous volumes</small></span>
              </button>
            </div>
          </section>
        </div>

        <EngineSettingsPanel
          v-else-if="activeTab === 'engine'"
          :platform="platformInfo"
          :migration="dockerMigrationPreview"
          :migration-source-busy="migrationSourceBusy"
          @engine-changed="loadCurrentTab"
          @review-migration="migrationPromptOpen = true"
          @start-migration-source="startDockerMigrationSource"
          @scan-migration-source="scanDockerMigrationSource"
        />
      </main>
    </div>

    <div
      v-if="storageCleanupTarget"
      class="storage-cleanup-backdrop"
      @click.self="closeStorageCleanup"
    >
      <section class="storage-cleanup-sheet">
        <header>
          <div>
            <span class="storage-eyebrow">RECLAIM SPACE</span>
            <h3>{{ storageCleanupTarget.name }}</h3>
            <p>{{ storageCleanupTarget.size }} currently allocated on this Mac</p>
          </div>
          <button class="storage-cleanup-close" :disabled="!!resourceBusy" @click="closeStorageCleanup">×</button>
        </header>

        <p v-if="!storageCleanupTarget.engineRunning" class="storage-engine-note">
          Docker Desktop is stopped. Dockiva will start it securely before analyzing and cleaning this data.
        </p>

        <div class="storage-cleanup-options">
          <button
            :class="{ selected: storageCleanupMode === 'cache' }"
            :aria-pressed="storageCleanupMode === 'cache'"
            :disabled="!!resourceBusy"
            @click="selectHostCleanupMode('cache')"
          >
            <span class="cleanup-option-icon">◇</span>
            <span><b>Build cache only</b><small>Remove cached build layers. Containers, images, and volumes stay intact.</small></span>
            <em>Safest</em>
          </button>
          <button
            :class="{ selected: storageCleanupMode === 'safe' }"
            :aria-pressed="storageCleanupMode === 'safe'"
            :disabled="!!resourceBusy"
            @click="selectHostCleanupMode('safe')"
          >
            <span class="cleanup-option-icon">✦</span>
            <span><b>Clean unused resources</b><small>Stopped containers, unused networks, dangling images, and build cache.</small></span>
            <em>Recommended</em>
          </button>
          <button
            class="deep"
            :class="{ selected: storageCleanupMode === 'deep' }"
            :aria-pressed="storageCleanupMode === 'deep'"
            :disabled="!!resourceBusy"
            @click="selectHostCleanupMode('deep')"
          >
            <span class="cleanup-option-icon">△</span>
            <span><b>Deep cleanup</b><small>Also remove every unused image and anonymous volume. Named volumes remain.</small></span>
            <em>More space</em>
          </button>
        </div>

        <div v-if="storageCleanupMode" class="storage-cleanup-confirm">
          <div>
            <strong>{{ cleanupModeLabel(storageCleanupMode) }}</strong>
            <p v-if="storageCleanupMode === 'deep'">Unused tagged images may need to be downloaded again. Named volumes remain protected.</p>
            <p v-else>Running containers, named volumes, and tagged images remain protected.</p>
            <p v-if="storageCleanupError" class="storage-cleanup-error">{{ storageCleanupError }}</p>
          </div>
          <button
            class="storage-cleanup-run"
            :disabled="!!resourceBusy"
            @click="cleanHostContainerStorage(storageCleanupTarget)"
          >
            {{ resourceBusy
              ? (storageCleanupTarget.engineRunning ? 'Cleaning…' : 'Starting Docker Desktop…')
              : cleanupModeLabel(storageCleanupMode) }}
          </button>
        </div>

        <footer>
          Docker’s disk image may take a short time to return reclaimed blocks to macOS after cleanup.
        </footer>
      </section>
    </div>

    <!-- LIVE LOGS -->
    <div
      v-if="logsOpen"
      class="fixed inset-0 z-[60] flex items-center justify-center bg-black/80 p-5"
      @click.self="closeLogs"
    >
      <section class="flex h-[86vh] w-full max-w-7xl flex-col overflow-hidden rounded-xl border border-zinc-700 bg-zinc-950">
        <header class="flex items-center justify-between border-b border-zinc-800 px-5 py-4">
          <div>
            <div class="flex items-center gap-2">
              <h3 class="font-semibold">{{ logsContainer?.name }}</h3>
              <span
                class="rounded-full px-2 py-0.5 text-[10px]"
                :class="logsFollowing ? 'bg-emerald-500/10 text-emerald-400' : 'bg-zinc-800 text-zinc-500'"
              >
                {{ logsFollowing ? 'LIVE' : 'CLOSED' }}
              </span>
            </div>
            <p class="mt-0.5 text-xs text-zinc-500">Streaming Docker logs</p>
          </div>

          <div class="flex items-center gap-2">
            <select v-model.number="logTail" class="field" @change="restartLogs">
              <option :value="100">100 lines</option>
              <option :value="200">200 lines</option>
              <option :value="500">500 lines</option>
              <option :value="1000">1000 lines</option>
              <option :value="5000">5000 lines</option>
            </select>
            <button class="toolbar-button" @click="restartLogs">Reconnect</button>
            <button class="toolbar-button" @click="closeLogs">Close</button>
          </div>
        </header>

        <pre
          ref="logsViewport"
          class="flex-1 overflow-auto whitespace-pre-wrap break-words bg-black p-5 font-mono text-xs leading-5 text-zinc-300"
        >{{ logsText || 'Waiting for log output…' }}</pre>
      </section>
    </div>

    <XtermTerminal
      v-if="terminalContainer"
      :container="terminalContainer"
      @close="terminalContainer = null"
    />

    <ContainerDetailsModal
      v-if="detailsContainer"
      :container="detailsContainer"
      @close="detailsContainer = null"
    />

    <div v-if="migrationPromptOpen && dockerMigrationPreview" class="migration-backdrop fixed inset-0 z-[80] flex items-center justify-center p-5">
      <section class="migration-dialog w-full max-w-xl rounded-2xl p-6 shadow-2xl">
        <p class="migration-eyebrow">DOCKIVA NATIVE</p>
        <h2 class="migration-title mt-2">Migrate your Docker workspace?</h2>
        <p class="migration-copy mt-2">{{ dockerMigrationPreview.message }}</p>
        <div class="mt-5 grid grid-cols-3 gap-3 text-center">
          <div class="migration-stat p-3"><b>{{ dockerMigrationPreview.containers }}</b><span>containers</span></div>
          <div class="migration-stat p-3"><b>{{ dockerMigrationPreview.images }}</b><span>images</span></div>
          <div class="migration-stat p-3"><b>{{ dockerMigrationPreview.volumes }}</b><span>volumes</span></div>
        </div>
        <p v-if="dockerMigrationPreview.running" class="migration-warning mt-4">{{ dockerMigrationPreview.running }} container(s) are running. Dockiva will ask to stop them before copying volume data.</p>
        <div v-if="migrationBusy || volumeMigrationBusy" class="migration-progress mt-4" aria-live="polite">
          <div class="flex items-center justify-between gap-3">
            <span>{{ migrationProgressStatus.message }}</span>
            <b v-if="migrationProgressStatus.totalImages">{{ migrationProgressStatus.importedImages || 0 }}/{{ migrationProgressStatus.totalImages }}</b>
          </div>
          <div class="migration-progress-track mt-2" role="progressbar" :aria-valuenow="migrationProgressStatus.importedImages || 0" aria-valuemin="0" :aria-valuemax="migrationProgressStatus.totalImages || 0">
            <span :style="{ width: migrationProgressStatus.totalImages ? `${Math.round(((migrationProgressStatus.importedImages || 0) / migrationProgressStatus.totalImages) * 100)}%` : '4%' }" />
          </div>
          <code v-if="migrationProgressStatus.currentImage" class="migration-current-image mt-2">{{ migrationProgressStatus.currentImage }}</code>
        </div>
        <div class="mt-6 flex justify-end gap-2">
          <button class="toolbar-button" @click="dismissMigrationPrompt">Not now</button>
          <button class="toolbar-button" :disabled="migrationBusy || !dockerMigrationPreview.containers" @click="migrateDockerContainers">{{ migrationBusy ? 'Migrating containers…' : 'Migrate containers now' }}</button>
          <button class="toolbar-button" :disabled="migrationBusy || !dockerMigrationPreview.images" @click="migrateDockerImages">{{ migrationBusy ? 'Migrating images…' : 'Migrate images now' }}</button>
          <button class="toolbar-button" :disabled="volumeMigrationBusy || !dockerMigrationPreview.volumes || !dockerMigrationPreview.volumeMigrationSupported" @click="migrateDockerVolumes">{{ volumeMigrationBusy ? 'Migrating volumes…' : dockerMigrationPreview.volumeMigrationSupported ? 'Migrate volumes now' : 'Volumes: Linux only' }}</button>
          <button class="primary-button" @click="activeTab = 'engine'; migrationPromptOpen = false">Review migration</button>
        </div>
      </section>
    </div>
  </div>
</template>

<style scoped>
.app-shell {
  position: relative;
  overflow-x: hidden;
  background:
    radial-gradient(circle at 82% 8%, rgb(0 229 255 / .12), transparent 28rem),
    linear-gradient(145deg, #020817 0%, #03132e 58%, #020817 100%);
}

.window-titlebar {
  background:
    radial-gradient(circle at 78% -120%, rgb(41 243 255 / .42), transparent 22rem),
    linear-gradient(100deg, #03132e 0%, #063e9b 48%, #0a68ff 100%);
  box-shadow: inset 0 1px rgb(255 255 255 / .1);
  --wails-draggable: drag;
  user-select: none;
}
.window-titlebar::after { content:''; position:absolute; top:100%; right:0; left:0; height:2.5rem; background:linear-gradient(to bottom,rgb(10 104 255/.1),transparent); pointer-events:none; }
.window-titlebar-glow { position:absolute; inset:-2rem 10% auto; height:5rem; background:linear-gradient(90deg,transparent,rgb(0 229 255/.2),rgb(41 243 255/.14),transparent); filter:blur(18px); pointer-events:none; }
.window-title { position:absolute; inset:0; display:grid; place-items:center; color:rgb(255 255 255/.8); font-size:.78rem; font-weight:600; letter-spacing:-.01em; text-shadow:0 1px 8px rgb(16 3 38/.42); pointer-events:none; }
.window-title-actions { position:absolute; top:0; right:.85rem; bottom:0; display:flex; align-items:center; --wails-draggable:no-drag; }
.titlebar-theme-toggle { display:grid; width:2rem; height:2rem; place-items:center; border:1px solid rgb(255 255 255/.12); border-radius:.62rem; background:rgb(255 255 255/.08); color:rgb(255 255 255/.72); transition:160ms ease; --wails-draggable:no-drag; }
.titlebar-theme-toggle:hover { border-color:rgb(255 255 255/.22); background:rgb(255 255 255/.14); color:white; transform:translateY(-1px); }
.titlebar-theme-toggle svg { width:.9rem; height:.9rem; fill:none; stroke:currentColor; stroke-width:1.8; stroke-linecap:round; stroke-linejoin:round; }

.ambient {
  position: fixed;
  z-index: 0;
  width: 34rem;
  height: 34rem;
  border-radius: 999px;
  filter: blur(120px);
  opacity: 0.09;
  pointer-events: none;
}

.ambient-one { top: -18rem; right: -8rem; background: #0a68ff; }
.ambient-two { bottom: -24rem; left: 30%; background: #00e5ff; }

.migration-backdrop { background:rgb(2 6 23/.7); backdrop-filter:blur(8px); }
.migration-dialog { border:1px solid rgb(34 211 238/.28); background:#030712; box-shadow:0 28px 80px rgb(0 0 0/.55),inset 0 1px rgb(255 255 255/.06); }
.migration-eyebrow { color:#67e8f9 !important; font-size:.72rem; font-weight:700; letter-spacing:.18em; }
.migration-title { color:#f8fafc !important; font-size:1.25rem; font-weight:700; }
.migration-copy { color:#cbd5e1 !important; font-size:.88rem; line-height:1.55; }
.migration-stat { border:1px solid rgb(255 255 255/.13); border-radius:.75rem; background:rgb(255 255 255/.035); }
.migration-stat b { display:block; color:#f8fafc !important; font-size:1.25rem; font-weight:700; }
.migration-stat span { color:#94a3b8 !important; font-size:.75rem; }
.migration-warning { color:#fcd34d !important; font-size:.78rem; line-height:1.55; }
.migration-progress { color:#67e8f9 !important; font-size:.78rem; line-height:1.55; }
.migration-progress b { color:#e0f2fe !important; font-variant-numeric:tabular-nums; }
.migration-progress-track { height:.4rem; overflow:hidden; border:1px solid rgb(103 232 249/.22); border-radius:999px; background:rgb(255 255 255/.06); }
.migration-progress-track span { display:block; height:100%; min-width:.4rem; border-radius:inherit; background:linear-gradient(90deg,#22d3ee,#3b82f6); transition:width .25s ease; }
.migration-current-image { display:block; overflow:hidden; color:#94a3b8 !important; font-size:.7rem; text-overflow:ellipsis; white-space:nowrap; }
.migration-check-card { margin-top:1rem; border:1px solid rgb(34 211 238/.2); border-radius:.75rem; padding:.8rem 1rem; background:rgb(8 47 73/.22); color:#bae6fd; }
.migration-check-card b,.migration-check-card span,.migration-check-card small { display:block; }
.migration-check-card span { margin-top:.2rem; color:#e0f2fe; font-size:.82rem; }
.migration-check-card small { margin-top:.3rem; color:#fcd34d; font-size:.75rem; }

.sidebar {
  width: 16rem;
  border-right: 1px solid rgb(255 255 255 / 0.055);
  background: rgb(12 13 21 / 0.86);
  box-shadow: 18px 0 50px rgb(0 0 0 / 0.12);
  backdrop-filter: blur(28px) saturate(135%);
  transition: width 220ms cubic-bezier(.2,.8,.2,1), box-shadow 220ms ease;
}

.app-content { margin-left:16rem; transition:margin-left 220ms cubic-bezier(.2,.8,.2,1); }

.sidebar-toggle {
  position:absolute;
  z-index:3;
  top:1.15rem;
  right:-.72rem;
  display:grid;
  width:1.5rem;
  height:1.5rem;
  place-items:center;
  border:1px solid rgb(255 255 255/.1);
  border-radius:999px;
  background:rgb(3 19 46/.92);
  color:rgb(247 249 252/.62);
  box-shadow:0 5px 14px rgb(0 0 0/.22);
  opacity:.72;
  transition:160ms ease;
}
.sidebar-toggle:hover { border-color:rgb(0 229 255/.3); color:#29f3ff; opacity:1; transform:scale(1.06); }
.sidebar-toggle svg { width:.78rem; height:.78rem; fill:none; stroke:currentColor; stroke-width:1.8; stroke-linecap:round; stroke-linejoin:round; }

.brand { display: flex; min-width:0; align-items: center; gap: .8rem; transition:padding 220ms ease; }
.brand-copy { min-width:0; opacity:1; transition:opacity 120ms ease; white-space:nowrap; }

.brand-mark {
  width: 2.65rem;
  height: 2.65rem;
  flex: none;
  object-fit: contain;
  filter: drop-shadow(0 8px 14px rgb(0 229 255 / .2));
}

.nav-item { position:relative; border:1px solid transparent; transform-origin:left center; transition:background 160ms ease,color 160ms ease,transform 160ms ease,padding 220ms ease; }
.nav-item:hover { background:rgb(255 255 255/.035); transform:translateX(3px); }
.nav-item-active {
  border-color: rgb(255 255 255 / .075);
  background: linear-gradient(100deg, rgb(10 104 255 / .22), rgb(0 229 255 / .08));
  box-shadow: inset 0 1px rgb(255 255 255 / .05), 0 8px 24px rgb(0 0 0 / .12);
  animation:nav-arrive 240ms cubic-bezier(.2,.8,.2,1);
}
.nav-item-active::before { content: ''; position: absolute; left: -4px; width: 3px; height: 18px; border-radius: 4px; background: #00e5ff; box-shadow: 0 0 12px #0a68ff; }
.nav-icon { display: grid; width: 1.9rem; height: 1.9rem; flex:none; place-items: center; border:1px solid rgb(255 255 255/.035); border-radius: .6rem; background: rgb(255 255 255 / .04); transition:160ms ease; }
.nav-icon svg { width: 1.08rem; height: 1.08rem; fill: none; stroke: currentColor; stroke-width: 2; stroke-linecap: round; stroke-linejoin: round; }
.nav-icon.icon-overview { color:#29f3ff; }
.nav-icon.icon-containers { color:#68d9ea; }
.nav-icon.icon-images { color:#7ca7ff; }
.nav-icon.icon-volumes { color:#eb79b6; }
.nav-icon.icon-networks { color:#62dca0; }
.nav-icon.icon-storage { color:#f3ad69; }
.nav-icon.icon-engine { color:#7dd3fc; }
.nav-item-active .nav-icon { border-color:rgb(0 229 255/.16); background:rgb(10 104 255/.2); filter:drop-shadow(0 0 6px rgb(0 229 255/.22)); }
.nav-count { color: #777785; background: rgb(0 0 0 / .24); }
.nav-item-active .nav-count { color: #29f3ff; background: rgb(0 229 255 / .14); }

.sidebar-compact .sidebar { width:4.75rem; box-shadow:10px 0 32px rgb(0 0 0/.1); }
.sidebar-compact .app-content { margin-left:4.75rem; }
.sidebar-compact .brand { justify-content:center; padding-right:.5rem; padding-left:.5rem; }
.sidebar-compact .brand-mark { width:2.55rem; height:2.55rem; }
.sidebar-compact .brand-copy,
.sidebar-compact .nav-label,
.sidebar-compact .nav-count,
.sidebar-compact .nav-section,
.sidebar-compact .sidebar-status-copy,
.sidebar-compact .sidebar-platform { display:none; }
.sidebar-compact .sidebar nav { padding-right:.65rem; padding-left:.65rem; }
.sidebar-compact .nav-item { justify-content:center; gap:0; padding-right:.55rem; padding-left:.55rem; }
.sidebar-compact .nav-item:hover { transform:translateY(-1px); }
.sidebar-compact .nav-icon { width:2.2rem; height:2.2rem; border-radius:.68rem; }
.sidebar-compact .nav-icon svg { width:1.2rem; height:1.2rem; }
.sidebar-compact .nav-item::after {
  content:attr(data-label);
  position:absolute;
  z-index:40;
  top:50%;
  left:calc(100% + .72rem);
  padding:.42rem .62rem;
  border:1px solid rgb(255 255 255/.09);
  border-radius:.55rem;
  background:rgb(3 19 46/.96);
  color:#f7f9fc;
  box-shadow:0 8px 22px rgb(0 0 0/.28);
  font-size:.68rem;
  font-weight:550;
  opacity:0;
  pointer-events:none;
  transform:translate(-4px,-50%);
  transition:opacity 120ms ease,transform 120ms ease;
  white-space:nowrap;
}
.sidebar-compact .nav-item:hover::after,
.sidebar-compact .nav-item:focus-visible::after { opacity:1; transform:translate(0,-50%); }
.sidebar-compact .sidebar-status { right:.7rem; left:.7rem; display:grid; padding:.45rem; place-items:center; }
.sidebar-compact .sidebar-status > div { justify-content:center; }
.sidebar-compact .sidebar-status .status-orb { width:1.8rem; height:1.8rem; }

.sidebar-status { border: 1px solid rgb(255 255 255 / .065); background: linear-gradient(145deg, rgb(255 255 255 / .045), rgb(255 255 255 / .018)); box-shadow: inset 0 1px rgb(255 255 255 / .035); }
.status-orb { position: relative; display: grid; width: 1.65rem; height: 1.65rem; flex: none; place-items: center; border-radius: 999px; }
.status-orb i { width: .46rem; height: .46rem; border-radius: inherit; background: currentColor; box-shadow: 0 0 11px currentColor; }
.status-orb.is-online { color: #42dc9a; background: rgb(48 211 145 / .11); }
.status-orb.is-offline { color: #fb7185; background: rgb(244 63 94 / .11); }

.content-canvas { animation:page-arrive 260ms cubic-bezier(.2,.8,.2,1); }
.content-tools { display:flex; min-height:2.4rem; align-items:center; justify-content:space-between; gap:1rem; margin-bottom:1rem; }
.content-context { min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.search-wrap { position: relative; }
.search-wrap svg { position: absolute; z-index: 1; top: 50%; left: .75rem; width: .95rem; translate: 0 -50%; fill: none; stroke: #71717f; stroke-width: 1.8; stroke-linecap: round; }
.search-wrap .field { padding-left: 2.25rem; }

.overview-grid { display: grid; grid-template-columns: minmax(260px, 1.65fr) repeat(3, minmax(145px, 1fr)); gap: .9rem; }
.overview-card {
  position: relative;
  display: flex;
  min-height: 7.4rem;
  align-items: center;
  gap: .9rem;
  overflow: hidden;
  border: 1px solid rgb(255 255 255 / .07);
  border-radius: 1.15rem;
  background: linear-gradient(145deg, rgb(28 29 43 / .92), rgb(20 21 31 / .78));
  padding: 1.15rem;
  box-shadow: inset 0 1px rgb(255 255 255 / .045), 0 14px 34px rgb(0 0 0 / .14);
}
.overview-runtime::after { content: ''; position: absolute; right: -3rem; width: 8rem; height: 8rem; border-radius: 999px; background: #0a68ff; filter: blur(45px); opacity: .17; }
.metric-icon { display: grid; width: 2.75rem; height: 2.75rem; flex: none; place-items: center; border-radius: .9rem; font-size: 1.05rem; font-weight: 600; }
.metric-icon.violet { background: rgb(10 104 255 / .13); color: #29f3ff; }
.metric-icon.blue { background: rgb(56 139 253 / .12); color: #60a5fa; }
.metric-icon.pink { background: rgb(236 72 153 / .12); color: #f472b6; }
.metric-icon.cyan { background: rgb(0 229 255 / .1); color: #29f3ff; }
.metric-label { margin-bottom: .22rem; color: #777785; font-size: .63rem; font-weight: 650; letter-spacing: .1em; text-transform: uppercase; }
.metric-value { color: #f7f7fb; font-size: 1.45rem; font-weight: 650; letter-spacing: -.025em; }
.metric-detail { margin-top: .18rem; color: #656573; font-size: .67rem; }

.container-group { overflow:hidden; border:1px solid rgb(255 255 255/.07); border-radius:1.15rem; background:linear-gradient(145deg,rgb(26 27 39/.9),rgb(17 18 27/.86)); box-shadow:inset 0 1px rgb(255 255 255/.04),0 16px 40px rgb(0 0 0/.13); }
.group-header { border-bottom:1px solid rgb(255 255 255/.055); background:rgb(255 255 255/.015); }
.group-icon { display:grid; width:2.5rem; height:2.5rem; flex:none; place-items:center; border-radius:.82rem; }
.group-icon.compose { color:#76ddeb; background:linear-gradient(145deg,rgb(45 190 215/.16),rgb(45 126 215/.07)); }
.group-icon.standalone { color:#29f3ff; background:linear-gradient(145deg,rgb(10 104 255/.17),rgb(0 229 255/.07)); }
.group-icon svg { width:1.15rem; height:1.15rem; fill:none; stroke:currentColor; stroke-width:1.6; stroke-linecap:round; stroke-linejoin:round; }
.group-count { border:1px solid rgb(255 255 255/.06); border-radius:999px; background:rgb(255 255 255/.035); padding:.08rem .45rem; color:#767684; font-size:.58rem; }
.mini-status { width:.38rem; height:.38rem; border-radius:999px; background:#565663; }
.mini-status.online { background:#45d99b; box-shadow:0 0 8px #45d99b; }
.group-chevron { width:1rem; height:1rem; margin-left:.35rem; fill:none; stroke:#656573; stroke-width:1.8; transition:180ms ease; }
.group-chevron.collapsed { transform:rotate(-90deg); }
.primary-quiet { background:rgb(51 205 147/.065)!important; border-color:rgb(73 214 164/.14)!important; }
.container-list { padding:.55rem; }
.container-row { display:grid; grid-template-columns:minmax(230px,1.35fr) minmax(220px,.9fr) minmax(150px,.75fr) minmax(250px,auto); align-items:center; gap:1.1rem; border:1px solid transparent; border-radius:.9rem; padding:.9rem; transition:160ms ease; }
.container-row + .container-row { border-top-color:rgb(255 255 255/.045); border-top-left-radius:0; border-top-right-radius:0; }
.container-row:hover { border-color:rgb(255 255 255/.07); background:linear-gradient(90deg,rgb(255 255 255/.035),rgb(255 255 255/.018)); transform:translateY(-1px); box-shadow:0 9px 22px rgb(0 0 0/.1); }
.container-identity { display:flex; min-width:0; align-items:center; gap:.8rem; }
.container-avatar { position:relative; display:grid; width:2.65rem; height:2.65rem; flex:none; place-items:center; border:1px solid rgb(255 255 255/.06); border-radius:.85rem; background:rgb(255 255 255/.035); }
.container-avatar span { position:absolute; width:1.2rem; height:.34rem; border:1px solid #777786; border-radius:.2rem; transform:skewY(-18deg); }
.container-avatar span:first-child { translate:0 -.38rem; opacity:.5; }
.container-avatar span:last-child { translate:0 .38rem; opacity:.72; }
.container-avatar.running { border-color:rgb(63 215 162/.14); background:rgb(45 203 148/.07); }
.container-avatar.running span { border-color:#55dca9; box-shadow:0 0 8px rgb(67 218 165/.16); }
.state-badge { border:1px solid transparent; font-size:.56rem; font-weight:650; letter-spacing:.03em; text-transform:uppercase; }
.state-badge.running { border-color:rgb(69 217 155/.12); background:rgb(49 211 145/.08); color:#62dca8; }
.state-badge.stopped { border-color:rgb(255 255 255/.06); background:rgb(255 255 255/.035); color:#767684; }
.resource-cluster { display:grid; grid-template-columns:1fr 1fr; gap:.8rem; }
.resource-item > div:first-child { display:flex; align-items:center; justify-content:space-between; gap:.5rem; }
.resource-item span { color:#686876; font-size:.56rem; font-weight:650; letter-spacing:.08em; text-transform:uppercase; }
.resource-item b { color:#bdbdc8; font-size:.64rem; font-weight:550; }
.resource-track { height:3px; margin-top:.42rem; overflow:hidden; border-radius:999px; background:rgb(255 255 255/.055); }
.resource-track i { display:block; min-width:2px; height:100%; border-radius:inherit; transition:width 450ms ease; }
.resource-track i.cpu { background:linear-gradient(90deg,#0a68ff,#00e5ff); box-shadow:0 0 7px #0a68ff; }
.resource-track i.memory { background:linear-gradient(90deg,#22abc3,#66dce9); box-shadow:0 0 7px #39c4d7; }
.container-ports { display:flex; min-width:0; flex-wrap:wrap; gap:.3rem; }
.more-ports { align-self:center; color:#6e6e7b; font-size:.6rem; }
.container-actions { display:flex; justify-content:flex-end; gap:.3rem; }
.container-actions .action-button { padding:.4rem .58rem; font-size:.66rem; }
.action-primary { border-color:rgb(70 214 163/.14)!important; background:rgb(43 204 146/.06)!important; }
.action-danger { opacity:.65; }
.action-danger:hover { opacity:1; }
.group-reveal-enter-active,.group-reveal-leave-active { transition:220ms cubic-bezier(.2,.8,.2,1); transform-origin:top; }
.group-reveal-enter-from,.group-reveal-leave-to { opacity:0; transform:translateY(-8px) scaleY(.96); }
.container-empty { padding:3.75rem 2rem; }
.container-empty h3 { margin-top:1.2rem; color:#e9e9ef; font-size:1.05rem; font-weight:600; }
.container-empty p { margin:.45rem auto 0; max-width:25rem; color:#6d6d7a; font-size:.76rem; line-height:1.55; }
.empty-stack { position:relative; width:4.4rem; height:3.6rem; margin:auto; }
.empty-stack span { position:absolute; left:50%; width:2.8rem; height:.8rem; border:1px solid rgb(0 229 255/.5); border-radius:.35rem; background:linear-gradient(100deg,rgb(10 104 255/.22),rgb(0 229 255/.08)); transform:translateX(-50%) skewY(-8deg); box-shadow:0 8px 18px rgb(6 62 155/.14); }
.empty-stack span:first-child { top:0; opacity:.45; }
.empty-stack span:nth-child(2) { top:.85rem; opacity:.7; }
.empty-stack span:last-child { top:1.7rem; border-color:rgb(101 206 220/.45); }

@keyframes nav-arrive { from { opacity:.45; transform:translateX(-5px); } }
@keyframes page-arrive { from { opacity:0; transform:translateY(7px); filter:blur(2px); } }

.smart-care {
  position: relative;
  display: flex;
  min-height: calc(100vh - 9.5rem);
  flex-direction: column;
  align-items: center;
  justify-content: flex-start;
  overflow: hidden;
  padding: 2.75rem 2rem 2rem;
  text-align: center;
}
.smart-copy { position:relative; z-index:2; max-width:34rem; }
.smart-copy .eyebrow { color:#29f3ff; font-size:.62rem; font-weight:700; letter-spacing:.2em; }
.smart-copy h3 { margin-top:.7rem; color:#f8f8fb; font-size:clamp(1.9rem,3vw,2.65rem); font-weight:650; letter-spacing:-.04em; }
.smart-copy p { margin-top:.65rem; color:#777785; font-size:.88rem; line-height:1.6; }

.care-visual { position:relative; width:min(29rem,70vw); height:17rem; margin-top:.1rem; }
.care-halo { position:absolute; border-radius:999px; filter:blur(45px); will-change:transform,opacity; }
.halo-one { inset:17% 24%; background:rgb(10 104 255/.3); animation:halo-breathe 5.5s ease-in-out infinite; }
.halo-two { inset:32% 32% 7%; background:rgb(21 190 215/.16); animation:halo-breathe 7s ease-in-out -2.1s infinite reverse; }
.scan-wave { position:absolute; z-index:0; top:50%; left:50%; width:6rem; height:6rem; border:1px solid rgb(0 229 255/.42); border-radius:2rem; opacity:0; transform:translate(-50%,-50%) rotate(-7deg); }
.energy-streams { position:absolute; inset:0; width:100%; height:100%; overflow:visible; filter:drop-shadow(0 0 7px rgb(10 104 255/.48)); opacity:.68; }
.stream { fill:none; stroke-width:1.1; stroke-linecap:round; stroke-dasharray:14 24; animation:stream-flow 9s linear infinite; }
.stream-a { stroke:url(#stream-a); }
.stream-b { stroke:url(#stream-b); animation-direction:reverse; animation-duration:12s; }
.care-orbit { position:absolute; top:50%; left:50%; border:1px solid rgb(56 189 248/.18); border-radius:50%; transform:translate(-50%,-50%) rotate(-11deg); will-change:transform; }
.orbit-one { width:20rem; height:10rem; }
.orbit-two { width:14rem; height:14rem; border-color:rgb(79 198 228/.1); transform:translate(-50%,-50%) rotate(40deg); }
.care-orbit i { position:absolute; top:12%; left:20%; width:.42rem; height:.42rem; border-radius:999px; background:#29f3ff; box-shadow:0 0 14px #0a68ff; }
.orbit-two i { top:72%; left:91%; background:#5fd5ea; box-shadow:0 0 14px #33bfd9; }
.orbit-one { animation:orbit-spin 19s linear infinite; }
.orbit-two { animation:orbit-spin-reverse 24s linear infinite; }
.care-particles { position:absolute; inset:0; }
.care-particles i { --angle:calc(var(--particle) * 36deg); position:absolute; top:50%; left:50%; width:3px; height:3px; border-radius:999px; background:hsl(calc(185 + var(--particle) * 10) 82% 70%); box-shadow:0 0 7px currentColor; opacity:.25; transform:rotate(var(--angle)) translateX(calc(6.3rem + var(--particle) * .24rem)); animation:particle-twinkle calc(2.8s + var(--particle) * .16s) ease-in-out calc(var(--particle) * -.31s) infinite; }
.care-core { position:absolute; z-index:1; top:50%; left:50%; display:grid; width:8rem; height:8rem; place-items:center; border:1px solid rgb(0 229 255/.12); border-radius:2.3rem; background:radial-gradient(circle,rgb(6 62 155/.26),rgb(2 8 23/.08) 68%,transparent 72%); box-shadow:0 0 0 13px rgb(0 229 255/.05),0 30px 65px rgb(10 104 255/.3); transform:translate(-50%,-50%) rotate(-7deg); animation:core-float 5.2s cubic-bezier(.45,.05,.55,.95) infinite; will-change:transform; }
.care-core::before { content:''; position:absolute; inset:-11px; z-index:-1; border-radius:2.65rem; background:conic-gradient(from 30deg,transparent,#0a68ff 22%,transparent 41%,#29f3ff 66%,transparent 82%); opacity:.28; filter:blur(7px); animation:core-aura 8s linear infinite; }
.core-glass { position:absolute; border:1px solid rgb(255 255 255/.12); background:linear-gradient(145deg,rgb(255 255 255/.16),rgb(255 255 255/.015)); box-shadow:inset 0 1px rgb(255 255 255/.18); backdrop-filter:blur(3px); }
.glass-back { width:4.6rem; height:4.6rem; border-radius:1.45rem; opacity:.32; transform:translate(-2.9rem,-1.85rem) rotate(-19deg); animation:glass-drift-a 6s ease-in-out infinite; }
.glass-front { width:3.8rem; height:3.8rem; border-radius:1.2rem; opacity:.25; transform:translate(3rem,2rem) rotate(18deg); animation:glass-drift-b 7s ease-in-out -1.2s infinite; }
.core-mark { position:relative; z-index:2; width:7.3rem; height:7.3rem; object-fit:contain; filter:drop-shadow(0 12px 18px rgb(0 229 255/.18)); transform:rotate(7deg); }
.orbit-chip { position:absolute; z-index:2; display:flex; min-width:6.6rem; flex-direction:column; border:1px solid rgb(255 255 255/.085); border-radius:.85rem; background:rgb(21 22 33/.82); padding:.6rem .8rem; text-align:left; box-shadow:0 12px 28px rgb(0 0 0/.22),inset 0 1px rgb(255 255 255/.05); backdrop-filter:blur(12px); }
.orbit-chip b { color:#eeeeF5; font-size:.8rem; }
.orbit-chip span { margin-top:.1rem; color:#6f6f7d; font-size:.58rem; }
.chip-containers { top:26%; left:6%; animation:chip-float 4.8s ease-in-out -.7s infinite; }
.chip-projects { right:4%; bottom:26%; animation:chip-float 5.4s ease-in-out -2.3s infinite; }
.chip-runtime { right:11%; top:15%; animation:chip-float 4.3s ease-in-out -1.4s infinite; }
.care-visual.is-checking .care-core { animation:core-scan 1.05s ease-in-out infinite; }
.care-visual.is-checking .orbit-one { animation-duration:2.2s; }
.care-visual.is-checking .orbit-two { animation-duration:2.8s; }
.care-visual.is-checking .stream { animation-duration:1.5s; stroke-width:1.8; }
.care-visual.is-checking .wave-one { animation:scan-ripple 1.65s ease-out infinite; }
.care-visual.is-checking .wave-two { animation:scan-ripple 1.65s ease-out -.8s infinite; }
.care-visual.is-checking .care-particles i { opacity:.8; animation-duration:.75s; }

.care-summary { position:relative; z-index:2; display:grid; width:min(42rem,100%); grid-template-columns:repeat(3,1fr); border:1px solid rgb(255 255 255/.06); border-radius:1rem; background:rgb(255 255 255/.022); }
.care-summary > div { display:flex; align-items:center; gap:.7rem; padding:.8rem 1rem; text-align:left; }
.care-summary > div + div { border-left:1px solid rgb(255 255 255/.06); }
.care-summary p { display:flex; min-width:0; flex-direction:column; }
.care-summary b { color:#cfcfd8; font-size:.7rem; font-weight:600; }
.care-summary small { overflow:hidden; margin-top:.12rem; color:#666674; font-size:.58rem; text-overflow:ellipsis; white-space:nowrap; }
.summary-dot { width:.48rem; height:.48rem; flex:none; border-radius:999px; box-shadow:0 0 10px currentColor; }
.summary-dot.violet { color:#0a68ff; background:currentColor; }
.summary-dot.cyan { color:#56d6e8; background:currentColor; }
.summary-dot.pink { color:#ed6ba9; background:currentColor; }
.check-button { position:relative; z-index:2; width:5.2rem; height:5.2rem; margin-top:1rem; border:1px solid rgb(41 243 255/.58); border-radius:999px; background:linear-gradient(145deg,#0a68ff,#00e5ff); color:#f7f9fc; font-size:.82rem; font-weight:650; box-shadow:0 15px 34px rgb(6 62 155/.34),inset 0 2px 2px rgb(255 255 255/.3),inset 0 -4px 10px rgb(3 19 46/.28); transition:180ms ease; }
.check-button::before { content:''; position:absolute; inset:-7px; border:1px solid rgb(0 229 255/.24); border-radius:inherit; animation:button-breathe 2.8s ease-in-out infinite; }
.check-button:hover { transform:scale(1.04); filter:brightness(1.08); }
.check-button:active { transform:scale(.98); }
.check-button:disabled { cursor:wait; opacity:.72; }

@keyframes halo-breathe { 0%,100% { opacity:.62; transform:scale(.88) translate3d(-4%,2%,0); } 50% { opacity:1; transform:scale(1.16) translate3d(4%,-3%,0); } }
@keyframes core-float { 0%,100% { transform:translate(-50%,-50%) rotate(-7deg) translateY(4px); } 50% { transform:translate(-50%,-50%) rotate(-3deg) translateY(-7px) scale(1.025); } }
@keyframes core-scan { 0%,100% { transform:translate(-50%,-50%) rotate(-7deg) scale(.98); filter:brightness(1); } 50% { transform:translate(-50%,-50%) rotate(1deg) scale(1.08); filter:brightness(1.2); } }
@keyframes core-aura { to { transform:rotate(360deg); } }
@keyframes glass-drift-a { 50% { transform:translate(-3.35rem,-1.45rem) rotate(-10deg) scale(1.08); opacity:.48; } }
@keyframes glass-drift-b { 50% { transform:translate(3.25rem,1.5rem) rotate(28deg) scale(.92); opacity:.38; } }
@keyframes stream-flow { to { stroke-dashoffset:-190; } }
@keyframes particle-twinkle { 0%,100% { opacity:.12; scale:.65; } 45% { opacity:.85; scale:1.45; } }
@keyframes chip-float { 0%,100% { translate:0 3px; } 50% { translate:0 -5px; } }
@keyframes scan-ripple { 0% { width:5.5rem; height:5.5rem; opacity:.65; } 100% { width:18rem; height:18rem; border-radius:50%; opacity:0; } }
@keyframes button-breathe { 0%,100% { scale:1; opacity:.45; } 50% { scale:1.1; opacity:.85; } }
@keyframes orbit-spin { to { transform:translate(-50%,-50%) rotate(349deg); } }
@keyframes orbit-spin-reverse { to { transform:translate(-50%,-50%) rotate(-320deg); } }

@media (prefers-reduced-motion: reduce) {
  .care-visual *, .check-button::before { animation-duration:.001ms!important; animation-iteration-count:1!important; }
}

.offline-state { border:1px solid rgb(255 255 255/.07); background:linear-gradient(145deg,rgb(27 28 40/.82),rgb(19 20 29/.75)); box-shadow:inset 0 1px rgb(255 255 255/.035),0 18px 48px rgb(0 0 0/.14); }
.offline-icon { position:relative; display:grid; width:4.4rem; height:4.4rem; place-items:center; border-radius:1.25rem; background:radial-gradient(circle,rgb(10 104 255/.18),transparent 70%); box-shadow:0 12px 30px rgb(6 62 155/.16); }
.offline-icon img { width:4.2rem; height:4.2rem; object-fit:contain; filter:drop-shadow(0 8px 12px rgb(0 229 255/.16)); }

.panel {
  border: 1px solid rgb(255 255 255 / .07);
  border-radius: 1.1rem;
  background: linear-gradient(145deg, rgb(27 28 40 / .88), rgb(19 20 29 / .84));
  box-shadow: inset 0 1px rgb(255 255 255 / .035), 0 14px 38px rgb(0 0 0 / .12);
}

.table-head {
  text-align: left;
  font-size: 0.68rem;
  font-weight: 600;
  letter-spacing: 0.06em;
  color: #6f6f7d;
}

.toolbar-button,
.action-button,
.project-button,
.compose-button,
.primary-button,
.danger-button {
  border-radius: 0.65rem;
  padding: 0.48rem 0.8rem;
  font-size: 0.75rem;
  transition: 160ms ease;
}

.toolbar-button,
.action-button,
.project-button,
.compose-button {
  border: 1px solid rgb(255 255 255 / .09);
  background: rgb(255 255 255 / .04);
  color: #d9d9e2;
}

.toolbar-button:hover,
.action-button:hover,
.project-button:hover,
.compose-button:hover {
  border-color: rgb(255 255 255 / .15);
  background: rgb(255 255 255 / .075);
  translate: 0 -1px;
}

.primary-button {
  border: 1px solid rgb(0 229 255 / .42);
  background: linear-gradient(135deg, #0a68ff, #00e5ff);
  color: white;
  font-weight: 600;
  box-shadow: 0 7px 18px rgb(6 62 155 / .26), inset 0 1px rgb(255 255 255 / .18);
}

.primary-button:hover {
  background: linear-gradient(135deg, #0a68ff, #29f3ff);
  translate: 0 -1px;
}

.danger-button {
  border: 1px solid rgb(127 29 29);
  background: rgb(69 10 10 / 0.35);
  color: rgb(248 113 113);
}

.danger-button:hover {
  background: rgb(69 10 10 / 0.7);
}

.toolbar-button:disabled,
.action-button:disabled,
.project-button:disabled,
.compose-button:disabled,
.primary-button:disabled,
.danger-button:disabled {
  cursor: not-allowed;
  opacity: 0.4;
}

.field {
  border: 1px solid rgb(255 255 255 / .085);
  border-radius: .65rem;
  background: rgb(6 7 12 / .48);
  padding: .55rem .75rem;
  font-size: 0.78rem;
  color: #ededf2;
  outline: none;
}

.field:focus {
  border-color: rgb(0 229 255 / .75);
  box-shadow: 0 0 0 3px rgb(10 104 255 / .14);
}

.state-badge {
  display: inline-flex;
  border-radius: 9999px;
  padding: 0.25rem 0.6rem;
  font-size: 0.7rem;
  font-weight: 500;
}

.port-link,
.port-label {
  border-radius: 0.3rem;
  padding: 0.2rem 0.4rem;
  font-family: ui-monospace, monospace;
  font-size: 0.66rem;
}

.port-link {
  border: 1px solid rgb(12 74 110);
  background: rgb(8 47 73 / 0.4);
  color: rgb(56 189 248);
}

.port-label {
  border: 1px solid rgb(63 63 70);
  color: rgb(113 113 122);
}

.empty-state {
  border: 1px solid rgb(255 255 255 / .07);
  border-radius: 1.1rem;
  background: rgb(24 25 36 / .7);
  padding: 3rem;
  text-align: center;
  font-size: 0.85rem;
  color: rgb(113 113 122);
}

#compose-drop-zone.file-drop-target-active {
  border-color: #0a68ff;
  background: rgb(0 229 255 / .12);
}

tbody tr { transition: background 150ms ease; }

.storage-page {
  --storage-border:rgb(255 255 255/.075);
  --storage-border-strong:rgb(255 255 255/.12);
  --storage-surface:linear-gradient(145deg,rgb(27 28 40/.9),rgb(19 20 29/.86));
  --storage-raised:rgb(255 255 255/.035);
  --storage-text:#ededf2;
  --storage-muted:#858591;
  --storage-faint:#676775;
}
.storage-hero { position:relative; display:flex; align-items:center; gap:1rem; overflow:hidden; border:1px solid var(--storage-border); border-radius:1.15rem; background:var(--storage-surface); padding:1.3rem 1.4rem; box-shadow:inset 0 1px rgb(255 255 255/.04),0 14px 36px rgb(0 0 0/.12); }
.storage-hero::after { content:''; position:absolute; right:8%; width:9rem; height:9rem; border-radius:999px; background:#0a68ff; filter:blur(55px); opacity:.15; pointer-events:none; }
.storage-hero-icon { display:grid; width:3rem; height:3rem; flex:none; place-items:center; border:1px solid rgb(241 173 105/.2); border-radius:.92rem; background:linear-gradient(145deg,rgb(243 173 105/.16),rgb(0 229 255/.08)); color:#efad6d; }
.storage-hero-icon svg { width:1.35rem; height:1.35rem; fill:none; stroke:currentColor; stroke-width:1.7; stroke-linecap:round; stroke-linejoin:round; }
.storage-eyebrow { color:#29f3ff; font-size:.6rem; font-weight:700; letter-spacing:.16em; }
.storage-hero h3,.host-storage-heading h3,.cleanup-heading h3,.storage-empty h3 { margin-top:.28rem; color:var(--storage-text); font-size:1rem; font-weight:620; }
.storage-hero p,.host-storage-heading p,.cleanup-heading p,.storage-empty p { margin-top:.3rem; color:var(--storage-muted); font-size:.75rem; line-height:1.55; }
.safe-badge { z-index:1; display:inline-flex; flex:none; align-items:center; gap:.42rem; margin-left:auto; border:1px solid rgb(66 207 153/.14); border-radius:999px; background:rgb(49 201 145/.07); padding:.38rem .65rem; color:#62dca8; font-size:.63rem; font-weight:600; }
.safe-badge i { width:.38rem; height:.38rem; border-radius:999px; background:currentColor; box-shadow:0 0 7px currentColor; }
.storage-grid { display:grid; grid-template-columns:repeat(4,minmax(0,1fr)); gap:.85rem; }
.storage-card { border:1px solid var(--storage-border); border-radius:1rem; background:var(--storage-surface); padding:1rem; box-shadow:inset 0 1px rgb(255 255 255/.035),0 12px 30px rgb(0 0 0/.1); }
.storage-card-head { display:flex; align-items:center; gap:.65rem; }
.storage-type-icon { display:grid; width:2rem; height:2rem; flex:none; place-items:center; border-radius:.62rem; background:rgb(0 229 255/.1); color:#29f3ff; font-size:.68rem; font-weight:700; }
.storage-type { color:var(--storage-text); font-size:.72rem; font-weight:600; text-transform:capitalize; }
.storage-count { margin-top:.08rem; color:var(--storage-faint); font-size:.58rem; }
.storage-size { margin-top:1.05rem; color:var(--storage-text); font-size:1.65rem; font-weight:650; letter-spacing:-.035em; }
.storage-track { height:4px; margin-top:.85rem; overflow:hidden; border-radius:999px; background:rgb(255 255 255/.06); }
.storage-track i { display:block; height:100%; border-radius:inherit; background:linear-gradient(90deg,#0a68ff,#00e5ff); box-shadow:0 0 8px #0a68ff; }
.storage-meta { display:flex; justify-content:space-between; gap:.5rem; margin-top:.65rem; color:var(--storage-faint); font-size:.58rem; }
.storage-meta b { color:var(--storage-muted); font-weight:600; }
.storage-empty { border:1px dashed var(--storage-border-strong); border-radius:1rem; background:var(--storage-raised); padding:2.5rem; text-align:center; }
.storage-empty .empty-stack { transform:scale(.8); }
.host-storage-panel { overflow:hidden; border:1px solid var(--storage-border); border-radius:1.15rem; background:var(--storage-surface); box-shadow:inset 0 1px rgb(255 255 255/.035),0 14px 36px rgb(0 0 0/.1); }
.host-storage-heading { display:flex; align-items:flex-end; justify-content:space-between; gap:2rem; padding:1.2rem 1.3rem; }
.host-storage-heading p { max-width:30rem; text-align:right; }
.host-storage-list { border-top:1px solid var(--storage-border); }
.host-storage-row { display:grid; grid-template-columns:auto minmax(0,1fr) auto auto; align-items:center; gap:1rem; padding:1.15rem 1.3rem; }
.host-storage-row + .host-storage-row { border-top:1px solid var(--storage-border); }
.host-storage-folder { display:grid; width:2.45rem; height:2.45rem; place-items:center; border-radius:.72rem; background:rgb(241 140 53/.1); color:#f0a25d; }
.host-storage-folder svg { width:1.25rem; height:1.25rem; fill:none; stroke:currentColor; stroke-width:1.7; stroke-linecap:round; stroke-linejoin:round; }
.host-storage-copy { min-width:0; }
.host-storage-copy h4 { color:var(--storage-text); font-size:.82rem; font-weight:650; }
.host-storage-copy p { margin-top:.18rem; color:var(--storage-muted); font-size:.66rem; }
.host-storage-path { display:block; max-width:100%; overflow:hidden; margin-top:.34rem; padding:0; color:var(--storage-faint); font-family:ui-monospace,SFMono-Regular,Menlo,monospace; font-size:.61rem; text-align:left; text-overflow:ellipsis; white-space:nowrap; }
.host-storage-path:hover { color:#29f3ff; }
.host-storage-size { min-width:6.5rem; color:var(--storage-text); font-size:1.05rem; font-weight:650; text-align:right; }
.host-storage-actions { display:flex; gap:.5rem; }
.storage-clean-button,.storage-reveal-button { border:1px solid var(--storage-border-strong); border-radius:.65rem; padding:.48rem .72rem; font-size:.66rem; font-weight:600; transition:160ms ease; }
.storage-clean-button { background:rgb(10 104 255/.1); color:#65cfff; }
.storage-reveal-button { background:var(--storage-raised); color:var(--storage-text); }
.storage-clean-button:hover:not(:disabled),.storage-reveal-button:hover:not(:disabled) { border-color:rgb(0 229 255/.3); background:rgb(10 104 255/.14); transform:translateY(-1px); }
.storage-clean-button:disabled,.storage-reveal-button:disabled { cursor:not-allowed; opacity:.38; }
.storage-cleanup-backdrop { position:fixed; inset:0; z-index:80; display:grid; place-items:center; background:rgb(2 8 23/.76); padding:1.5rem; backdrop-filter:blur(14px); }
.storage-cleanup-sheet { --storage-border:rgb(255 255 255/.075); --storage-border-strong:rgb(255 255 255/.12); --storage-raised:rgb(255 255 255/.035); --storage-text:#ededf2; --storage-muted:#858591; --storage-faint:#676775; width:min(35rem,100%); max-height:calc(100vh - 3rem); overflow-y:auto; scrollbar-width:none; border:1px solid var(--storage-border-strong); border-radius:1.2rem; background:linear-gradient(145deg,#171a27,#0e111c); color:var(--storage-text); box-shadow:0 32px 90px rgb(0 0 0/.55),inset 0 1px rgb(255 255 255/.05); }
.storage-cleanup-sheet::-webkit-scrollbar { display:none; }
.storage-cleanup-sheet header { display:flex; align-items:flex-start; justify-content:space-between; gap:1rem; border-bottom:1px solid var(--storage-border); padding:1.25rem 1.35rem; }
.storage-cleanup-sheet h3 { margin-top:.3rem; font-size:1.05rem; font-weight:650; }
.storage-cleanup-sheet header p { margin-top:.2rem; color:var(--storage-muted); font-size:.68rem; }
.storage-cleanup-close { display:grid; width:2rem; height:2rem; place-items:center; border:1px solid var(--storage-border); border-radius:.62rem; background:var(--storage-raised); color:var(--storage-muted); font-size:1.15rem; }
.storage-engine-note { margin:1rem 1.35rem 0; border:1px solid rgb(10 104 255/.18); border-radius:.7rem; background:rgb(10 104 255/.08); padding:.72rem .8rem; color:#79d7f5; font-size:.66rem; line-height:1.5; }
.storage-cleanup-options { display:grid; gap:.6rem; padding:1rem 1.35rem 1.25rem; }
.storage-cleanup-options > button { display:grid; grid-template-columns:auto minmax(0,1fr) auto; align-items:center; gap:.75rem; border:1px solid var(--storage-border); border-radius:.82rem; background:var(--storage-raised); padding:.8rem; text-align:left; transition:160ms ease; }
.storage-cleanup-options > button:hover:not(:disabled) { border-color:rgb(0 229 255/.3); background:rgb(10 104 255/.09); transform:translateY(-1px); }
.storage-cleanup-options > button.selected { border-color:rgb(0 229 255/.58); background:linear-gradient(135deg,rgb(10 104 255/.15),rgb(0 229 255/.07)); box-shadow:0 0 0 1px rgb(0 229 255/.08),0 12px 28px rgb(10 104 255/.1); }
.storage-cleanup-options > button:disabled { cursor:wait; opacity:.45; }
.cleanup-option-icon { display:grid; width:2rem; height:2rem; place-items:center; border-radius:.6rem; background:rgb(0 229 255/.09); color:#29f3ff; }
.storage-cleanup-options span:nth-child(2) { display:flex; min-width:0; flex-direction:column; }
.storage-cleanup-options b { font-size:.73rem; font-weight:650; }
.storage-cleanup-options small { margin-top:.17rem; color:var(--storage-muted); font-size:.61rem; line-height:1.4; }
.storage-cleanup-options em { border-radius:999px; background:rgb(49 201 145/.08); padding:.22rem .45rem; color:#62dca8; font-size:.55rem; font-style:normal; white-space:nowrap; }
.storage-cleanup-options .deep em { background:rgb(241 140 53/.09); color:#f0a25d; }
.storage-cleanup-confirm { display:flex; align-items:flex-end; justify-content:space-between; gap:1rem; border-top:1px solid var(--storage-border); background:rgb(10 104 255/.055); padding:1rem 1.35rem; }
.storage-cleanup-confirm strong { font-size:.72rem; font-weight:650; }
.storage-cleanup-confirm p { max-width:22rem; margin-top:.2rem; color:var(--storage-muted); font-size:.59rem; line-height:1.45; }
.storage-cleanup-confirm .storage-cleanup-error { color:#ff9292; }
.storage-cleanup-run { flex:none; border:1px solid rgb(0 229 255/.3); border-radius:.68rem; background:linear-gradient(135deg,#0a68ff,#087acb); padding:.62rem .85rem; color:#f7f9fc; font-size:.64rem; font-weight:650; box-shadow:0 9px 22px rgb(10 104 255/.2); }
.storage-cleanup-run:hover:not(:disabled) { filter:brightness(1.08); transform:translateY(-1px); }
.storage-cleanup-run:disabled { cursor:wait; opacity:.65; }
.storage-cleanup-sheet footer { border-top:1px solid var(--storage-border); padding:.8rem 1.35rem; color:var(--storage-faint); font-size:.59rem; line-height:1.5; }
.cleanup-panel { border:1px solid var(--storage-border); border-radius:1.15rem; background:var(--storage-surface); padding:1.3rem; box-shadow:inset 0 1px rgb(255 255 255/.035),0 14px 36px rgb(0 0 0/.1); }
.cleanup-heading { display:flex; align-items:flex-end; justify-content:space-between; gap:2rem; }
.cleanup-heading p { max-width:32rem; text-align:right; }
.cleanup-grid { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:.65rem; margin-top:1.1rem; }
.cleanup-action { display:flex; min-width:0; align-items:center; gap:.8rem; border:1px solid var(--storage-border); border-radius:.82rem; background:var(--storage-raised); padding:.78rem .9rem; color:var(--storage-text); text-align:left; transition:160ms ease; }
.cleanup-action:hover { border-color:rgb(0 229 255/.27); background:rgb(10 104 255/.07); transform:translateY(-1px); }
.cleanup-action:disabled { cursor:not-allowed; opacity:.45; transform:none; }
.cleanup-action > span:last-child { display:flex; min-width:0; flex-direction:column; }
.cleanup-action b { font-size:.72rem; font-weight:600; }
.cleanup-action small { overflow:hidden; margin-top:.16rem; color:var(--storage-faint); font-size:.59rem; text-overflow:ellipsis; white-space:nowrap; }
.cleanup-icon { display:grid; width:2rem; height:2rem; flex:none; place-items:center; border-radius:.6rem; background:rgb(0 229 255/.1); color:#29f3ff; font-size:.72rem; }
.cleanup-danger { grid-column:1/-1; border-color:rgb(239 68 68/.16); }
.cleanup-danger .cleanup-icon { background:rgb(239 68 68/.09); color:#f17c7c; }
.cleanup-danger:hover { border-color:rgb(239 68 68/.28); background:rgb(239 68 68/.06); }

@media (max-width: 1180px) {
  .overview-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .storage-grid { grid-template-columns:repeat(2,minmax(0,1fr)); }
  .container-row { grid-template-columns:minmax(220px,1fr) minmax(220px,1fr); }
  .container-ports { grid-column:1; }
  .container-actions { grid-column:2; grid-row:2; }
  .group-actions .compose-button:nth-of-type(2), .group-actions .compose-button:nth-of-type(3) { display:none; }
  .host-storage-row { grid-template-columns:auto minmax(0,1fr) auto; }
  .host-storage-actions { grid-column:2/-1; justify-content:flex-end; }
}

@media (max-height: 800px) {
  .smart-care { min-height:auto; padding-top:1.25rem; }
  .smart-copy h3 { margin-top:.35rem; font-size:1.8rem; }
  .smart-copy p { margin-top:.3rem; }
  .care-visual { width:25rem; height:13rem; }
  .orbit-one { width:17rem; height:8.5rem; }
  .orbit-two { width:11.5rem; height:11.5rem; }
  .care-core { width:6rem; height:6rem; border-radius:1.8rem; }
  .core-mark { transform:rotate(7deg) scale(.78); }
  .orbit-chip { transform:scale(.88); }
  .chip-containers { left:3%; }
  .chip-runtime { right:8%; top:11%; }
  .chip-projects { right:1%; bottom:19%; }
  .check-button { width:4.5rem; height:4.5rem; margin-top:.75rem; }
}

@media (max-width: 760px) {
  .sidebar { width: 4.75rem; }
  .brand-copy, .nav-label, .nav-count, .nav-section, .sidebar-status { display: none; }
  .brand { justify-content: center; padding-inline: .5rem; }
  .nav-item { justify-content: center; }
  .sidebar-toggle { display:none; }
  .app-content { margin-left: 4.75rem; }
  .overview-grid { grid-template-columns: 1fr; }
  .container-row { grid-template-columns:1fr; }
  .container-ports,.container-actions { grid-column:1; grid-row:auto; justify-content:flex-start; }
  .group-actions { display:none; }
  .care-summary { grid-template-columns:1fr; }
  .care-summary > div + div { border-top:1px solid rgb(255 255 255/.06); border-left:0; }
  .orbit-chip { display:none; }
  .storage-hero { align-items:flex-start; }
  .safe-badge { display:none; }
  .storage-grid,.cleanup-grid { grid-template-columns:1fr; }
  .host-storage-heading,.cleanup-heading { align-items:flex-start; flex-direction:column; gap:.35rem; }
  .host-storage-heading p,.cleanup-heading p { text-align:left; }
  .storage-cleanup-confirm { align-items:stretch; flex-direction:column; }
  .storage-cleanup-run { width:100%; }
  .host-storage-row { grid-template-columns:auto minmax(0,1fr); align-items:start; }
  .host-storage-size { grid-column:2; text-align:left; }
  .host-storage-actions { grid-column:2; justify-content:flex-start; }
  main { padding-inline: 1rem; }
}
</style>
