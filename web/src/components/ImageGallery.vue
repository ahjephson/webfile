<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { CSSProperties } from 'vue'
import Button from 'primevue/button'
import { api } from '@/api/client'
import type { FileEntry } from '@/types/files'
import { previewKindForContentType, previewKindForPath } from '@/utils/preview'

const props = defineProps<{
  active?: boolean
  path: string
  folderPath: string
  entries: FileEntry[]
}>()
const emit = defineEmits<{ back: []; close: []; navigate: [path: string] }>()

type Point = { startX: number; startY: number; x: number; y: number; previousX: number; previousY: number; startedAt: number; offset: number }
type ControlsGesture = { pointerId: number; startX: number; startY: number; x: number; y: number }
type ThumbnailState = 'queued' | 'loading' | 'loaded' | 'failed'

const doubleTapDelay = 400
const doubleTapDistance = 40
const swipeAnimationMinMs = 220
const swipeAnimationMaxMs = 380

const root = ref<HTMLElement | null>(null)
const currentPath = ref(props.path)
const viewport = ref<HTMLElement | null>(null)
const track = ref<HTMLElement | null>(null)
const previewImage = ref<HTMLImageElement | null>(null)
const thumbnailStrip = ref<HTMLElement | null>(null)
const naturalWidth = ref(0)
const naturalHeight = ref(0)
const scale = ref(1)
const fitScale = ref(1)
const fitMode = ref(true)
const loaded = ref(false)
const mediaError = ref(false)
const immersive = ref(false)
const fullscreenControlsVisible = ref(true)
const touchEnabled = ref(false)
const thumbnailsVisible = ref(true)
const swipeOffset = ref(0)
const trackAnimating = ref(false)
const transitionDurationMs = ref(swipeAnimationMaxMs)
const transitionTargetPath = ref<string | null>(null)
const detectedImagePaths = ref<string[]>([])
const thumbnailStates = ref<Record<string, ThumbnailState>>({})
const metadataRevision = ref(0)
const touchPointers = new Map<number, Point>()
const preloadedOriginals = new Map<string, HTMLImageElement>()
const originalLoads = new Map<string, Promise<boolean>>()
const cancelOriginalLoads = new Map<string, () => void>()
const imageMetadata = new Map<string, { width: number; height: number }>()
let resizeObserver: ResizeObserver | null = null
let galleryDetection = 0
let galleryController: AbortController | null = null
let thumbnailGeneration = 0
let navigationTimer: ReturnType<typeof setTimeout> | null = null
let singleTapTimer: ReturnType<typeof setTimeout> | null = null
let lastTap: { x: number; y: number; at: number } | null = null
let pinchStartDistance = 0
let pinchStartScale = 1
let pinchUsed = false
let navigationRequest = 0
let thumbnailNeedsCenter = false
let fullscreenControlsGesture: ControlsGesture | null = null

const images = computed(() => props.entries.filter(entry => detectedImagePaths.value.includes(entry.path)))
const imageIndex = computed(() => images.value.findIndex(entry => entry.path === currentPath.value))
const activeThumbnailPath = computed(() => transitionTargetPath.value ?? currentPath.value)
const activeThumbnailIndex = computed(() => images.value.findIndex(entry => entry.path === activeThumbnailPath.value))
const previousEntry = computed(() => imageIndex.value > 0 ? images.value[imageIndex.value - 1] : undefined)
const nextEntry = computed(() => imageIndex.value >= 0 && imageIndex.value < images.value.length - 1 ? images.value[imageIndex.value + 1] : undefined)
const canPrevious = computed(() => Boolean(previousEntry.value))
const canNext = computed(() => Boolean(nextEntry.value))
const name = computed(() => currentPath.value.split('/').pop() || currentPath.value)
const source = computed(() => api.previewUrl(currentPath.value))
const zoomLevel = computed(() => `${Math.round(scale.value * 100)}%`)
const thumbnailsLoaded = computed(() => Object.values(thumbnailStates.value).filter(value => value === 'loaded' || value === 'failed').length)
const reducedMotion = computed(() => typeof window !== 'undefined' && window.matchMedia('(prefers-reduced-motion: reduce)').matches)
const imageStyle = computed<CSSProperties>(() => naturalWidth.value > 0 ? {
  width: `${naturalWidth.value * scale.value}px`,
  height: `${naturalHeight.value * scale.value}px`,
} : {
  width: 'auto',
  height: 'auto',
  maxWidth: '100%',
  maxHeight: '100%',
  visibility: 'hidden',
})
const trackStyle = computed<CSSProperties>(() => ({
  transform: `translate3d(calc(-100% + ${swipeOffset.value}px), 0, 0)`,
  transition: trackAnimating.value && !reducedMotion.value
    ? `transform ${transitionDurationMs.value}ms cubic-bezier(0.25, 0.46, 0.45, 0.94)`
    : 'none',
}))

