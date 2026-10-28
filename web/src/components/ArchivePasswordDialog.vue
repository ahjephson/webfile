<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import Password from 'primevue/password'

const props = defineProps<{
  visible: boolean
  archiveName: string
  busy?: boolean
  error?: string
}>()
const emit = defineEmits<{
  'update:visible': [value: boolean]
  submit: [password: string]
  cancel: []
  dismiss: []
  'after-hide': []
}>()
const password = ref('')
const passwordInput = ref<any>(null)

watch(() => props.visible, visible => {
  if (!visible) return
  password.value = ''
  void nextTick(() => passwordInput.value?.$el?.querySelector('input')?.focus())
})

function submit() {
  if (password.value && !props.busy) emit('submit', password.value)
}

function updateVisible(value: boolean) {
  if (!value) {
    emit('cancel')
    return
  }
  emit('update:visible', true)
}
</script>

<template>
  <Dialog
    :visible="visible"
    modal
    :header="`Password required — ${archiveName}`"
    :style="{ width: 'min(30rem, 92vw)' }"
    @update:visible="updateVisible"
    @after-hide="emit('after-hide')"
  >
    <form class="space-y-4" autocomplete="off" @submit.prevent="submit">
      <p class="text-sm text-zinc-300">This archive is encrypted. Enter its password to continue extraction.</p>
      <p v-if="error" class="webfile-text-error text-sm" role="alert">{{ error }}</p>
      <div class="space-y-2">
        <label for="archive-password" class="block text-sm font-medium">Password</label>
        <Password
          input-id="archive-password"
          ref="passwordInput"
          v-model="password"
          fluid
          :feedback="false"
          toggle-mask
          :input-props="{ autocomplete: 'off', name: 'archive-passphrase', spellcheck: false }"
        />
      </div>
      <div class="flex justify-end gap-2">
        <Button label="Later" severity="secondary" text :disabled="busy" @click="emit('dismiss')" />
        <Button label="Cancel" severity="secondary" text :disabled="busy" @click="emit('cancel')" />
        <Button label="Continue" type="submit" :loading="busy" :disabled="busy || !password" />
      </div>
    </form>
  </Dialog>
</template>
