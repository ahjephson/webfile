<script setup lang="ts">
import { ref, watch } from 'vue'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'

const props = defineProps<{
  visible: boolean
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  create: [name: string]
}>()

const name = ref('')

watch(() => props.visible, visible => {
  if (visible) name.value = ''
})

function submit() {
  const value = name.value.trim()
  if (!value) return
  emit('create', value)
  emit('update:visible', false)
}
</script>

<template>
  <Dialog
    :visible="visible"
    modal
    header="New document"
    :style="{ width: 'min(32rem, 92vw)' }"
    @update:visible="emit('update:visible', $event)"
  >
    <form class="space-y-4" @submit.prevent="submit">
      <div class="space-y-2">
        <label for="new-document-name" class="block text-sm font-medium">Filename</label>
        <InputText
          id="new-document-name"
          v-model="name"
          autofocus
          fluid
          placeholder="notes.txt, LICENSE, app.conf…"
          autocomplete="off"
        />
        <p class="text-xs text-zinc-400">
          The complete filename is yours — extensions are optional and may be custom.
        </p>
      </div>
      <div class="flex justify-end gap-2">
        <Button label="Cancel" severity="secondary" text @click="emit('update:visible', false)" />
        <Button label="Open editor" type="submit" :disabled="!name.trim()" />
      </div>
    </form>
  </Dialog>
</template>
