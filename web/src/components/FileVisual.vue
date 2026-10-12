<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { api } from '@/api/client'
import type { FileEntry } from '@/types/files'
import { fileVisualForContentType, fileVisualForPath } from '@/utils/fileVisual'

const props = defineProps<{
  entry: FileEntry
  tile?: boolean
  tileSize?: 'small' | 'medium' | 'large'
  thumbnailEnabled?: boolean
}>()

const detectedContentType = ref('')
const thumbnailFailed = ref(false)
const videoSprite = ref<HTMLElement | null>(null)
const videoFrame = ref(0)
const videoFrameWidth = ref(0)
const videoThumbnailUrl = ref('')
let videoTimer: ReturnType<typeof setInterval> | null = null
let videoRetryTimer: number | null = null
let videoRequest = 0
const videoFrameCount = 10
const pathVisual = computed(() => fileVisualForPath(props.entry.path))
const visual = computed(() => detectedContentType.value
  ? fileVisualForContentType(detectedContentType.value, props.entry.path)
  : pathVisual.value)
const lockedDirectory = computed(() => props.entry.is_dir && props.entry.can_navigate === false)
const lockedFile = computed(() => !props.entry.is_dir && props.entry.can_read === false)
const showImageThumbnail = computed(() => props.tile && !props.entry.is_dir && !lockedFile.value && visual.value.kind === 'image' && !thumbnailFailed.value)
const showVideoThumbnail = computed(() => props.thumbnailEnabled !== false && props.tile && !props.entry.is_dir && !lockedFile.value && visual.value.kind === 'video' && !thumbnailFailed.value)
const tileHeight = computed(() => ({ small: '4rem', medium: '7rem', large: '10rem' })[props.tileSize ?? 'medium'])
const tileIconSize = computed(() => ({ small: '2.5rem', medium: '4rem', large: '5.5rem' })[props.tileSize ?? 'medium'])
const videoSpriteStyle = computed(() => ({
  backgroundImage: videoThumbnailUrl.value ? `url("${videoThumbnailUrl.value}")` : 'none',
  backgroundPositionX: `${-(videoFrame.value * videoFrameWidth.value)}px`,
  backgroundPositionY: '0',
  backgroundRepeat: 'no-repeat',
  backgroundSize: `${videoFrameCount * 100}% 100%`,
  transition: 'none',
}))

watch(() => [props.entry.path, props.entry.size, props.entry.modified] as const, async ([path]) => {
  stopVideoAnimation()
  detectedContentType.value = ''
  thumbnailFailed.value = false
  if (props.entry.is_dir || props.entry.can_read === false || fileVisualForPath(path).known) return
  try {
    const result = await api.contentType(path)
    if (props.entry.path === path) detectedContentType.value = result.content_type
  } catch {
    // Keep the generic file icon when type detection is unavailable.
  }
}, { immediate: true })

watch([() => props.entry.path, showVideoThumbnail], ([path, visible]) => {
  clearVideoThumbnail()
  if (visible) void loadVideoThumbnail(path)
}, { immediate: true })

onBeforeUnmount(() => {
  stopVideoAnimation()
  clearVideoThumbnail()
})

async function loadVideoThumbnail(path: string) {
  const request = ++videoRequest
  try {
    const result = await api.requestThumbnail(path)
    if (request !== videoRequest || props.entry.path !== path) {
      if (result.url) URL.revokeObjectURL(result.url)
      return
    }
    if (result.url) {
      videoThumbnailUrl.value = result.url
      return
    }
    if (result.job?.state === 'failed' || result.job?.state === 'cancelled') {
      thumbnailFailed.value = true
      return
    }
    videoRetryTimer = window.setTimeout(() => void loadVideoThumbnail(path), 750)
  } catch {
    if (request === videoRequest) thumbnailFailed.value = true
  }
}

function clearVideoThumbnail() {
  videoRequest++
  if (videoRetryTimer !== null) window.clearTimeout(videoRetryTimer)
  videoRetryTimer = null
  if (videoThumbnailUrl.value) URL.revokeObjectURL(videoThumbnailUrl.value)
  videoThumbnailUrl.value = ''
}