watch(() => props.path, async () => {
  // Our own navigation is committed synchronously, so the parent's echo must
  // not clear a gesture that has already started on the next image.
  if (props.path === currentPath.value) return
  navigationRequest++
  stopTrackAnimation()
  currentPath.value = props.path
  restoreCurrentImage()
  swipeOffset.value = 0
  trackAnimating.value = false
  await nextTick()
  syncActiveThumbnail()
  preloadNearbyOriginals()
})
watch(() => props.folderPath, () => {
  clearOriginals()
  thumbnailStates.value = {}
  void detectGalleryImages()
})
watch(() => JSON.stringify(props.entries.map(entry => [entry.path, entry.is_dir, entry.can_read, entry.size, entry.modified])), () => {
  void detectGalleryImages()
})
watch(images, () => {
  preloadNearbyOriginals()
  void loadThumbnailCatalogue()
}, { immediate: true })
watch(imageIndex, () => syncActiveThumbnail())

onMounted(() => {
  touchEnabled.value = navigator.maxTouchPoints > 0 || window.matchMedia('(any-pointer: coarse)').matches
  window.addEventListener('keydown', onKeyDown)
  document.addEventListener('fullscreenchange', onFullscreenChange)
  resizeObserver = new ResizeObserver(() => {
    if (fitMode.value) fitImage()
    else clampPan()
  })
  if (viewport.value) resizeObserver.observe(viewport.value)
  void detectGalleryImages()
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeyDown)
  document.removeEventListener('fullscreenchange', onFullscreenChange)
  resizeObserver?.disconnect()
  resizeObserver = null
  clearPendingTap()
  clearOriginals()
  galleryDetection++
  galleryController?.abort()
  thumbnailGeneration++
  navigationRequest++
  if (navigationTimer !== null) clearTimeout(navigationTimer)
})

function resetCurrentImage() {
  naturalWidth.value = 0
  naturalHeight.value = 0
  scale.value = 1
  fitScale.value = 1
  fitMode.value = true
  loaded.value = false
  mediaError.value = false
  clearGesture()
}

function restoreCurrentImage() {
  const metadata = imageMetadata.get(currentPath.value)
  if (!metadata) {
    resetCurrentImage()
    return
  }
  naturalWidth.value = metadata.width
  naturalHeight.value = metadata.height
  const restoredScale = fittedScale(metadata.width, metadata.height)
  scale.value = restoredScale
  fitScale.value = restoredScale
  fitMode.value = true
  loaded.value = true
  mediaError.value = false
  clearGesture()
}

async function detectGalleryImages() {
  galleryController?.abort()
  const controller = new AbortController()
  galleryController = controller
  const request = ++galleryDetection
  const files = props.entries.filter(entry => !entry.is_dir && entry.can_read !== false)
  const paths = new Set(files.filter(entry => previewKindForPath(entry.path) === 'image').map(entry => entry.path))
  paths.add(currentPath.value)
  detectedImagePaths.value = files.filter(entry => paths.has(entry.path)).map(entry => entry.path)
  const unknown = files.filter(entry => previewKindForPath(entry.path) === 'unsupported')
  let cursor = 0
  async function worker() {
    while (cursor < unknown.length && !controller.signal.aborted) {
      const entry = unknown[cursor++]
      try {
        const result = await api.contentType(entry.path, controller.signal)
        if (previewKindForContentType(result.content_type, entry.path) === 'image') paths.add(entry.path)
      } catch {
        continue
      }
    }
  }
  await Promise.all(Array.from({ length: Math.min(4, unknown.length) }, worker))
  if (request === galleryDetection && !controller.signal.aborted) detectedImagePaths.value = files.filter(entry => paths.has(entry.path)).map(entry => entry.path)
}

async function loadThumbnailCatalogue() {
  const generation = ++thumbnailGeneration
  const ordered = images.value.map(entry => entry.path)
  if (!ordered.length) return
  const active = currentPath.value
  const queue = [active, ...ordered.filter(path => path !== active)]
  const states: Record<string, ThumbnailState> = {}
  for (const path of ordered) states[path] = 'queued'
  thumbnailStates.value = states
  const batchSize = 16
  for (let start = 0; start < queue.length && generation === thumbnailGeneration; start += batchSize) {
    const batch = queue.slice(start, start + batchSize)
    await runBounded(batch, 4, path => loadThumbnail(path, generation))
    await new Promise(resolve => setTimeout(resolve, 0))
  }
}

async function runBounded<T>(values: T[], concurrency: number, action: (value: T) => Promise<void>) {
  let cursor = 0
  async function worker() {
    while (cursor < values.length) await action(values[cursor++])
  }
  await Promise.all(Array.from({ length: Math.min(concurrency, values.length) }, worker))
}

function loadThumbnail(path: string, generation: number) {
  thumbnailStates.value = { ...thumbnailStates.value, [path]: 'loading' }
  return new Promise<void>(resolve => {
    const image = new Image()
    const finish = (state: ThumbnailState) => {
      if (generation === thumbnailGeneration) thumbnailStates.value = { ...thumbnailStates.value, [path]: state }
      image.onload = null
      image.onerror = null
      resolve()
    }
    image.onload = () => finish('loaded')
    image.onerror = () => finish('failed')
    image.src = api.thumbnailUrl(path)
  })
}

