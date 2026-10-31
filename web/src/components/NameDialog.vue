<script setup lang="ts">
import { ref, watch } from 'vue'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'

const props = defineProps<{
  visible: boolean
  title: string
  label: string
  initialValue?: string
  submitLabel?: string
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  submit: [value: string]
}>()

const value = ref('')
const nameForm = ref<HTMLFormElement | null>(null)

function focusName() {
  const input = nameForm.value?.querySelector('input')
  input?.focus()
  input?.select()
}

watch(() => props.visible, visible => {
  if (visible) value.value = props.initialValue ?? ''
})

function submit() {
  const next = value.value
  if (!next) return
  emit('submit', next)
  emit('update:visible', false)
}
</script>

<template>
  <Dialog
    :visible="visible"
    modal
    :header="title"
    :style="{ width: 'min(30rem, 92vw)' }"
    @show="focusName"
    @update:visible="emit('update:visible', $event)"
  >
    <form ref="nameForm" class="space-y-4" @submit.prevent="submit">
      <div class="space-y-2">
        <label class="block text-sm font-medium">{{ label }}</label>
        <InputText v-model="value" autofocus fluid autocomplete="off" />
      </div>
      <div class="flex justify-end gap-2">
        <Button label="Cancel" severity="secondary" text @click="emit('update:visible', false)" />
        <Button :label="submitLabel ?? 'Save'" type="submit" :disabled="!value" />
      </div>
    </form>
  </Dialog>
</template>