function startVideoAnimation() {
  if (!showVideoThumbnail.value || videoTimer !== null) return
  videoFrameWidth.value = videoSprite.value?.clientWidth ?? 0
  videoTimer = setInterval(() => {
    videoFrame.value = (videoFrame.value + 1) % videoFrameCount
  }, 500)
}

function stopVideoAnimation() {
  if (videoTimer !== null) clearInterval(videoTimer)
  videoTimer = null
  videoFrame.value = 0
}
</script>

<template>
  <span
    v-if="entry.is_dir"
    class="pointer-events-none grid min-h-0 min-w-0 place-items-center"
    :class="tile ? 'w-full overflow-hidden' : 'shrink-0'"
    :style="tile ? { height: tileHeight } : undefined"
    :role="lockedDirectory ? 'img' : undefined"
    :aria-label="lockedDirectory ? `No permission to open ${entry.name}` : undefined"
    :aria-hidden="lockedDirectory ? undefined : 'true'"
    :title="lockedDirectory ? 'You do not have permission to open this folder' : undefined"
  >
    <span class="relative grid place-items-center">
      <i class="pi pi-folder" :class="lockedDirectory ? 'text-zinc-500' : 'webfile-accent-warning'" :style="{ fontSize: tile ? tileIconSize : '1.125rem' }" aria-hidden="true" />
      <span v-if="lockedDirectory" class="webfile-accent-danger absolute -bottom-1 -right-2 grid place-items-center rounded-full border border-zinc-700 bg-zinc-950 shadow" :class="tile ? 'h-7 w-7 text-sm' : 'h-4 w-4 text-[0.6rem]'" data-directory-lock aria-hidden="true">
        <i class="pi pi-lock" />
      </span>
    </span>
  </span>
  <span
    v-else
    class="relative grid min-h-0 min-w-0 place-items-center"
    :class="tile ? 'relative w-full overflow-hidden' : 'pointer-events-none shrink-0'"
    :style="tile ? { height: tileHeight } : undefined"
    :role="lockedFile ? 'img' : undefined"
    :aria-label="lockedFile ? `No permission to read ${entry.name}` : undefined"
    :aria-hidden="lockedFile ? undefined : 'true'"
    :title="lockedFile ? 'You do not have permission to read or preview this file' : undefined"
    @mouseenter="startVideoAnimation"
    @mouseleave="stopVideoAnimation"
  >
    <img
      v-if="showImageThumbnail"
      :src="api.thumbnailUrl(entry.path)"
      :alt="`Preview ${entry.name}`"
      class="absolute inset-0 m-auto block object-contain"
      style="width: auto; height: auto; max-width: 100%; max-height: 100%"
      loading="lazy"
      draggable="false"
      data-file-thumbnail
      @error="thumbnailFailed = true"
    />
    <template v-else-if="showVideoThumbnail">
      <i :class="[visual.icon, visual.color]" :style="{ fontSize: tileIconSize }" aria-hidden="true" data-file-icon />
      <span
        ref="videoSprite"
        class="absolute h-full max-w-full aspect-square"
        :style="videoSpriteStyle"
        role="img"
        :aria-label="`Preview ${entry.name}`"
        data-video-thumbnail
        :data-video-frame="videoFrame"
      />
    </template>
    <i v-else :class="[visual.icon, lockedFile ? 'text-zinc-500' : visual.color]" :style="{ fontSize: tile ? tileIconSize : '1.125rem' }" aria-hidden="true" data-file-icon />
    <span v-if="lockedFile" class="webfile-accent-danger absolute -bottom-1 -right-2 grid place-items-center rounded-full border border-zinc-700 bg-zinc-950 shadow" :class="tile ? 'h-7 w-7 text-sm' : 'h-4 w-4 text-[0.6rem]'" data-file-lock aria-hidden="true">
      <i class="pi pi-lock" />
    </span>
  </span>
</template>