function retryThumbnail(path: string) {
  void loadThumbnail(path, thumbnailGeneration)
}

function preloadNearbyOriginals() {
  const index = imageIndex.value
  if (index < 0) return
  const desired = new Set(images.value.slice(Math.max(0, index - 5), index + 6).map(entry => entry.path))
  for (const path of preloadedOriginals.keys()) {
    if (!desired.has(path)) releaseOriginal(path)
  }
  for (let offset = -5; offset <= 5; offset++) {
    const entry = images.value[index + offset]
    if (!entry) continue
    void ensureOriginalReady(entry.path)
  }
}

function ensureOriginalReady(path: string) {
  const existing = originalLoads.get(path)
  if (existing) return existing
  const image = new Image()
  image.decoding = 'async'
  preloadedOriginals.set(path, image)
  const load = new Promise<boolean>(resolve => {
    let settled = false
    const finish = (ready: boolean) => {
      if (settled) return
      settled = true
      image.onload = null
      image.onerror = null
      cancelOriginalLoads.delete(path)
      resolve(ready)
    }
    cancelOriginalLoads.set(path, () => finish(false))
    image.onload = () => {
      imageMetadata.set(path, { width: image.naturalWidth, height: image.naturalHeight })
      metadataRevision.value++
      void Promise.resolve(image.decode?.()).catch(() => undefined).finally(() => finish(true))
    }
    image.onerror = () => finish(false)
  })
  originalLoads.set(path, load)
  image.src = api.previewUrl(path)
  return load
}

function releaseOriginal(path: string) {
  cancelOriginalLoads.get(path)?.()
  const image = preloadedOriginals.get(path)
  if (image) image.src = ''
  preloadedOriginals.delete(path)
  originalLoads.delete(path)
  imageMetadata.delete(path)
  metadataRevision.value++
}

function clearOriginals() {
  for (const path of preloadedOriginals.keys()) releaseOriginal(path)
  imageMetadata.clear()
  metadataRevision.value++
}

function onImageLoad(event: Event) {
  const image = event.currentTarget as HTMLImageElement
  naturalWidth.value = image.naturalWidth
  naturalHeight.value = image.naturalHeight
  imageMetadata.set(currentPath.value, { width: image.naturalWidth, height: image.naturalHeight })
  metadataRevision.value++
  fitImage()
  loaded.value = true
}

function carouselImageStyle(path: string): CSSProperties {
  void metadataRevision.value
  const metadata = imageMetadata.get(path)
  if (!metadata) {
    return {
      visibility: 'hidden',
      width: '0',
      height: '0',
    }
  }
  const imageScale = fittedScale(metadata.width, metadata.height)
  return {
    width: `${metadata.width * imageScale}px`,
    height: `${metadata.height * imageScale}px`,
    maxWidth: 'none',
    maxHeight: 'none',
  }
}

function onCarouselImageLoad(path: string, event: Event) {
  const image = event.currentTarget as HTMLImageElement
  const metadata = imageMetadata.get(path)
  if (metadata?.width === image.naturalWidth && metadata.height === image.naturalHeight) return
  imageMetadata.set(path, { width: image.naturalWidth, height: image.naturalHeight })
  metadataRevision.value++
}

function fittedScale(width: number, height: number) {
  const target = viewport.value
  if (!target || width === 0 || height === 0) return 1
  const availableWidth = Math.max(1, target.clientWidth - 32)
  const availableHeight = Math.max(1, target.clientHeight - 32)
  return Math.min(1, availableWidth / width, availableHeight / height)
}

function fitImage() {
  if (naturalWidth.value === 0 || naturalHeight.value === 0) return
  scale.value = fittedScale(naturalWidth.value, naturalHeight.value)
  fitScale.value = scale.value
  fitMode.value = true
  void nextTick(() => {
    if (!viewport.value) return
    viewport.value.scrollLeft = 0
    viewport.value.scrollTop = 0
  })
}

function actualSize() {
  scale.value = 1
  fitMode.value = false
}

function zoomBy(amount: number) {
  scale.value = Math.min(8, Math.max(0.1, Math.round((scale.value + amount) * 100) / 100))
  fitMode.value = false
}

function zoomWithWheel(event: WheelEvent) {
  zoomBy(event.deltaY < 0 ? 0.1 : -0.1)
}

function clampPan() {
  const target = viewport.value
  if (!target) return
  target.scrollLeft = Math.min(target.scrollLeft, Math.max(0, target.scrollWidth - target.clientWidth))
  target.scrollTop = Math.min(target.scrollTop, Math.max(0, target.scrollHeight - target.clientHeight))
}

function touchDistance() {
  const points = [...touchPointers.values()]
  return points.length < 2 ? 0 : Math.hypot(points[0].x - points[1].x, points[0].y - points[1].y)
}

function clearGesture() {
  touchPointers.clear()
  pinchStartDistance = 0
  pinchStartScale = scale.value
  pinchUsed = false
  swipeOffset.value = 0
  clearPendingTap()
}

