<script setup lang="ts">
import { ref, watch } from 'vue'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'

const props = defineProps<{
  visible: boolean
  archiveName: string
  destination: string
  busy?: boolean
  existingFiles: 'skip' | 'overwrite'
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  extract: [destination: string]
}>()

const destination = ref('')

watch(() => props.visible, visible => {
  if (visible) {
    destination.value = props.destination
  }
})

function submit() {
  emit('extract', destination.value.trim())
}
</script>

<template>
  <Dialog
    :visible="visible"
    modal
    :header="`Extract ${archiveName}`"
    :style="{ width: 'min(34rem, 92vw)' }"
    @update:visible="emit('update:visible', $event)"
  >
    <form class="space-y-4" @submit.prevent="submit">
      <div class="space-y-2">
        <label for="extract-destination" class="block text-sm font-medium">Destination folder</label>
        <InputText id="extract-destination" v-model="destination" fluid autocomplete="off" />
      </div>
      <p class="text-xs text-zinc-400">
        Each archive will be extracted into its own named folder. Existing regular files will be
        {{ existingFiles === 'overwrite' ? 'overwritten' : 'skipped' }}. Encrypted archives request a password when they reach the front of the queue.
      </p>
      <div class="flex justify-end gap-2">
        <Button label="Cancel" severity="secondary" text :disabled="busy" @click="emit('update:visible', false)" />
        <Button label="Extract" type="submit" :loading="busy" :disabled="!destination.trim()" />
      </div>
    </form>
  </Dialog>
</template>
