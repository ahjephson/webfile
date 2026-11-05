<script setup lang="ts">
import { ref, watch } from 'vue'
import Button from 'primevue/button'
import Dialog from '@/components/AppDialog.vue'
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

watch(() => props.visible, visible => {
  if (!visible) {
    password.value = ''
    return
  }
})

function submit() {
  if (!password.value || props.busy) return
  emit('submit', password.value)
}
</script>

<template>
  <Dialog
    :busy="props.busy"
    :visible="props.visible"
    modal
    header="Elevate access"
    :style="{ width: 'min(28rem, 94vw)' }"
    @update:visible="emit('update:visible', $event)"
  >
    <form class="space-y-4" @submit.prevent="submit">
      <div class="webfile-alert-warning rounded-lg border p-3 text-sm">
        <div class="flex gap-3">
          <i class="webfile-accent-warning pi pi-shield mt-0.5" />
          <p>Temporarily run file operations with root permissions. Access expires automatically and can be dropped at any time.</p>
        </div>
      </div>
      <Message v-if="props.error" severity="error">{{ props.error }}</Message>
      <div class="space-y-2">
        <label for="elevation-password" class="block text-sm font-medium">Password for {{ props.username }}</label>
        <Password
          id="elevation-password"
          v-model="password"
          fluid
          :feedback="false"
          toggle-mask
          autocomplete="current-password"
        />
      </div>
      <div class="flex justify-end gap-2">
        <Button data-dialog-cancel type="button" label="Cancel" severity="secondary" text :disabled="props.busy" @click="emit('update:visible', false)" />
        <Button data-dialog-primary type="submit" label="Elevate access" icon="pi pi-shield" :loading="props.busy" :disabled="props.busy || !password" />
      </div>
    </form>
  </Dialog>
</template>