function clearPendingTap() {
  if (singleTapTimer !== null) clearTimeout(singleTapTimer)
  singleTapTimer = null
  lastTap = null
}

async function toggleActualSizeAt(clientX: number, clientY: number) {
  if (Math.abs(scale.value - 1) < 0.001 && !fitMode.value) {
    fitImage()
    return
  }
  const scroller = viewport.value
  const image = previewImage.value
  if (!scroller || !image || naturalWidth.value === 0 || naturalHeight.value === 0) return
  const before = image.getBoundingClientRect()
  const focalX = Math.min(naturalWidth.value, Math.max(0, (clientX - before.left) / scale.value))
  const focalY = Math.min(naturalHeight.value, Math.max(0, (clientY - before.top) / scale.value))
  scale.value = 1
  fitMode.value = false
  await nextTick()
  const after = image.getBoundingClientRect()
  scroller.scrollLeft += after.left + focalX - clientX
  scroller.scrollTop += after.top + focalY - clientY
}

function handleTap(clientX: number, clientY: number) {
  const now = performance.now()
  if (lastTap && now - lastTap.at <= doubleTapDelay && Math.hypot(clientX - lastTap.x, clientY - lastTap.y) <= doubleTapDistance) {
    clearPendingTap()
    void toggleActualSizeAt(clientX, clientY)
    return
  }
  clearPendingTap()
  lastTap = { x: clientX, y: clientY, at: now }
  singleTapTimer = setTimeout(() => {
    singleTapTimer = null
    lastTap = null
    if (immersive.value) toggleThumbnails()
  }, doubleTapDelay)
}

function toggleThumbnails() {
  thumbnailsVisible.value = !thumbnailsVisible.value
  if (thumbnailsVisible.value && thumbnailNeedsCenter) {
    thumbnailNeedsCenter = false
    scrollActiveThumbnailIntoView(false)
  }
  refitAfterLayout()
}

function onFullscreenControlsPointerDown(event: PointerEvent) {
  if (!touchEnabled.value || (event.pointerType !== 'touch' && event.pointerType !== 'pen')) return
  fullscreenControlsGesture = {
    pointerId: event.pointerId,
    startX: event.clientX,
    startY: event.clientY,
    x: event.clientX,
    y: event.clientY,
  }
  const target = event.currentTarget as HTMLElement
  target.setPointerCapture(event.pointerId)
}

function onFullscreenControlsPointerMove(event: PointerEvent) {
  if (fullscreenControlsGesture?.pointerId !== event.pointerId) return
  fullscreenControlsGesture.x = event.clientX
  fullscreenControlsGesture.y = event.clientY
}

function onFullscreenControlsPointerUp(event: PointerEvent) {
  const gesture = fullscreenControlsGesture
  fullscreenControlsGesture = null
  if (!gesture || gesture.pointerId !== event.pointerId) return
  const deltaX = gesture.x - gesture.startX
  const deltaY = gesture.y - gesture.startY
  if (deltaY < -36 && Math.abs(deltaY) > Math.abs(deltaX) * 1.2) {
    event.preventDefault()
    fullscreenControlsVisible.value = false
  }
}

function onFullscreenControlsPointerCancel(event: PointerEvent) {
  if (fullscreenControlsGesture?.pointerId === event.pointerId) fullscreenControlsGesture = null
}

function onPointerDown(event: PointerEvent) {
  if (event.pointerType === 'mouse' && event.button !== 0) return
  navigationRequest++
  interruptTrackAnimation()
  if (event.pointerType === 'touch' || event.pointerType === 'pen') event.preventDefault()
  const point: Point = {
    startX: event.clientX,
    startY: event.clientY,
    x: event.clientX,
    y: event.clientY,
    previousX: event.clientX,
    previousY: event.clientY,
    startedAt: performance.now(),
    offset: swipeOffset.value,
  }
  touchPointers.set(event.pointerId, point)
  ;(event.currentTarget as HTMLElement).setPointerCapture(event.pointerId)
  if (touchPointers.size === 2) {
    pinchStartDistance = touchDistance()
    pinchStartScale = scale.value
    pinchUsed = true
    swipeOffset.value = 0
  }
}

function onPointerMove(event: PointerEvent) {
  const point = touchPointers.get(event.pointerId)
  if (!point) return
  point.previousX = point.x
  point.previousY = point.y
  point.x = event.clientX
  point.y = event.clientY
  if (touchPointers.size >= 2 && pinchStartDistance > 0) {
    scale.value = Math.min(8, Math.max(0.1, pinchStartScale * touchDistance() / pinchStartDistance))
    fitMode.value = false
    return
  }
  if (!fitMode.value && viewport.value) {
    viewport.value.scrollLeft -= point.x - point.previousX
    viewport.value.scrollTop -= point.y - point.previousY
    return
  }
  const deltaX = point.x - point.startX
  const atBeginning = deltaX > 0 && !canPrevious.value
  const atEnd = deltaX < 0 && !canNext.value
  swipeOffset.value = point.offset + ((atBeginning || atEnd) ? deltaX * 0.28 : deltaX)
}

