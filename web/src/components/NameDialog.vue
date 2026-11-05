<script setup lang="ts">
import { ref, watch } from 'vue'
import Button from 'primevue/button'
import Dialog from '@/components/AppDialog.vue'
import InputText from 'primevue/inputtext'

const props = defineProps<{
  visible: boolean
  title: string
  label: string
  initialValue?: string
  submitLabel?: string
  busy?: boolean
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  submit: [value: string]
}>()

const value = ref('')

watch(() => props.visible, visible => {
  if (visible) value.value = props.initialValue ?? ''
})

function submit() {
  const next = value.value
  if (!next || props.busy) return
  emit('submit', next)
}
</script>

<template>
  <Dialog
    :visible="visible"
    :busy="busy"
    modal
    :header="title"
    :style="{ width: 'min(30rem, 92vw)' }"
    @update:visible="emit('update:visible', $event)"
  >
    <form class="space-y-4" @submit.prevent="submit">
      <div class="space-y-2">
        <label class="block text-sm font-medium">{{ label }}</label>
        <InputText v-model="value" data-select-on-open fluid autocomplete="off" :disabled="busy" />
      </div>
      <div class="flex justify-end gap-2">
        <Button data-dialog-cancel label="Cancel" severity="secondary" text :disabled="busy" @click="emit('update:visible', false)" />
        <Button data-dialog-primary :label="submitLabel ?? 'Save'" type="submit" :loading="busy" :disabled="busy || !value" />
      </div>
    </form>
  </Dialog>
</template>
