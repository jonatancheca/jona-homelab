<script setup lang="ts">
import type { Favorite } from '../../shared/types/favorite'

const favorites = ref<Favorite[]>([])
const loading = ref(true)
const loadError = ref('')
const formDialog = ref<HTMLDialogElement>()
const deleteDialog = ref<HTMLDialogElement>()
const editing = ref<Favorite | null>(null)
const deleting = ref<Favorite | null>(null)
const form = reactive({ name: '', url: '' })
const formError = ref('')
const deleteError = ref('')
const saving = ref(false)
const removing = ref(false)
const announcement = ref('')

function errorMessage(error: unknown): string {
  const failure = error as { data?: { data?: { message?: string }, message?: string } }
  return failure.data?.data?.message || failure.data?.message || 'No se pudo completar la operación. Comprueba la conexión e inténtalo de nuevo.'
}

async function loadFavorites() {
  loading.value = true
  loadError.value = ''
  try { favorites.value = await $fetch<Favorite[]>('/api/favorites') }
  catch (error) { loadError.value = errorMessage(error) }
  finally { loading.value = false }
}

function openForm(favorite: Favorite | null = null) {
  editing.value = favorite
  form.name = favorite?.name || ''
  form.url = favorite?.url || ''
  formError.value = ''
  announcement.value = ''
  formDialog.value?.showModal()
}

function closeForm() { if (!saving.value) formDialog.value?.close() }
function closeDelete() { if (!removing.value) deleteDialog.value?.close() }

async function saveFavorite() {
  if (saving.value) return
  saving.value = true
  formError.value = ''
  try {
    const favorite = await $fetch<Favorite>(editing.value ? `/api/favorites/${editing.value.id}` : '/api/favorites', {
      method: editing.value ? 'PATCH' : 'POST', body: { ...form },
    })
    favorites.value = [...favorites.value.filter(item => item.id !== favorite.id), favorite].sort((a, b) => a.name.localeCompare(b.name, 'es'))
    formDialog.value?.close()
    announcement.value = editing.value ? 'Favorito actualizado.' : 'Favorito guardado.'
  }
  catch (error) { formError.value = errorMessage(error) }
  finally { saving.value = false }
}

function confirmDelete(favorite: Favorite) {
  deleting.value = favorite
  deleteError.value = ''
  announcement.value = ''
  deleteDialog.value?.showModal()
}

async function removeFavorite() {
  if (!deleting.value || removing.value) return
  removing.value = true
  deleteError.value = ''
  try {
    await $fetch(`/api/favorites/${deleting.value.id}`, { method: 'DELETE', body: {} })
    favorites.value = favorites.value.filter(item => item.id !== deleting.value!.id)
    deleteDialog.value?.close()
    announcement.value = 'Favorito eliminado.'
  }
  catch (error) { deleteError.value = errorMessage(error) }
  finally { removing.value = false }
}

onMounted(loadFavorites)
</script>

<template>
  <section class="favorites-section" aria-labelledby="favorites-heading" lang="es">
    <div class="section-heading">
      <div>
        <div class="section-title"><h2 id="favorites-heading"><AppIcon name="star" /> Favoritos</h2><span class="count">{{ favorites.length }}</span></div>
        <p class="favorites-intro">Tus servicios del homelab, a un clic.</p>
      </div>
      <button class="button primary add-button" :disabled="loading || !!loadError" @click="openForm()"><AppIcon name="plus" /> Añadir favorito</button>
    </div>

    <p v-if="loading" class="favorites-notice" role="status">Cargando favoritos…</p>
    <div v-else-if="loadError" class="favorites-notice">
      <p class="form-error" role="alert">{{ loadError }}</p>
      <button class="button secondary" @click="loadFavorites()">Reintentar</button>
    </div>
    <p v-else-if="!favorites.length" class="favorites-notice">Guarda las URLs de tus servicios para abrirlos desde este panel.</p>
    <div v-else class="favorites-grid">
      <article v-for="favorite in favorites" :key="favorite.id" class="favorite-card" :aria-label="favorite.name">
        <a class="favorite-link" :href="favorite.url" target="_blank" rel="noopener noreferrer" :aria-label="`Abrir ${favorite.name} (nueva pestaña)`">
          <span class="device-symbol"><AppIcon name="link" /></span>
          <span class="favorite-details"><strong>{{ favorite.name }}</strong><span>{{ favorite.url }}</span></span>
          <AppIcon name="external" />
        </a>
        <div class="favorite-tools">
          <button class="icon-button" :aria-label="`Editar favorito ${favorite.name}`" :title="`Editar ${favorite.name}`" @click="openForm(favorite)"><AppIcon name="edit" /></button>
          <button class="icon-button danger-hover" :aria-label="`Eliminar favorito ${favorite.name}`" :title="`Eliminar ${favorite.name}`" @click="confirmDelete(favorite)"><AppIcon name="trash" /></button>
        </div>
      </article>
    </div>
    <p v-if="announcement" class="favorite-announcement" role="status">{{ announcement }}</p>

    <dialog ref="formDialog" class="modal favorite-form" aria-labelledby="favorite-form-title" @cancel.prevent="closeForm()">
      <form @submit.prevent="saveFavorite()">
        <div class="modal-heading"><span class="device-symbol"><AppIcon name="star" /></span><button type="button" class="icon-button" aria-label="Cerrar formulario de favorito" :disabled="saving" @click="closeForm()"><AppIcon name="close" /></button></div>
        <h2 id="favorite-form-title">{{ editing ? 'Editar favorito' : 'Añadir favorito' }}</h2>
        <p class="modal-intro">Guarda un acceso directo a cualquier servicio de tu homelab.</p>
        <label class="field">Nombre<input v-model="form.name" name="favorite-name" placeholder="Ej. Proxmox" maxlength="80" required autofocus autocomplete="off" :disabled="saving" /></label>
        <label class="field">URL<input v-model="form.url" name="favorite-url" type="url" placeholder="https://proxmox.local:8006" maxlength="2048" required autocomplete="off" autocapitalize="none" spellcheck="false" :disabled="saving" aria-describedby="favorite-url-help" /><span id="favorite-url-help">Incluye http:// o https://. Se abrirá en una nueva pestaña.</span></label>
        <p v-if="formError" class="form-error" role="alert">{{ formError }}</p>
        <div class="modal-actions"><button type="button" class="button secondary" :disabled="saving" @click="closeForm()">Cancelar</button><button class="button primary" :disabled="saving">{{ saving ? 'Guardando…' : 'Guardar favorito' }}</button></div>
      </form>
    </dialog>

    <dialog ref="deleteDialog" class="modal" aria-labelledby="favorite-delete-title" @cancel.prevent="closeDelete()">
      <form @submit.prevent="removeFavorite()">
        <div class="modal-heading"><span class="device-symbol delete-symbol"><AppIcon name="trash" /></span><button type="button" class="icon-button" aria-label="Cerrar confirmación" :disabled="removing" @click="closeDelete()"><AppIcon name="close" /></button></div>
        <h2 id="favorite-delete-title">¿Eliminar favorito?</h2>
        <p class="modal-intro">Se eliminará el acceso directo a <strong>{{ deleting?.name }}</strong>. El servicio seguirá disponible en su URL.</p>
        <p v-if="deleteError" class="form-error" role="alert">{{ deleteError }}</p>
        <div class="modal-actions"><button type="button" class="button secondary" :disabled="removing" @click="closeDelete()">Cancelar</button><button class="button danger" :disabled="removing">{{ removing ? 'Eliminando…' : 'Eliminar favorito' }}</button></div>
      </form>
    </dialog>
  </section>
</template>
