<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import Button from 'primevue/button'
import Dialog from '@/components/AppDialog.vue'
import Checkbox from 'primevue/checkbox'
import type { ConflictChoice, ConflictDecision, FileOperation } from '@/types/files'
const props = defineProps<{ visible: boolean; operation: FileOperation | null; busy: boolean; error: string }>()
const emit = defineEmits<{ 'update:visible': [value: boolean]; submit: [decision: ConflictDecision]; cancel: [] }>()
const applyRemaining = ref(false)
watch(() => props.operation?.input?.revision, () => { applyRemaining.value = false })
const labels: Record<ConflictChoice, string> = { skip: 'Skip', keep_both: 'Keep both', replace: 'Replace', merge: 'Merge folders' }
const input = computed(() => props.operation?.input)
function choose(choice: ConflictChoice) {
  if (input.value && !props.busy) emit('submit', { revision: input.value.revision, choice, apply_remaining: applyRemaining.value })
}
</script>

<template>
  <Dialog :visible="visible && !!input" :busy="busy" confirmation modal header="Resolve transfer conflict" :style="{ width: 'min(52rem, 96vw)' }" :content-style="{ maxHeight: '70vh', overflow: 'auto' }" @update:visible="emit('update:visible', $event)">
    <template v-if="input">
      <p class="mb-4 text-sm text-zinc-400">An item already exists at the destination. Completed items are retained while this task waits for your decision.</p>
      <p v-if="input.changed" class="webfile-alert-error mb-3 rounded p-3">The source or destination changed. Review the updated details before continuing.</p>
      <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <section v-for="side in [{ title: 'Source', entry: input.source }, { title: 'Destination', entry: input.destination }]" :key="side.title" class="rounded border border-zinc-800 p-3">
          <h3 class="mb-2 font-semibold">{{ side.title }}</h3>
          <p class="break-all text-sm">{{ side.entry.path }}</p>
          <dl class="mt-3 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-sm text-zinc-400">
            <dt>Type</dt><dd>{{ side.entry.mode === 'unavailable' ? 'No longer available' : side.entry.is_dir ? 'Folder' : 'File' }}</dd>
            <dt>Size</dt><dd>{{ side.entry.is_dir || side.entry.mode === 'unavailable' ? '—' : `${side.entry.size.toLocaleString()} bytes` }}</dd>
            <dt>Modified</dt><dd>{{ side.entry.mode === 'unavailable' ? '—' : new Date(side.entry.modified).toLocaleString() }}</dd>
          </dl>
        </section>
      </div>
      <p v-if="input.category === 'files'" class="mt-3 text-sm text-zinc-400">Replace permanently replaces the destination file. Keep both creates a uniquely named copy.</p>
      <p v-if="input.category === 'folders'" class="mt-3 text-sm text-zinc-400">Merge combines folder contents and reviews file conflicts separately. It does not replace the whole folder.</p>
      <p v-if="input.category === 'type_mismatch'" class="mt-3 text-sm text-zinc-400">These items cannot be replaced or merged. Skip the item or keep both with a unique name.</p>
      <label class="mt-4 flex items-center gap-2 text-sm"><Checkbox v-model="applyRemaining" binary :disabled="busy" input-id="conflict-remaining" /> Apply to remaining conflicts of this type in this task</label>
      <p v-if="error" class="webfile-text-error mt-3" role="alert">{{ error }}</p>
      <p v-if="busy" class="mt-3 text-sm text-zinc-400" role="status"><i class="pi pi-spinner pi-spin mr-2" />Updating task…</p>
    </template>
    <template #footer>
      <div class="flex w-full flex-wrap justify-between gap-2">
        <Button data-dialog-cancel label="Cancel task" severity="secondary" :disabled="busy" @click="emit('cancel')" />
        <div class="flex flex-wrap gap-2"><Button v-for="choice in input?.choices ?? []" :key="choice" :label="labels[choice]" :severity="choice === 'replace' ? 'danger' : choice === 'skip' ? 'secondary' : undefined" :disabled="busy" @click="choose(choice)" /></div>
      </div>
    </template>
  </Dialog>
</template>
