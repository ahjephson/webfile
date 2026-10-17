<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, provide, ref, watch } from 'vue'
import FileExplorer from '@/components/FileExplorer.vue'
import { createExplorerWorkspace, explorerWorkspaceKey, type PaneId } from '@/composables/useExplorerWorkspace'
import { provideFileOperations } from '@/composables/useFileOperations'

const props = defineProps<{
  username: string
  showLogout: boolean
  elevationAvailable: boolean
  elevated: boolean
  elevationExpiresAt?: string
}>()
const emit = defineEmits<{ logout: []; requestElevation: []; dropElevation: [] }>()
const workspace = createExplorerWorkspace()
provide(explorerWorkspaceKey, workspace)
provideFileOperations()
const layoutKey = `webfile-workspace:${props.username}`
const rightMounted = ref(false)
const ratio = ref(50)
const leadingPane = ref<PaneId>('left')
const paneOrder = computed<PaneId[]>(() => [leadingPane.value, leadingPane.value === 'left' ? 'right' : 'left'])
const host = ref<HTMLElement | null>(null)
const narrowViewport = window.matchMedia('(max-width: 639px)')
const stacked = ref(narrowViewport.matches)
const updateOrientation = () => { stacked.value = narrowViewport.matches }
onMounted(() => narrowViewport.addEventListener('change', updateOrientation))
onBeforeUnmount(() => narrowViewport.removeEventListener('change', updateOrientation))
try {
  const saved = JSON.parse(sessionStorage.getItem(layoutKey) ?? 'null')
  if (saved) {
    workspace.split.value = saved.split === true
    workspace.activePane.value = saved.active === 'right' ? 'right' : 'left'
    leadingPane.value = workspace.split.value ? (saved.leading === 'right' ? 'right' : 'left') : workspace.activePane.value
    ratio.value = Math.max(20, Math.min(80, Number(saved.ratio) || 50))
    rightMounted.value = workspace.split.value || workspace.activePane.value === 'right'
  }
} catch { /* A new tab starts with one pane. */ }

watch([workspace.split, workspace.activePane, ratio, leadingPane], () => {
  try { sessionStorage.setItem(layoutKey, JSON.stringify({ split: workspace.split.value, active: workspace.activePane.value, ratio: ratio.value, leading: leadingPane.value })) }
  catch { /* Browsing also works when storage is unavailable. */ }
})

workspace.toggleSplit = () => {
  if (workspace.split.value) {
    const hidden: PaneId = workspace.activePane.value === 'left' ? 'right' : 'left'
    if (workspace.panes.get(hidden)?.canHide() === false) return
    workspace.split.value = false
  } else {
    leadingPane.value = workspace.activePane.value
    rightMounted.value = true
    workspace.split.value = true
  }
}

function resize(event: PointerEvent) {
  const divider = event.currentTarget as HTMLElement
  divider.setPointerCapture(event.pointerId)
  divider.addEventListener('pointermove', move)
  divider.addEventListener('pointerup', stop, { once: true })
  divider.addEventListener('pointercancel', stop, { once: true })
  function move(next: PointerEvent) {
    const box = host.value?.getBoundingClientRect()
    if (!box) return
    const value = stacked.value ? (next.clientY - box.top) / box.height : (next.clientX - box.left) / box.width
    ratio.value = Math.max(20, Math.min(80, value * 100))
  }
  function stop() {
    divider.removeEventListener('pointermove', move)
    divider.removeEventListener('pointerup', stop)
    divider.removeEventListener('pointercancel', stop)
  }
}

function resizeKey(event: KeyboardEvent) {
  const delta = ['ArrowLeft', 'ArrowUp'].includes(event.key) ? -5 : ['ArrowRight', 'ArrowDown'].includes(event.key) ? 5 : 0
  if (!delta && event.key !== 'Home' && event.key !== 'End') return
  event.preventDefault()
  ratio.value = event.key === 'Home' ? 20 : event.key === 'End' ? 80 : Math.max(20, Math.min(80, ratio.value + delta))
}

onBeforeUnmount(() => { workspace.drag.value = null })
</script>

<template>
  <section class="flex h-full min-h-0 flex-col bg-zinc-950" aria-label="File workspace">
    <div id="webfile-workspace-toolbar" class="shrink-0" />
    <div ref="host" class="webfile-panes flex min-h-0 flex-1" :data-split="workspace.split.value">
      <template v-for="(pane, index) in paneOrder" :key="pane">
        <FileExplorer
          v-if="pane === 'left' || rightMounted" v-show="workspace.split.value || workspace.activePane.value === pane"
          v-bind="props" :pane-id="pane" :pane-position="index === 0 ? 'left' : 'right'" :pane-visible="workspace.split.value || workspace.activePane.value === pane"
          class="min-w-0" :style="{ flex: workspace.split.value && index === 0 ? `0 0 calc(${ratio}% - 4px)` : '1 1 0%' }"
          @logout="emit('logout')" @request-elevation="emit('requestElevation')" @drop-elevation="emit('dropElevation')"
        />
        <div v-if="workspace.split.value && index === 0" class="webfile-pane-divider shrink-0 bg-zinc-800 hover:bg-sky-600 focus:bg-sky-600"
          role="separator" tabindex="0" aria-label="Resize panes" :aria-orientation="stacked ? 'horizontal' : 'vertical'"
          aria-valuemin="20" aria-valuemax="80" :aria-valuenow="Math.round(ratio)"
          @pointerdown="resize" @keydown="resizeKey" />
      </template>
    </div>
  </section>
</template>

<style scoped>
.webfile-pane-divider { width: 8px; cursor: col-resize; touch-action: none; }
@media (max-width: 639px) {
  .webfile-panes { flex-direction: column; }
  .webfile-pane-divider { width: 100%; height: 8px; cursor: row-resize; }
}
</style>