function onPointerUp(event: PointerEvent) {
  const point = touchPointers.get(event.pointerId)
  if (!point) return
  if (event.pointerType === 'touch' || event.pointerType === 'pen') event.preventDefault()
  const wasPinch = pinchUsed || touchPointers.size > 1
  touchPointers.delete(event.pointerId)
  if (touchPointers.size === 0) pinchUsed = false
  if (wasPinch) return
  const deltaX = point.x - point.startX
  const deltaY = point.y - point.startY
  const duration = Math.max(1, performance.now() - point.startedAt)
  const velocity = Math.abs(deltaX) / duration
  const threshold = Math.min(120, (viewport.value?.clientWidth ?? 600) * 0.2)
  if (fitMode.value && Math.abs(deltaX) > Math.abs(deltaY) * 1.2 && (Math.abs(deltaX) >= threshold || velocity >= 0.5)) {
    if (deltaX < 0 && canNext.value) animateNavigate(1)
    else if (deltaX > 0 && canPrevious.value) animateNavigate(-1)
    else snapBack()
    return
  }
  snapBack()
  if (Math.hypot(deltaX, deltaY) < 12 && duration < 400 && (event.pointerType === 'touch' || event.pointerType === 'pen')) handleTap(point.x, point.y)
}

function onPointerCancel(event: PointerEvent) {
  touchPointers.delete(event.pointerId)
  if (touchPointers.size === 0) pinchUsed = false
  snapBack()
}

function snapBack() {
  stopTrackAnimation()
  if (Math.abs(swipeOffset.value) < 0.5) {
    swipeOffset.value = 0
    return
  }
  const width = viewport.value?.clientWidth ?? 1
  transitionDurationMs.value = swipeTransitionDuration(swipeOffset.value, 0, width)
  trackAnimating.value = true
  swipeOffset.value = 0
  navigationTimer = setTimeout(stopTrackAnimation, reducedMotion.value ? 0 : transitionDurationMs.value)
}

function stopTrackAnimation() {
  if (navigationTimer !== null) clearTimeout(navigationTimer)
  navigationTimer = null
  transitionTargetPath.value = null
  trackAnimating.value = false
}

function interruptTrackAnimation() {
  if (!trackAnimating.value) return
  const width = viewport.value?.clientWidth ?? 1
  const renderedOffset = track.value
    ? new DOMMatrixReadOnly(getComputedStyle(track.value).transform).m41 + width
    : swipeOffset.value
  const target = transitionTargetPath.value
  const targetOffset = target
    ? (images.value.findIndex(entry => entry.path === target) > imageIndex.value ? -width : width)
    : 0
  finishAnimatedNavigation()
  stopTrackAnimation()
  // Rebase the three panels around the committed image without jumping the
  // pixels currently on screen. The new pointer can drag immediately.
  swipeOffset.value = renderedOffset - targetOffset
}

function swipeTransitionDuration(currentOffset: number, targetOffset: number, width: number) {
  const remainingRatio = Math.min(1, Math.abs(targetOffset - currentOffset) / Math.max(1, width))
  return Math.round(swipeAnimationMinMs + (swipeAnimationMaxMs - swipeAnimationMinMs) * remainingRatio)
}

function finishAnimatedNavigation() {
  const path = transitionTargetPath.value
  if (!path) return
  stopTrackAnimation()
  currentPath.value = path
  restoreCurrentImage()
  syncActiveThumbnail()
  preloadNearbyOriginals()
  emit('navigate', path)
}

function onTrackTransitionEnd(event: TransitionEvent) {
  if (event.target !== track.value || event.propertyName !== 'transform' || !trackAnimating.value) return
  if (transitionTargetPath.value) finishAnimatedNavigation()
  else stopTrackAnimation()
}

function nextAnimationFrame() {
  return new Promise<void>(resolve => requestAnimationFrame(() => resolve()))
}

async function animateNavigate(direction: -1 | 1) {
  interruptTrackAnimation()
  if ((direction < 0 && !canPrevious.value) || (direction > 0 && !canNext.value)) return
  const entry = direction > 0 ? nextEntry.value : previousEntry.value
  if (!entry) return
  const request = ++navigationRequest
  const ready = await ensureOriginalReady(entry.path)
  if (request !== navigationRequest || !ready || (direction < 0 && previousEntry.value?.path !== entry.path) || (direction > 0 && nextEntry.value?.path !== entry.path)) return
  const width = viewport.value?.clientWidth ?? 1
  const targetOffset = direction > 0 ? -width : width
  transitionDurationMs.value = swipeTransitionDuration(swipeOffset.value, targetOffset, width)
  transitionTargetPath.value = entry.path
  syncActiveThumbnail()
  if (reducedMotion.value) {
    swipeOffset.value = targetOffset
    finishAnimatedNavigation()
    return
  }
  trackAnimating.value = true
  await nextTick()
  track.value?.getBoundingClientRect()
  await nextAnimationFrame()
  if (request !== navigationRequest || transitionTargetPath.value !== entry.path) return
  swipeOffset.value = targetOffset
  if (navigationTimer !== null) clearTimeout(navigationTimer)
  navigationTimer = setTimeout(() => {
    finishAnimatedNavigation()
  }, transitionDurationMs.value + 150)
}

