<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import Message from 'primevue/message'
import Password from 'primevue/password'

const props = defineProps<{
  visible: boolean
  username: string
  busy: boolean
  error: string
}>()

const emit = defineEmits<{
  'update:visible': [visible: boolean]
  submit: [password: string]
}>()

const password = ref('')
const passwordInput = ref<any>(null)

watch(() => props.visible, visible => {
  if (!visible) {
    password.value = ''
    return
  }
  void nextTick(() => passwordInput.value?.$el?.querySelector('input')?.focus())
})

function submit() {
  if (!password.value || props.busy) return
  emit('submit', password.value)
}
</script>

<template>
  <Dialog
    :visible="props.visible"
    modal
    header="Elevate access"
    :style="{ width: 'min(28rem, 94vw)' }"
    @update:visible="emit('update:visible', $event)"
  >
    <form class="space-y-4" @submit.prevent="submit">
      <div class="rounded-lg border border-amber-900/70 bg-amber-950/30 p-3 text-sm text-amber-100">
        <div class="flex gap-3">
          <i class="pi pi-shield mt-0.5 text-amber-400" />
          <p>Temporarily run file operations with root permissions. Access expires automatically and can be dropped at any time.</p>
        </div>
      </div>
      <Message v-if="props.error" severity="error">{{ props.error }}</Message>
      <div class="space-y-2">
        <label for="elevation-password" class="block text-sm font-medium">Password for {{ props.username }}</label>
        <Password
          id="elevation-password"
          ref="passwordInput"
          v-model="password"
          fluid
          :feedback="false"
          toggle-mask
          autocomplete="current-password"
        />
      </div>
      <div class="flex justify-end gap-2">
        <Button type="button" label="Cancel" severity="secondary" text :disabled="props.busy" @click="emit('update:visible', false)" />
        <Button type="submit" label="Elevate access" icon="pi pi-shield" :loading="props.busy" :disabled="!password" />
      </div>
    </form>
  </Dialog>
</template>
