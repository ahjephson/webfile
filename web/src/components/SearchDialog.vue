<script setup lang="ts">
import { ref, watch } from 'vue'
import Button from 'primevue/button'
import Dialog from '@/components/AppDialog.vue'
import InputText from 'primevue/inputtext'
import Select from 'primevue/select'
import Checkbox from 'primevue/checkbox'
import type { SearchRequest } from '@/types/files'

const props = defineProps<{ visible: boolean; path: string; showHidden: boolean; initial?: SearchRequest | null }>()
const emit = defineEmits<{ 'update:visible': [visible: boolean]; search: [request: SearchRequest] }>()
const query = ref(''), wildcard = ref(false), caseSensitive = ref(false), hidden = ref(false)
const kind = ref<SearchRequest['kind']>('all')
watch(() => props.visible, visible => {
  if (!visible) return
  hidden.value = props.initial?.show_hidden ?? props.showHidden
  if (props.initial) {
    query.value = props.initial.query
    wildcard.value = props.initial.wildcard
    caseSensitive.value = props.initial.case_sensitive
    kind.value = props.initial.kind
  }
})
function submit() {
  if (!query.value.trim()) return
  emit('search', { path: props.path, query: query.value, wildcard: wildcard.value, case_sensitive: caseSensitive.value, kind: kind.value, show_hidden: hidden.value })
}
</script>

<template>
  <Dialog :visible="visible" modal header="Search beneath this folder" :style="{ width: 'min(48rem, 96vw)' }" @update:visible="emit('update:visible', $event)">
    <p class="mb-3 break-all text-sm text-zinc-500">{{ path }} · Filename search; directory links are not followed.</p>
    <form class="space-y-3" @submit.prevent="submit">
      <div class="flex flex-wrap gap-2">
        <InputText v-model="query" fluid class="min-w-40 flex-1" aria-label="Search filenames" placeholder="Filename or wildcard pattern" />
        <Select v-model="kind" :options="[{ label: 'Files and folders', value: 'all' }, { label: 'Files only', value: 'files' }, { label: 'Folders only', value: 'folders' }]" option-label="label" option-value="value" aria-label="Search kind" />
      </div>
      <div class="flex flex-wrap gap-4 text-sm">
        <label class="flex items-center gap-2"><Checkbox v-model="wildcard" binary input-id="search-wildcard" /> Wildcards (* and ?)</label>
        <label class="flex items-center gap-2"><Checkbox v-model="caseSensitive" binary input-id="search-case" /> Match case</label>
        <label class="flex items-center gap-2"><Checkbox v-model="hidden" binary input-id="search-hidden" /> Include dotfiles</label>
      </div>
      <Button data-dialog-primary type="submit" label="Search" icon="pi pi-search" :disabled="!query.trim()" />
    </form>
    <template #footer><Button data-dialog-cancel label="Close" severity="secondary" @click="emit('update:visible', false)" /></template>
  </Dialog>
</template>
