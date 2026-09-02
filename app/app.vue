<script setup lang="ts">
import type { Device, DeviceStatus, ShutdownResult, WakeResult } from '../shared/types/device'

const devices = ref<Device[]>([])
const loading = ref(true)
const loadError = ref('')
const mode = ref('')
const search = ref('')
const formDialog = ref<HTMLDialogElement>()
const deleteDialog = ref<HTMLDialogElement>()
const shutdownDialog = ref<HTMLDialogElement>()
const editing = ref<Device | null>(null)
const deleting = ref<Device | null>(null)
const shutdownTarget = ref<Device | null>(null)
const form = reactive({ name: '', mac: '', address: '', sshUser: '' })
const formError = ref('')
const deleteError = ref('')
const shutdownError = ref('')
const saving = ref(false)
const removing = ref(false)
const shuttingDown = ref(false)
const forceShutdown = ref(false)
const sending = ref(new Set<string>())
const cooldowns = ref<Record<string, number>>({})
const feedback = ref<Record<string, { message: string, error: boolean } | undefined>>({})
const statuses = ref<Record<string, DeviceStatus>>({})
const refreshingStatus = ref(false)
const statusError = ref('')
const now = ref(Date.now())
const toast = ref('')
let timer: ReturnType<typeof setInterval> | undefined
let statusTimer: ReturnType<typeof setInterval> | undefined
let toastTimer: ReturnType<typeof setTimeout> | undefined
let postShutdownTimer: ReturnType<typeof setTimeout> | undefined

const filtered = computed(() => devices.value.filter(device => `${device.name} ${device.mac} ${device.address || ''} ${device.sshUser || ''}`.toLocaleLowerCase('en').includes(search.value.toLocaleLowerCase('en').trim())))

function errorMessage(error: unknown): string {
  const failure = error as { data?: { data?: { message?: string }, message?: string }, statusCode?: number }
  return failure.data?.data?.message || failure.data?.message || 'The request could not be completed. Check your connection or sign in again.'
}

async function loadDevices() {
  loading.value = true
  loadError.value = ''
  try { devices.value = await $fetch<Device[]>('/api/devices') }
  catch (error) { loadError.value = errorMessage(error) }
  finally { loading.value = false }
}

async function refreshStatuses() {
  if (refreshingStatus.value || !devices.value.length) return
  refreshingStatus.value = true
  statusError.value = ''
  try {
    const result = await $fetch<DeviceStatus[]>('/api/devices/status')
    statuses.value = Object.fromEntries(result.map(status => [status.deviceId, status]))
  }
  catch (error) { statusError.value = errorMessage(error) }
  finally { refreshingStatus.value = false }
}

function notify(message: string) {
  toast.value = message
  clearTimeout(toastTimer)
  toastTimer = setTimeout(() => { toast.value = '' }, 4500)
}

function openForm(device: Device | null = null) {
  editing.value = device
  form.name = device?.name || ''
  form.mac = device?.mac || ''
  form.address = device?.address || ''
  form.sshUser = device?.sshUser || ''
  formError.value = ''
  formDialog.value?.showModal()
}

function closeForm() { if (!saving.value) formDialog.value?.close() }
function closeDelete() { if (!removing.value) deleteDialog.value?.close() }
function closeShutdown() { if (!shuttingDown.value) shutdownDialog.value?.close() }

async function saveDevice() {
  saving.value = true
  formError.value = ''
  try {
    const device = await $fetch<Device>(editing.value ? `/api/devices/${editing.value.id}` : '/api/devices', {
      method: editing.value ? 'PATCH' : 'POST', body: { ...form },
    })
    devices.value = [...devices.value.filter(item => item.id !== device.id), device].sort((a, b) => a.name.localeCompare(b.name, 'en'))
    formDialog.value?.close()
    notify(editing.value ? 'Device updated' : 'Device registered')
    void refreshStatuses()
  }
  catch (error) { formError.value = errorMessage(error) }
  finally { saving.value = false }
}

function confirmDelete(device: Device) {
  deleting.value = device
  deleteError.value = ''
  deleteDialog.value?.showModal()
}

async function removeDevice() {
  if (!deleting.value) return
  removing.value = true
  deleteError.value = ''
  try {
    await $fetch(`/api/devices/${deleting.value.id}`, { method: 'DELETE', body: {} })
    devices.value = devices.value.filter(device => device.id !== deleting.value!.id)
    statuses.value = Object.fromEntries(Object.entries(statuses.value).filter(([id]) => id !== deleting.value!.id))
    deleteDialog.value?.close()
    notify('Device removed from registry')
  }
  catch (error) { deleteError.value = errorMessage(error) }
  finally { removing.value = false }
}

