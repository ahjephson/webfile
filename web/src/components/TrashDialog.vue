<script setup lang="ts">
import { ref, watch } from 'vue'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import ProgressSpinner from 'primevue/progressspinner'
import { api } from '@/api/client'
import type { TrashItem } from '@/types/files'

const props = defineProps<{ visible: boolean; refreshToken?: number }>()
const emit = defineEmits<{
  'update:visible': [value: boolean]
  changed: []
}>()

const items = ref<TrashItem[]>([])
const loading = ref(false)
const busyId = ref('')
const error = ref('')

watch(() => props.visible, visible => {
  if (visible) void load()
})
watch(() => props.refreshToken, () => {
  if (props.visible) void load()
})

async function load() {
  loading.value = true
  error.value = ''
  try {
    items.value = (await api.trash()).items
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    loading.value = false
  }
}

async function restore(item: TrashItem) {
  busyId.value = item.id
  try {
    await api.restoreTrash(item.id)
    await load()
    emit('changed')
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    busyId.value = ''
  }
}

async function purge(item: TrashItem) {
  if (!window.confirm(`Permanently delete “${item.name}”? This cannot be undone.`)) return
  busyId.value = item.id
  try {
    await api.purgeTrash(item.id)
    await load()
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    busyId.value = ''
  }
}
function formatDate(value: string): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
}

</script>

<template>
  <Dialog :visible="visible" modal header="Recycle Bin" :style="{ width: 'min(54rem, 94vw)' }" @update:visible="emit('update:visible', $event)">
    <p v-if="error" class="mb-3 rounded bg-red-950/50 px-3 py-2 text-sm text-red-200">{{ error }}</p>
    <div v-if="loading" class="grid min-h-48 place-items-center"><ProgressSpinner class="h-9 w-9" /></div>
    <div v-else-if="items.length === 0" class="grid min-h-48 place-items-center text-zinc-500">Recycle Bin is empty.</div>
    <div v-else class="max-h-[60vh] overflow-auto divide-y divide-zinc-800">
      <div v-for="item in items" :key="item.id" class="flex items-center gap-3 py-3">
        <i :class="item.is_dir ? 'pi pi-folder text-amber-400' : 'pi pi-file text-zinc-400'" class="text-xl" />
        <div class="min-w-0 flex-1">
          <div class="truncate text-sm font-medium">{{ item.name }}</div>
          <div class="truncate text-xs text-zinc-500">{{ item.original_path }} · {{ formatDate(item.deleted_at) }}</div>
        </div>
        <Button label="Restore" size="small" severity="secondary" :loading="busyId === item.id" @click="restore(item)" />
        <Button icon="pi pi-times" size="small" severity="danger" text aria-label="Delete permanently" :disabled="busyId === item.id" @click="purge(item)" />
      </div>
    </div>
  </Dialog>
</template>
