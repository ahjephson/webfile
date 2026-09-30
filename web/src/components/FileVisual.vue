<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { api } from '@/api/client'
import type { FileEntry } from '@/types/files'
import { fileVisualForContentType, fileVisualForPath } from '@/utils/fileVisual'

const props = defineProps<{
  entry: FileEntry
  tile?: boolean
  tileSize?: 'small' | 'medium' | 'large'
}>()

const detectedContentType = ref('')
const thumbnailFailed = ref(false)
const videoSprite = ref<HTMLElement | null>(null)
const videoFrame = ref(0)
const videoFrameWidth = ref(0)
let videoTimer: ReturnType<typeof setInterval> | null = null
const videoFrameCount = 10
const pathVisual = computed(() => fileVisualForPath(props.entry.path))
const visual = computed(() => detectedContentType.value
  ? fileVisualForContentType(detectedContentType.value, props.entry.path)
  : pathVisual.value)
const showImageThumbnail = computed(() => props.tile && !props.entry.is_dir && visual.value.kind === 'image' && !thumbnailFailed.value)
const showVideoThumbnail = computed(() => props.tile && !props.entry.is_dir && visual.value.kind === 'video' && !thumbnailFailed.value)
const tileHeight = computed(() => ({ small: '4rem', medium: '7rem', large: '10rem' })[props.tileSize ?? 'medium'])
const tileIconSize = computed(() => ({ small: '2.5rem', medium: '4rem', large: '5.5rem' })[props.tileSize ?? 'medium'])
const videoSpriteStyle = computed(() => ({
  backgroundImage: `url("${api.thumbnailUrl(props.entry.path)}")`,
  backgroundPositionX: `${-(videoFrame.value * videoFrameWidth.value)}px`,
  backgroundPositionY: '0',
  backgroundRepeat: 'no-repeat',
  backgroundSize: `${videoFrameCount * 100}% 100%`,
  transition: 'none',
}))

watch(() => props.entry.path, async path => {
  stopVideoAnimation()
  detectedContentType.value = ''
  thumbnailFailed.value = false
  if (props.entry.is_dir || fileVisualForPath(path).known) return
  try {
    const result = await api.contentType(path)
    if (props.entry.path === path) detectedContentType.value = result.content_type
  } catch {
    // Keep the generic file icon when type detection is unavailable.
  }
}, { immediate: true })

onBeforeUnmount(stopVideoAnimation)

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
  >
    <i class="pi pi-folder text-amber-400" :style="{ fontSize: tile ? tileIconSize : '1.125rem' }" aria-hidden="true" />
  </span>
  <span
    v-else
    class="grid min-h-0 min-w-0 place-items-center"
    :class="tile ? 'relative w-full overflow-hidden' : 'pointer-events-none shrink-0'"
    :style="tile ? { height: tileHeight } : undefined"
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
      <i v-else :class="[visual.icon, visual.color]" :style="{ fontSize: tile ? tileIconSize : '1.125rem' }" aria-hidden="true" data-file-icon />
  </span>
</template>