function confirmShutdown(device: Device) {
  shutdownTarget.value = device
  forceShutdown.value = false
  shutdownError.value = ''
  shutdownDialog.value?.showModal()
}

async function requestShutdown() {
  if (!shutdownTarget.value) return
  shuttingDown.value = true
  shutdownError.value = ''
  try {
    const result = await $fetch<ShutdownResult>(`/api/devices/${shutdownTarget.value.id}/shutdown`, {
      method: 'POST', body: { force: forceShutdown.value },
    })
    shutdownDialog.value?.close()
    notify(result.message)
    clearTimeout(postShutdownTimer)
    postShutdownTimer = setTimeout(() => { void refreshStatuses() }, 3000)
  }
  catch (error) { shutdownError.value = errorMessage(error) }
  finally { shuttingDown.value = false }
}

function remaining(id: string): number { return Math.max(0, Math.ceil(((cooldowns.value[id] || 0) - now.value) / 1000)) }

async function wake(device: Device) {
  if (sending.value.has(device.id) || remaining(device.id)) return
  sending.value.add(device.id)
  feedback.value[device.id] = undefined
  try {
    const result = await $fetch<WakeResult>(`/api/devices/${device.id}/wake`, { method: 'POST', body: {} })
    devices.value = devices.value.map(item => item.id === device.id ? result.device : item)
    feedback.value[device.id] = { message: result.message, error: false }
    cooldowns.value[device.id] = Date.now() + result.retryAfter * 1000
  }
  catch (error) {
    feedback.value[device.id] = { message: errorMessage(error), error: true }
    const failure = error as { data?: { data?: { retryAfter?: number } } }
    cooldowns.value[device.id] = Date.now() + (failure.data?.data?.retryAfter || 5) * 1000
  }
  finally { sending.value.delete(device.id) }
}

function dateLabel(value: string | null | undefined): string {
  return value ? new Intl.DateTimeFormat('en-GB', { dateStyle: 'short', timeStyle: 'short' }).format(new Date(value)) : 'No packet sent yet'
}

function deviceOnline(id: string): boolean { return Boolean(statuses.value[id]?.networkReachable || statuses.value[id]?.sshReady) }
function statusLabel(id: string): string {
  if (refreshingStatus.value && !statuses.value[id]) return 'Checking…'
  return statuses.value[id] ? (deviceOnline(id) ? 'Online' : 'No response') : 'Not checked'
}

onMounted(() => {
  void loadDevices().then(() => refreshStatuses())
  void $fetch<{ mode: string }>('/api/session').then(result => { mode.value = result.mode }).catch(() => {})
  timer = setInterval(() => { now.value = Date.now() }, 500)
  statusTimer = setInterval(() => { void refreshStatuses() }, 30000)
})
onUnmounted(() => {
  clearInterval(timer)
  clearInterval(statusTimer)
  clearTimeout(toastTimer)
  clearTimeout(postShutdownTimer)
})
</script>

