<script setup lang="ts">
import type { CompanionOperation, CompanionUpdateResult, DeviceStatus } from '../../shared/types/device'

const props = defineProps<{ deviceId: string, status?: DeviceStatus }>()
const emit = defineEmits<{ refresh: [] }>()
const requesting = ref(false)
const pending = ref<{ target?: string, startedAt: number }>()
const message = ref('')
const error = ref('')
const companion = computed(() => props.status?.companion)
const phases: Partial<Record<CompanionOperation['phase'], string>> = {
  checking: 'Comprobando versión', scheduled: 'Actualización programada', downloading: 'Descargando',
  verifying: 'Verificando paquete', installing: 'Instalando', restarting: 'Reiniciando Companion',
}
const updating = computed(() => Boolean(pending.value || companion.value?.state === 'updating'))
const stateLabel = computed(() => {
  if (pending.value && !props.status?.remoteReady) return 'Esperando a Companion'
  if (updating.value) return phases[companion.value?.operation?.phase || 'scheduled'] || 'Esperando confirmación'
  return { current: 'Actualizado', available: 'Actualización disponible', unknown: 'No comprobado', local: 'Versión local', failed: 'Actualización fallida', updating: 'Actualizando' }[companion.value?.state || 'unknown']
})
const canUpdate = computed(() => Boolean(props.status?.remoteReady && companion.value?.remoteUpdate && companion.value.latestVersion
  && companion.value.version?.startsWith('main-') && companion.value.version !== companion.value.latestVersion && !requesting.value && !updating.value))
const operationError = computed(() => companion.value?.state === 'failed' ? companion.value.operation?.error : '')

watch(() => props.status, (status) => {
  if (!pending.value || !status?.remoteReady) return
  const operation = status.companion?.operation
  if (operation?.phase === 'failed' || operation?.phase === 'rolled-back') {
    pending.value = undefined
    error.value = operation.error || 'La actualización falló. Consulta el diagnóstico.'
  }
  else if (pending.value.target && status.companion?.version === pending.value.target) {
    pending.value = undefined
    message.value = 'Actualización confirmada: Companion responde con la nueva versión.'
  }
  else if (!pending.value.target && operation?.targetVersion) pending.value.target = operation.targetVersion
})

async function update() {
  if (!canUpdate.value) return
  requesting.value = true
  error.value = ''
  message.value = ''
  try {
    const result = await $fetch<CompanionUpdateResult>(`/api/devices/${props.deviceId}/update`, { method: 'POST', body: {} })
    if (result.localBuild) error.value = 'Las versiones locales requieren instalación manual.'
    else if (result.scheduled) pending.value = { target: result.targetVersion, startedAt: Date.now() }
    else message.value = 'Comprobación terminada. Actualizando estado…'
  }
  catch (failure) {
    const value = failure as { data?: { data?: { message?: string }, message?: string } }
    error.value = value.data?.data?.message || value.data?.message || 'No se pudo confirmar la petición. Comprueba el estado.'
  }
  finally { requesting.value = false; emit('refresh') }
}

let poll: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  poll = setInterval(() => {
    if (pending.value && Date.now() - pending.value.startedAt > 15 * 60_000) {
      pending.value = undefined
      error.value = 'No se pudo confirmar la actualización. Consulta el diagnóstico antes de reintentar.'
    }
    if (updating.value) emit('refresh')
  }, 3000)
})
onUnmounted(() => clearInterval(poll))
</script>

<template>
  <section class="companion-update" aria-label="Versión de Companion">
    <div class="companion-version-line"><strong>Companion</strong><span class="status-pill" :class="{ online: companion?.state === 'current' && !updating }">{{ stateLabel }}</span></div>
    <p>Instalada: <code :title="companion?.version || undefined">{{ companion?.displayVersion || companion?.version || 'No comprobada' }}</code><br />Última publicada: <code :title="companion?.latestVersion || undefined">{{ companion?.latestDisplayVersion || companion?.latestVersion || 'No comprobada' }}</code></p>
    <p v-if="companion?.state === 'local'">Instalación manual. Las versiones locales no se actualizan automáticamente.</p>
    <p v-else-if="status?.remoteReady && companion && !companion.remoteUpdate">Necesita instalación manual inicial para habilitar actualización remota.</p>
    <p v-if="companion?.releaseError">{{ companion.releaseError }}</p>
    <p v-if="error || operationError" class="failure" role="alert">{{ error || operationError }}</p>
    <p v-else-if="message" role="status">{{ message }}</p>
    <button v-if="companion?.remoteUpdate && companion.state !== 'local' && (companion.state !== 'current' || updating)" class="button secondary" :disabled="!canUpdate" @click="update">
      <span v-if="requesting || updating" class="spinner small"></span><AppIcon v-else name="refresh" />{{ requesting ? 'Solicitando…' : updating ? 'Actualizando…' : 'Actualizar Companion' }}
    </button>
    <small v-if="requesting || updating" role="status">Solo se reinicia Companion. Esperando confirmación de versión.</small>
  </section>
</template>
