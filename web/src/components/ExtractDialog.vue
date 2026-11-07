<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { archiveFolderName } from '@/utils/archives'
import Button from 'primevue/button'
import Dialog from '@/components/AppDialog.vue'
import InputText from 'primevue/inputtext'
import DestinationDialog from '@/components/DestinationDialog.vue'

const props = defineProps<{
  visible: boolean
  archiveName: string
  archiveNames: string[]
  destination: string
  busy?: boolean
  existingFiles: 'skip' | 'overwrite'
  showHidden: boolean
  hideInaccessible: boolean
  deleteArchive: boolean
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  extract: [destination: string]
}>()

const destination = ref('')
const browseVisible = ref(false)
const outputPaths = computed(() => {
  if (!destination.value.trim()) return []
  return props.archiveNames.map(name => {
    const parts: string[] = []
    for (const part of `${destination.value.trim()}/${archiveFolderName(name)}`.split('/')) {
      if (part === '..') parts.pop()
      else if (part && part !== '.') parts.push(part)
    }
    return `/${parts.join('/')}`
  })
})

watch(() => props.visible, visible => {
  browseVisible.value = false
  if (visible) {
    destination.value = props.destination
  }
})

function submit() {
  if (!props.busy && destination.value.trim()) emit('extract', destination.value.trim())
}

function selectDestination(path: string) {
  destination.value = path
  browseVisible.value = false
}
</script>

<template>
  <Dialog
    :busy="busy"
    :visible="visible"
    modal
    :header="`Extract ${archiveName}`"
    :style="{ width: 'min(34rem, 92vw)' }"
    @update:visible="emit('update:visible', $event)"
  >
    <form class="space-y-4" @submit.prevent="submit">
      <div class="space-y-2">
        <label for="extract-destination" class="block text-sm font-medium">Destination folder</label>
        <div class="flex gap-2">
          <InputText id="extract-destination" v-model="destination" class="min-w-0 flex-1" autocomplete="off" :disabled="busy" />
          <Button label="Browse…" icon="pi pi-folder-open" severity="secondary" :disabled="busy" @click="browseVisible = true" />
        </div>
      </div>
      <section v-if="outputPaths.length" aria-label="Extraction folders" class="space-y-2 rounded-lg border border-zinc-700 bg-zinc-900/50 p-3">
        <p class="text-sm font-medium">{{ outputPaths.length === 1 ? 'Extract into folder' : 'Extract into folders' }}</p>
        <ul class="space-y-1 text-sm text-zinc-300">
          <li v-for="(path, index) in outputPaths" :key="index" class="flex items-start gap-2"><i class="pi pi-folder mt-0.5 shrink-0" aria-hidden="true" /><span class="break-all">{{ path }}</span></li>
        </ul>
      </section>
      <p class="text-xs text-zinc-400">
        Each archive will be extracted into its own named folder. Existing regular files will be
        {{ existingFiles === 'overwrite' ? 'overwritten' : 'skipped' }}. Encrypted archives request a password when they reach the front of the queue.
      </p>
      <p v-if="deleteArchive" class="text-xs text-zinc-400">Source archives will be deleted after successful extraction. Archives are kept if any files are skipped.</p>
      <div class="flex justify-end gap-2">
        <Button data-dialog-cancel label="Cancel" severity="secondary" text :disabled="busy" @click="emit('update:visible', false)" />
        <Button data-dialog-primary label="Extract" type="submit" :loading="busy" :disabled="busy || !destination.trim()" />
      </div>
    </form>
  </Dialog>
  <DestinationDialog
    v-model:visible="browseVisible"
    mode="select"
    :count="archiveNames.length"
    :initial-path="destination.trim() || props.destination"
    :show-hidden="showHidden"
    :hide-inaccessible="hideInaccessible"
    @submit="selectDestination"
  />
</template>