<template>
  <div class="shell">
    <a class="skip-link" href="#main">Skip to content</a>
    <div class="workspace">
      <header class="topbar">
        <span v-if="mode" class="access-badge"><AppIcon name="shield" />{{ mode === 'access' ? 'Cloudflare Access' : 'Local development' }}</span>
      </header>
      <main id="main">
        <section aria-labelledby="devices-heading">
          <div class="section-heading">
            <div class="section-title"><h1 id="devices-heading">My devices</h1><span class="count">{{ devices.length }}</span></div>
            <div class="section-actions">
              <button class="button secondary refresh-button" :disabled="refreshingStatus || !devices.length" @click="refreshStatuses()"><span v-if="refreshingStatus" class="spinner small"></span><AppIcon v-else name="refresh" /> Refresh status</button>
              <label class="search"><AppIcon name="search" /><input v-model="search" type="search" aria-label="Search devices" placeholder="Search name, MAC or IP…" /></label>
              <button class="button primary add-button" @click="openForm()"><AppIcon name="plus" /> Add device</button>
            </div>
          </div>
          <p v-if="statusError" class="status-error" role="alert">Status refresh failed: {{ statusError }}</p>

          <div v-if="loadError" class="empty-state error-state" role="alert"><AppIcon name="info" /><h3>We couldn't load your devices</h3><p>{{ loadError }}</p><button class="button secondary" @click="loadDevices()"><AppIcon name="refresh" /> Retry</button></div>
          <div v-else-if="loading" class="empty-state" role="status"><span class="spinner"></span><p>Loading devices…</p></div>
          <div v-else-if="!devices.length" class="empty-state"><div class="empty-illustration"><AppIcon name="server" /><span><AppIcon name="plus" /></span></div><h3>Your first device starts here</h3><p>Add a name and its MAC address.<br />We'll handle the packet.</p><button class="button secondary" @click="openForm()"><AppIcon name="plus" /> Register my first device</button></div>
          <div v-else-if="!filtered.length" class="empty-state compact"><AppIcon name="search" /><h3>No matches</h3><p>Try another name or MAC address.</p><button class="button secondary" @click="search = ''">Clear search</button></div>
          <div v-else class="device-grid">
            <article v-for="device in filtered" :key="device.id" class="device-card" :aria-label="device.name">
              <div class="card-top"><span class="device-symbol"><AppIcon name="server" /></span><strong class="device-name">{{ device.name }}</strong><div class="card-tools"><button class="icon-button" :aria-label="`Edit ${device.name}`" :disabled="sending.has(device.id)" @click="openForm(device)"><AppIcon name="edit" /></button><button class="icon-button danger-hover" :aria-label="`Delete ${device.name}`" :disabled="sending.has(device.id)" @click="confirmDelete(device)"><AppIcon name="trash" /></button></div></div>
              <div class="device-status" :aria-label="`Status: ${statusLabel(device.id)}`">
                <span class="status-pill" :class="{ online: deviceOnline(device.id), offline: statuses[device.id] && !deviceOnline(device.id) }"><span class="status-dot"></span>{{ statusLabel(device.id) }}</span>
                <span class="status-pill ssh-status" :class="{ online: statuses[device.id]?.sshReady }"><AppIcon name="network" />{{ statuses[device.id]?.sshReady ? 'SSH ready' : 'SSH unavailable' }}</span>
              </div>
              <p class="mac-label">MAC ADDRESS</p><code class="mac">{{ device.mac }}</code>
              <p v-if="device.address && device.sshUser" class="remote-target">{{ device.sshUser }}@{{ device.address }}</p>
              <p v-else class="remote-target missing">Edit this device to configure status and shutdown.</p>
              <div class="last-sent"><AppIcon name="clock" /><span>{{ device.lastSentAt ? `Last sent: ${dateLabel(device.lastSentAt)}` : 'No packets sent' }}</span></div>
              <div class="power-actions">
                <button class="button wake-button" :disabled="sending.has(device.id) || remaining(device.id) > 0" @click="wake(device)"><span v-if="sending.has(device.id)" class="spinner small"></span><AppIcon v-else name="power" />{{ sending.has(device.id) ? 'Sending…' : remaining(device.id) ? `Wait ${remaining(device.id)} s` : 'Wake' }}</button>
                <button class="button shutdown-button" :disabled="!statuses[device.id]?.sshReady || shuttingDown" @click="confirmShutdown(device)"><AppIcon name="power" /> Shut down</button>
              </div>
              <p v-if="feedback[device.id]" class="card-feedback" :class="{ failure: feedback[device.id]!.error }" :role="feedback[device.id]!.error ? 'alert' : 'status'"><AppIcon :name="feedback[device.id]!.error ? 'info' : 'check'" />{{ feedback[device.id]!.message }}</p>
            </article>
            <button class="add-card" @click="openForm()"><span><AppIcon name="plus" /></span><strong>Add another device</strong><small>A name. A MAC. Done.</small></button>
          </div>
        </section>

        <aside class="info-banner"><span class="info-symbol"><AppIcon name="info" /></span><div><p>The device must have Wake-on-LAN enabled and be connected via Ethernet. “Packet sent” confirms the packet was sent, not that the device has started.</p></div><span class="lan-label">ON YOUR LOCAL NETWORK</span></aside>
      </main>
    </div>

    <dialog ref="formDialog" class="modal" aria-labelledby="form-title" @cancel.prevent="closeForm()">
      <form @submit.prevent="saveDevice()">
        <div class="modal-heading"><span class="device-symbol"><AppIcon name="server" /></span><button type="button" class="icon-button" aria-label="Close form" :disabled="saving" @click="closeForm()"><AppIcon name="close" /></button></div>
        <h2 id="form-title">{{ editing ? 'Edit device' : 'Add device' }}</h2><p class="modal-intro">You only need its name and Ethernet MAC address.</p>
        <label class="field">Device name<input v-model="form.name" name="name" placeholder="e.g. Living room server" maxlength="80" required autofocus autocomplete="off" :disabled="saving" /></label>
        <label class="field">MAC address<input v-model="form.mac" name="mac" class="mac-input" placeholder="AA:BB:CC:DD:EE:FF" maxlength="17" minlength="12" required autocomplete="off" spellcheck="false" :disabled="saving" /><span>Dashes or all 12 digits are also accepted.</span></label>
        <label class="field">Private IPv4 address<input v-model="form.address" name="address" class="mac-input" placeholder="192.168.1.25" maxlength="15" required autocomplete="off" spellcheck="false" :disabled="saving" /><span>Use a DHCP reservation so this address stays assigned to the device.</span></label>
        <label class="field">SSH user<input v-model="form.sshUser" name="sshUser" placeholder="jona-homelab-remote" maxlength="32" required autocomplete="off" spellcheck="false" :disabled="saving" /><span>Dedicated Windows account configured for restricted remote commands.</span></label>
        <p v-if="formError" class="form-error" role="alert">{{ formError }}</p>
        <div class="modal-actions"><button type="button" class="button secondary" :disabled="saving" @click="closeForm()">Cancel</button><button type="submit" class="button primary" :disabled="saving">{{ saving ? 'Saving…' : editing ? 'Save changes' : 'Add device' }}</button></div>
      </form>
    </dialog>

    <dialog ref="deleteDialog" class="modal" aria-labelledby="delete-title" @cancel.prevent="closeDelete()">
      <div class="modal-heading"><span class="device-symbol delete-symbol"><AppIcon name="trash" /></span><button class="icon-button" aria-label="Close confirmation" :disabled="removing" @click="closeDelete()"><AppIcon name="close" /></button></div>
      <h2 id="delete-title">Delete device?</h2><p class="modal-intro">This will remove <strong>{{ deleting?.name }}</strong> from the registry. It will not shut down or modify the computer. You can register its MAC again later.</p><p v-if="deleteError" class="form-error" role="alert">{{ deleteError }}</p>
      <div class="modal-actions"><button class="button secondary" :disabled="removing" @click="closeDelete()">Cancel</button><button class="button danger" :disabled="removing" @click="removeDevice()">{{ removing ? 'Deleting…' : 'Delete device' }}</button></div>
    </dialog>
    <dialog ref="shutdownDialog" class="modal" aria-labelledby="shutdown-title" @cancel.prevent="closeShutdown()">
      <form @submit.prevent="requestShutdown()">
        <div class="modal-heading"><span class="device-symbol shutdown-symbol"><AppIcon name="power" /></span><button type="button" class="icon-button" aria-label="Close shutdown confirmation" :disabled="shuttingDown" @click="closeShutdown()"><AppIcon name="close" /></button></div>
        <h2 id="shutdown-title">Shut down {{ shutdownTarget?.name }}?</h2>
        <p class="modal-intro">This sends a restricted command over SSH. The panel will keep checking status, but command acceptance does not prove the computer has shut down.</p>
        <fieldset class="shutdown-options" :disabled="shuttingDown">
          <legend>Shutdown mode</legend>
          <label><input v-model="forceShutdown" type="radio" :value="false" /><span><strong>Safe shutdown</strong><small>Does not force applications to close. An application may block shutdown.</small></span></label>
          <label><input v-model="forceShutdown" type="radio" :value="true" /><span><strong>Force shutdown</strong><small>Closes applications immediately. Unsaved work can be lost.</small></span></label>
        </fieldset>
        <p v-if="forceShutdown" class="force-warning"><AppIcon name="info" />Forced shutdown can permanently lose unsaved work.</p>
        <p v-if="shutdownError" class="form-error" role="alert">{{ shutdownError }}</p>
        <div class="modal-actions"><button type="button" class="button secondary" :disabled="shuttingDown" @click="closeShutdown()">Cancel</button><button type="submit" class="button danger" :disabled="shuttingDown">{{ shuttingDown ? 'Sending…' : forceShutdown ? 'Force shut down' : 'Shut down safely' }}</button></div>
      </form>
    </dialog>
    <div v-if="toast" class="toast" role="status"><AppIcon name="check" />{{ toast }}</div>
  </div>
</template>