function navigateTo(path: string) {
  interruptTrackAnimation()
  if (path === currentPath.value) return
  const targetIndex = images.value.findIndex(entry => entry.path === path)
  if (targetIndex === imageIndex.value - 1) animateNavigate(-1)
  else if (targetIndex === imageIndex.value + 1) animateNavigate(1)
  else emit('navigate', path)
}

function syncActiveThumbnail() {
  if (!thumbnailsVisible.value) {
    thumbnailNeedsCenter = true
    return
  }
  thumbnailNeedsCenter = false
  scrollActiveThumbnailIntoView()
}

function scrollActiveThumbnailIntoView(animate = true) {
  void nextTick(() => {
    const strip = thumbnailStrip.value
    const item = strip?.querySelector<HTMLElement>('[data-active-thumbnail="true"]')
    if (!strip || !item) return
    const itemBounds = item.getBoundingClientRect()
    const stripBounds = strip.getBoundingClientRect()
    const left = strip.scrollLeft + itemBounds.left + itemBounds.width / 2 - (stripBounds.left + stripBounds.width / 2)
    strip.scrollTo({ left, behavior: animate && !reducedMotion.value ? 'smooth' : 'auto' })
  })
}

function scrollThumbnails(event: WheelEvent) {
  const strip = thumbnailStrip.value
  if (!strip || strip.scrollWidth <= strip.clientWidth) return
  const delta = Math.abs(event.deltaX) > Math.abs(event.deltaY) ? event.deltaX : event.deltaY
  if (delta === 0) return
  const maximum = strip.scrollWidth - strip.clientWidth
  const target = Math.min(maximum, Math.max(0, strip.scrollLeft + delta))
  if (target === strip.scrollLeft) return
  event.preventDefault()
  strip.scrollLeft = target
}

async function toggleFullscreen() {
  if (!root.value) return
  fullscreenControlsVisible.value = true
  fullscreenControlsGesture = null
  if (immersive.value) {
    immersive.value = false
    if (document.fullscreenElement === root.value) await document.exitFullscreen().catch(() => undefined)
  } else {
    immersive.value = true
    if (root.value.requestFullscreen) await root.value.requestFullscreen().catch(() => undefined)
  }
  refitAfterLayout()
}

function onFullscreenChange() {
  if (document.fullscreenElement !== root.value) {
    immersive.value = false
    fullscreenControlsVisible.value = true
    fullscreenControlsGesture = null
  }
  refitAfterLayout()
}

function refitAfterLayout() {
  requestAnimationFrame(() => requestAnimationFrame(() => {
    if (fitMode.value) fitImage()
    else clampPan()
  }))
}

function onKeyDown(event: KeyboardEvent) {
  if (props.active === false) return
  if (event.key === 'Escape' && immersive.value) void toggleFullscreen()
  else if (event.key === 'Escape') emit('back')
  else if (event.key === 'ArrowLeft') animateNavigate(-1)
  else if (event.key === 'ArrowRight') animateNavigate(1)
  else if (event.key === 'Home' && images.value[0]) navigateTo(images.value[0].path)
  else if (event.key === 'End' && images.value.at(-1)) navigateTo(images.value.at(-1)!.path)
  else if (event.key === '0') fitImage()
  else if (event.key === '+' || event.key === '=') zoomBy(0.25)
  else if (event.key === '-') zoomBy(-0.25)
}
</script>

