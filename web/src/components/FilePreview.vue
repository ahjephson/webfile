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

watch(() => props.path, () => {
  mediaError.value = false
})

onMounted(() => {
  window.addEventListener('keydown', onKeyDown)
  document.addEventListener('fullscreenchange', onFullscreenChange)
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeyDown)
  document.removeEventListener('fullscreenchange', onFullscreenChange)
})

function close() {
  if (document.fullscreenElement === previewRoot.value) void document.exitFullscreen().catch(() => undefined)
  emit('update:visible', false)
}

function returnToFolder() {
  if (document.fullscreenElement === previewRoot.value) void document.exitFullscreen().catch(() => undefined)
  emit('return-to-folder')
}

async function toggleFullscreen() {
  const root = previewRoot.value
  if (!root) return
  if (immersive.value) {
    immersive.value = false
    if (document.fullscreenElement === root) await document.exitFullscreen().catch(() => undefined)
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
  if (event.key === 'Escape') returnToFolder()
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

  <section v-else-if="visible" ref="previewRoot" class="absolute inset-0 z-40 flex min-h-0 flex-col bg-zinc-950 text-zinc-100" aria-label="File preview" :data-immersive="immersive">
    <header v-show="!immersive" class="flex h-14 shrink-0 items-center gap-3 border-b border-zinc-800 bg-zinc-900 px-3">
      <Button v-tooltip.bottom="'Back to folder'" icon="pi pi-arrow-left" severity="secondary" text rounded aria-label="Back to folder" @click="returnToFolder" />
      <div class="min-w-0">
        <div class="truncate font-semibold">{{ name }}</div>
        <div class="truncate text-xs text-zinc-400">{{ path }}</div>
      </div>
      <div class="ml-auto flex items-center gap-1">
        <Button v-tooltip.bottom="'Fullscreen'" icon="pi pi-window-maximize" severity="secondary" text aria-label="Enter fullscreen" @click="toggleFullscreen" />
      </div>
    </header>

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
        <p class="mb-4 text-sm">{{ mediaError ? 'The browser could not display this file.' : 'This file type is not mapped to a preview handler.' }}</p>
      </div>
    </div>
  </section>
</template>
