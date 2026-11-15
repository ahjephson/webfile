<script setup lang="ts">
import { ref, watch } from 'vue'
import Button from 'primevue/button'
import Dialog from '@/components/AppDialog.vue'
import Password from 'primevue/password'

const props = defineProps<{
  visible: boolean
  archiveName: string
  busy?: boolean
  error?: string
  purpose?: 'extract' | 'preview'
}>()
const emit = defineEmits<{
  'update:visible': [value: boolean]
  submit: [password: string]
  cancel: []
  dismiss: []
  'after-hide': []
}>()
const password = ref('')

watch(() => props.visible, () => {
  password.value = ''
})

function submit() {
  if (password.value && !props.busy) {
    emit('submit', password.value)
    password.value = ''
  }
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
    :busy="busy"
    :visible="visible"
    modal
    :header="`Password required — ${archiveName}`"
    :style="{ width: 'min(30rem, 92vw)' }"
    @update:visible="updateVisible"
    @after-hide="emit('after-hide')"
  >
    <form class="space-y-4" autocomplete="off" @submit.prevent="submit">
      <p class="text-sm text-zinc-300">{{ purpose === 'preview' ? 'Enter the password to view this archive’s contents.' : 'This archive is encrypted. Enter its password to continue extraction.' }}</p>
      <p v-if="error" class="webfile-text-error text-sm" role="alert">{{ error }}</p>
      <div class="space-y-2">
        <label for="archive-password" class="block text-sm font-medium">Password</label>
        <Password
          input-id="archive-password"
          v-model="password"
          fluid
          :feedback="false"
          toggle-mask
          :input-props="{ autocomplete: 'off', name: 'archive-passphrase', spellcheck: false }"
        />
      </div>
      <div class="flex justify-end gap-2">
        <Button v-if="purpose !== 'preview'" label="Later" severity="secondary" text :disabled="busy" @click="emit('dismiss')" />
        <Button data-dialog-cancel label="Cancel" severity="secondary" text :disabled="busy" @click="emit('cancel')" />
        <Button data-dialog-primary label="Continue" type="submit" :loading="busy" :disabled="busy || !password" />
      </div>
    </form>
  </Dialog>
</template>