<template>
  <section ref="root" class="gallery-root absolute inset-0 z-40 flex min-h-0 flex-col bg-zinc-950 text-zinc-100" aria-label="File preview" aria-roledescription="image gallery" :data-immersive="immersive">
    <header v-show="!immersive" class="flex h-14 shrink-0 items-center gap-3 border-b border-zinc-800 bg-zinc-900 px-3">
      <Button v-tooltip.bottom="'Back to folder'" icon="pi pi-arrow-left" severity="secondary" text rounded aria-label="Back to folder" @click="emit('back')" />
      <div class="min-w-0">
        <div class="truncate font-semibold">{{ name }}</div>
        <div class="truncate text-xs text-zinc-400">{{ currentPath }}</div>
      </div>
      <div class="ml-auto flex items-center gap-1">
        <Button v-tooltip.bottom="'Zoom out'" icon="pi pi-search-minus" severity="secondary" text :disabled="scale <= 0.1" aria-label="Zoom out" @click="zoomBy(-0.25)" />
        <span class="min-w-12 text-center text-xs text-zinc-400" aria-label="Zoom level">{{ zoomLevel }}</span>
        <Button v-tooltip.bottom="'Zoom in'" icon="pi pi-search-plus" severity="secondary" text :disabled="scale >= 8" aria-label="Zoom in" @click="zoomBy(0.25)" />
        <Button v-tooltip.bottom="'Actual size'" icon="pi pi-stop" severity="secondary" text aria-label="Actual size" @click="actualSize" />
        <Button v-tooltip.bottom="'Fit to window'" icon="pi pi-expand" severity="secondary" text aria-label="Fit to window" @click="fitImage" />
        <span class="webfile-divider mx-2 h-6 w-px" />
        <Button v-tooltip.bottom="'Previous image'" icon="pi pi-chevron-left" severity="secondary" text :disabled="!canPrevious" aria-label="Previous image" @click="animateNavigate(-1)" />
        <span class="min-w-16 text-center text-xs text-zinc-400" aria-live="polite">{{ activeThumbnailIndex + 1 }} / {{ images.length }}</span>
        <Button v-tooltip.bottom="'Next image'" icon="pi pi-chevron-right" severity="secondary" text :disabled="!canNext" aria-label="Next image" @click="animateNavigate(1)" />
        <Button v-tooltip.bottom="thumbnailsVisible ? 'Hide thumbnails' : 'Show thumbnails'" icon="pi pi-images" severity="secondary" text :aria-label="thumbnailsVisible ? 'Hide thumbnails' : 'Show thumbnails'" @click="toggleThumbnails" />
        <span class="webfile-divider mx-2 h-6 w-px" />
        <Button v-tooltip.bottom="'Fullscreen'" icon="pi pi-window-maximize" severity="secondary" text aria-label="Enter fullscreen" @click="toggleFullscreen" />
      </div>
    </header>

    <nav
      v-if="immersive && (!touchEnabled || fullscreenControlsVisible)"
      class="gallery-fullscreen-controls absolute left-1/2 top-3 z-50 flex max-w-[calc(100%-7rem)] -translate-x-1/2 items-center gap-1 overflow-x-auto rounded-lg border border-zinc-700 bg-zinc-900/90 px-2 py-1 shadow-lg backdrop-blur"
      aria-label="Fullscreen image controls"
      @pointerdown="onFullscreenControlsPointerDown"
      @pointermove="onFullscreenControlsPointerMove"
      @pointerup="onFullscreenControlsPointerUp"
      @pointercancel="onFullscreenControlsPointerCancel"
    >
      <Button v-tooltip.bottom="'Zoom out'" icon="pi pi-search-minus" severity="secondary" text :disabled="scale <= 0.1" aria-label="Zoom out" @click.stop="zoomBy(-0.25)" />
      <span class="min-w-12 text-center text-xs text-zinc-300" aria-label="Zoom level">{{ zoomLevel }}</span>
      <Button v-tooltip.bottom="'Zoom in'" icon="pi pi-search-plus" severity="secondary" text :disabled="scale >= 8" aria-label="Zoom in" @click.stop="zoomBy(0.25)" />
      <Button v-tooltip.bottom="'Actual size'" icon="pi pi-stop" severity="secondary" text aria-label="Actual size" @click.stop="actualSize" />
      <Button v-tooltip.bottom="'Fit to window'" icon="pi pi-expand" severity="secondary" text aria-label="Fit to window" @click.stop="fitImage" />
      <span class="webfile-divider mx-2 h-6 w-px shrink-0" />
      <Button v-tooltip.bottom="'Previous image'" icon="pi pi-chevron-left" severity="secondary" text :disabled="!canPrevious" aria-label="Previous image" @click.stop="animateNavigate(-1)" />
      <span class="min-w-16 text-center text-xs text-zinc-300" aria-live="polite">{{ activeThumbnailIndex + 1 }} / {{ images.length }}</span>
      <Button v-tooltip.bottom="'Next image'" icon="pi pi-chevron-right" severity="secondary" text :disabled="!canNext" aria-label="Next image" @click.stop="animateNavigate(1)" />
      <Button v-tooltip.bottom="thumbnailsVisible ? 'Hide thumbnails' : 'Show thumbnails'" icon="pi pi-images" severity="secondary" text :aria-label="thumbnailsVisible ? 'Hide thumbnails' : 'Show thumbnails'" @click.stop="toggleThumbnails" />
    </nav>
    <div v-if="immersive" class="absolute right-3 top-3 z-50 flex items-center gap-1">
      <Button v-if="touchEnabled && !fullscreenControlsVisible" v-tooltip.left="'Show image controls'" icon="pi pi-sliders-h" severity="secondary" rounded class="border border-zinc-700 bg-black/65 shadow-lg" aria-label="Show fullscreen image controls" @click.stop="fullscreenControlsVisible = true" />
      <Button v-tooltip.left="'Exit fullscreen'" icon="pi pi-times" severity="secondary" rounded class="border border-zinc-700 bg-black/65 shadow-lg" aria-label="Exit fullscreen" @click.stop="toggleFullscreen" />
    </div>

    <div
      class="gallery-image-viewport relative min-h-0 flex-1 overflow-hidden bg-black"
      data-gallery-gesture-surface
      @pointerdown="onPointerDown"
      @pointermove="onPointerMove"
      @pointerup="onPointerUp"
      @pointercancel="onPointerCancel"
    >
      <div
        ref="track"
        class="absolute inset-0 flex h-full min-h-0 max-h-full w-full will-change-transform"
        :style="trackStyle"
        :data-gallery-animating="trackAnimating"
        :data-gallery-transition-ms="transitionDurationMs"
        data-gallery-track
        @transitionend="onTrackTransitionEnd"
      >
        <div class="grid h-full min-h-0 max-h-full min-w-full place-items-center overflow-hidden p-4" aria-hidden="true">
          <img v-if="previousEntry" :src="api.previewUrl(previousEntry.path)" :style="carouselImageStyle(previousEntry.path)" alt="" class="max-h-none max-w-none select-none object-contain" draggable="false" data-gallery-adjacent="previous" @load="onCarouselImageLoad(previousEntry.path, $event)" />
        </div>
        <div
          ref="viewport"
          class="gallery-image-viewport relative grid h-full min-h-0 max-h-full min-w-full cursor-grab place-items-center overflow-auto p-4 active:cursor-grabbing"
          data-image-viewport
          @wheel.prevent="zoomWithWheel"
          @dblclick.prevent
        >
          <i v-if="!loaded && !mediaError" class="pi pi-spinner pi-spin col-start-1 row-start-1 text-2xl text-zinc-500" aria-label="Loading image" />
          <img
            v-if="!mediaError"
            ref="previewImage"
            :src="source"
            :alt="name"
            :style="imageStyle"
            :data-image-loaded="loaded"
            class="col-start-1 row-start-1 max-h-none max-w-none select-none object-contain"
            draggable="false"
            @load="onImageLoad"
            @error="mediaError = true"
          />
          <div v-else class="max-w-lg text-center text-zinc-400">
            <i class="pi pi-exclamation-triangle mb-4 text-5xl text-zinc-600" />
            <h2 class="mb-2 text-base font-semibold text-zinc-200">Preview unavailable</h2>
            <p class="mb-4 text-sm">The browser could not display this image. Use the arrows or thumbnail strip to continue.</p>
          </div>
        </div>
        <div class="grid h-full min-h-0 max-h-full min-w-full place-items-center overflow-hidden p-4" aria-hidden="true">
          <img v-if="nextEntry" :src="api.previewUrl(nextEntry.path)" :style="carouselImageStyle(nextEntry.path)" alt="" class="max-h-none max-w-none select-none object-contain" draggable="false" data-gallery-adjacent="next" @load="onCarouselImageLoad(nextEntry.path, $event)" />
        </div>
      </div>
    </div>

    <footer v-show="thumbnailsVisible" class="webfile-gallery-footer shrink-0 overflow-hidden border-t border-zinc-800 px-2 py-2" data-thumbnail-strip>
      <div class="mb-1 flex items-center justify-between px-1 text-[11px] text-zinc-500">
        <span>Gallery</span>
        <span>{{ thumbnailsLoaded }} / {{ images.length }} thumbnails</span>
      </div>
      <div ref="thumbnailStrip" class="gallery-thumbnail-strip flex min-h-14 gap-2 overflow-x-auto overflow-y-hidden overscroll-x-contain" style="padding-inline: calc(50% - 2.5rem)" role="listbox" aria-label="Image thumbnails" @wheel="scrollThumbnails">
        <button
          v-for="entry in images"
          :key="entry.path"
          type="button"
          class="relative h-14 w-20 shrink-0 overflow-hidden rounded border bg-zinc-950 outline-none transition-colors focus-visible:ring-2 focus-visible:ring-emerald-400"
          :class="entry.path === activeThumbnailPath ? 'border-emerald-400 ring-1 ring-emerald-400' : 'border-zinc-700 hover:border-zinc-500'"
          role="option"
          :aria-label="`Show ${entry.name}`"
          :aria-selected="entry.path === activeThumbnailPath"
          :data-active-thumbnail="entry.path === activeThumbnailPath"
          @click="navigateTo(entry.path)"
        >
          <img v-if="thumbnailStates[entry.path] === 'loaded'" :src="api.thumbnailUrl(entry.path)" alt="" class="h-full w-full object-cover" draggable="false" />
          <i v-else-if="thumbnailStates[entry.path] === 'failed'" class="pi pi-refresh text-zinc-500" title="Retry thumbnail" @click.stop="retryThumbnail(entry.path)" />
          <i v-else class="pi pi-spinner pi-spin text-zinc-600" aria-hidden="true" />
        </button>
      </div>
    </footer>
  </section>
</template>

<style scoped>
.gallery-root {
  touch-action: pan-x pan-y;
}

.gallery-image-viewport {
  touch-action: none;
}

.gallery-fullscreen-controls {
  scrollbar-width: none;
  -ms-overflow-style: none;
  touch-action: pan-x;
}

.gallery-fullscreen-controls::-webkit-scrollbar {
  display: none;
}

.gallery-thumbnail-strip {
  scrollbar-color: rgb(82 82 91) transparent;
  scrollbar-width: thin;
  touch-action: pan-x;
}

.gallery-thumbnail-strip::-webkit-scrollbar {
  height: 0.5rem;
}

.gallery-thumbnail-strip::-webkit-scrollbar-thumb {
  border: 2px solid transparent;
  border-radius: 9999px;
  background: rgb(82 82 91);
  background-clip: padding-box;
}
</style>
