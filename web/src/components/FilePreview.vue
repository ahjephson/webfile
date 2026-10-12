<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import Button from 'primevue/button'
import { api } from '@/api/client'
import type { FileEntry } from '@/types/files'
import type { PreviewKind } from '@/utils/preview'
import ImageGallery from '@/components/ImageGallery.vue'

const props = defineProps<{
  visible: boolean
  path: string
  kind: PreviewKind
  contentType: string
  folderPath: string
  entries: FileEntry[]
}>()
const emit = defineEmits<{ 'update:visible': [value: boolean]; navigate: [path: string]; 'return-to-folder': [] }>()
const mediaError = ref(false)
const previewRoot = ref<HTMLElement | null>(null)
const immersive = ref(false)
const source = computed(() => api.previewUrl(props.path))
const name = computed(() => props.path.split('/').pop() || props.path)
const canImmerse = computed(() => props.kind === 'video' && !mediaError.value)
const immersiveActive = computed(() => canImmerse.value && immersive.value)

watch(() => props.path, () => {
  mediaError.value = false
  leaveImmersive()
}, { flush: 'sync' })
watch(() => props.kind, leaveImmersive, { flush: 'sync' })
watch(() => props.visible, leaveImmersive, { flush: 'sync' })
watch(mediaError, failed => {
  if (failed) leaveImmersive()
}, { flush: 'sync' })

function leaveImmersive() {
  immersive.value = false
  if (document.fullscreenElement === previewRoot.value) void document.exitFullscreen().catch(() => undefined)
}

onMounted(() => {
  window.addEventListener('keydown', onKeyDown)
  document.addEventListener('fullscreenchange', onFullscreenChange)
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeyDown)
  document.removeEventListener('fullscreenchange', onFullscreenChange)
  leaveImmersive()
})

function close() {
  leaveImmersive()
  emit('update:visible', false)
}

function returnToFolder() {
  leaveImmersive()
  emit('return-to-folder')
}

async function toggleFullscreen() {
  const root = previewRoot.value
  if (!canImmerse.value) return
  if (!root) return
  if (immersive.value) {
    leaveImmersive()
    return
  }
  immersive.value = true
  if (root.requestFullscreen) await root.requestFullscreen().catch(() => undefined)
}

function onFullscreenChange() {
  if (document.fullscreenElement !== previewRoot.value) immersive.value = false
}

function onKeyDown(event: KeyboardEvent) {
  if (!props.visible || props.kind === 'image') return
  if (event.key === 'Escape' && immersiveActive.value) leaveImmersive()
  else if (event.key === 'Escape') returnToFolder()
}
</script>

<template>
  <ImageGallery
    v-if="visible && kind === 'image'"
    :path="path"
    :folder-path="folderPath"
    :entries="entries"
    @back="emit('return-to-folder')"
    @close="close"
    @navigate="emit('navigate', $event)"
  />

  <section v-else-if="visible" ref="previewRoot" class="absolute inset-0 z-40 flex min-h-0 flex-col bg-zinc-950 text-zinc-100" aria-label="File preview" :data-immersive="immersiveActive">
    <header v-show="!immersiveActive" class="flex h-14 shrink-0 items-center gap-3 border-b border-zinc-800 bg-zinc-900 px-3">
      <Button v-tooltip.bottom="'Back to folder'" icon="pi pi-arrow-left" severity="secondary" text rounded aria-label="Back to folder" @click="returnToFolder" />
      <div class="min-w-0">
        <div class="truncate font-semibold">{{ name }}</div>
        <div class="truncate text-xs text-zinc-400">{{ path }}</div>
      </div>
      <div v-if="canImmerse" class="ml-auto flex items-center gap-1">
        <Button v-tooltip.bottom="'Fullscreen'" icon="pi pi-window-maximize" severity="secondary" text aria-label="Enter fullscreen" @click="toggleFullscreen" />
      </div>
    </header>

    <div v-if="immersiveActive" class="absolute right-3 top-3 z-50">
      <Button v-tooltip.left="'Exit fullscreen'" icon="pi pi-times" severity="secondary" rounded class="border border-zinc-700 bg-black/65 shadow-lg" aria-label="Exit fullscreen" @click="toggleFullscreen" />
    </div>

    <div class="relative grid min-h-0 flex-1 place-items-center overflow-hidden">
      <video
        v-if="kind === 'video' && !mediaError"
        :src="source"
        class="h-full w-full min-h-0 min-w-0 bg-black object-contain"
        controls
        preload="metadata"
        playsinline
        @error="mediaError = true"
      />
      <div v-else class="max-w-lg text-center text-zinc-400">
        <i :class="mediaError ? 'pi pi-exclamation-triangle' : 'pi pi-file'" class="mb-4 text-5xl text-zinc-600" />
        <h2 class="mb-2 text-base font-semibold text-zinc-200">{{ mediaError ? 'Preview unavailable' : 'No preview available' }}</h2>
        <p class="mb-4 text-sm">{{ mediaError ? 'The browser could not display this file.' : "This type of file can't be previewed." }}</p>
      </div>
    </div>
  </section>
</template>
