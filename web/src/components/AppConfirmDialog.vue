<script setup lang="ts">
import { ref } from 'vue'
import Button from 'primevue/button'
import AppDialog from './AppDialog.vue'
import { confirmation } from '@/composables/useAppConfirm'

const busy = ref(false)
const error = ref('')
function cancel() {
  if (!busy.value) { confirmation.value = null; error.value = '' }
}
async function accept() {
  if (busy.value || !confirmation.value) return
  busy.value = true
  error.value = ''
  try {
    if (await confirmation.value.accept() !== false) confirmation.value = null
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <AppDialog :visible="!!confirmation" :header="confirmation?.header" :busy="busy" modal confirmation role="alertdialog"
    :style="{ width: 'min(34rem, 92vw)' }" @update:visible="cancel">
    <div class="flex items-center gap-3"><i v-if="confirmation?.icon" :class="confirmation.icon" aria-hidden="true" /><p>{{ confirmation?.message }}</p></div>
    <p v-if="error" role="alert" class="webfile-text-error mt-3">{{ error }}</p>
    <template #footer>
      <Button data-dialog-cancel :label="confirmation?.rejectLabel" severity="secondary" :disabled="busy" @click="cancel" />
      <Button data-dialog-primary :label="confirmation?.acceptLabel" :severity="confirmation?.acceptProps?.severity" :loading="busy" :disabled="busy" @click="accept" />
    </template>
  </AppDialog>
</template>
