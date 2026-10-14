<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import Select from 'primevue/select'
import Checkbox from 'primevue/checkbox'
import { api } from '@/api/client'
import type { FileEntry, SearchRequest } from '@/types/files'

const props = defineProps<{ visible: boolean; path: string; showHidden: boolean }>()
const emit = defineEmits<{ 'update:visible': [visible: boolean]; open: [entry: FileEntry]; locate: [entry: FileEntry] }>()
const query = ref(''), wildcard = ref(false), caseSensitive = ref(false), hidden = ref(false)
const kind = ref<SearchRequest['kind']>('all')
const results = ref<FileEntry[]>([]), busy = ref(false), message = ref(''), error = ref('')
const scanned = ref(0), skipped = ref(0)
let controller: AbortController | null = null
watch(() => props.visible, visible => { if (visible) hidden.value = props.showHidden; else cancel() })
onBeforeUnmount(cancel)
function cancel() { controller?.abort() }
async function search() {
  if (busy.value) return
  const request = new AbortController(); controller = request
  results.value = []; error.value = ''; scanned.value = 0; skipped.value = 0
  busy.value = true; message.value = 'Searching…'
  try {
    await api.search({ path: props.path, query: query.value, wildcard: wildcard.value, case_sensitive: caseSensitive.value, kind: kind.value, show_hidden: hidden.value }, value => {
      if (value.entry) results.value.push(value.entry)
      scanned.value = value.scanned; skipped.value = value.skipped
      if (value.done) message.value = value.truncated ? 'Partial results: result, traversal or time limit reached. Search a smaller folder or refine the query.' : 'Search complete'
    }, request.signal)
  } catch (err) {
    if (request.signal.aborted) message.value = 'Search cancelled — results are partial'
    else { error.value = err instanceof Error ? err.message : String(err); message.value = 'Search stopped — results are partial' }
  } finally { busy.value = false; controller = null }
}
function folder(entry: FileEntry) { return entry.path.slice(0, entry.path.lastIndexOf('/')) || '/' }
</script>

<template>
  <Dialog :visible="visible" modal header="Search beneath this folder" :style="{ width: 'min(64rem, 96vw)' }" :content-style="{ maxHeight: '75vh', overflow: 'auto' }" @update:visible="emit('update:visible', $event)">
    <p class="mb-3 break-all text-sm text-zinc-500">{{ path }} · Filename search; directory links are not followed.</p>
    <form class="space-y-3" @submit.prevent="search">
      <div class="flex flex-wrap gap-2">
        <InputText v-model="query" fluid class="min-w-40 flex-1" aria-label="Search filenames" placeholder="Filename or wildcard pattern" :disabled="busy" />
        <Select v-model="kind" :options="[{ label: 'Files and folders', value: 'all' }, { label: 'Files only', value: 'files' }, { label: 'Folders only', value: 'folders' }]" option-label="label" option-value="value" aria-label="Search kind" :disabled="busy" />
      </div>
      <div class="flex flex-wrap gap-4 text-sm">
        <label class="flex items-center gap-2"><Checkbox v-model="wildcard" binary :disabled="busy" input-id="search-wildcard" /> Wildcards (* and ?)</label>
        <label class="flex items-center gap-2"><Checkbox v-model="caseSensitive" binary :disabled="busy" input-id="search-case" /> Match case</label>
        <label class="flex items-center gap-2"><Checkbox v-model="hidden" binary :disabled="busy" input-id="search-hidden" /> Include dotfiles</label>
      </div>
      <div class="flex gap-2"><Button type="submit" label="Search" icon="pi pi-search" :loading="busy" :disabled="busy || !query.trim()" /><Button v-if="busy" label="Cancel search" severity="secondary" @click="cancel" /></div>
    </form>
    <p v-if="message" class="my-3 text-sm text-zinc-400" role="status"><i v-if="busy" class="pi pi-spinner pi-spin mr-2" />{{ message }} · {{ results.length }} results · {{ scanned }} entries checked<span v-if="skipped"> · {{ skipped }} inaccessible or unavailable entries skipped</span></p>
    <p v-if="error" class="webfile-text-error my-2" role="alert">{{ error }}</p>
    <div v-if="results.length" class="mt-3 max-h-[45vh] overflow-auto rounded border border-zinc-800">
      <table class="w-full text-left text-sm"><thead class="sticky top-0 bg-zinc-900 text-zinc-400"><tr><th class="p-2">Name</th><th class="p-2">Location</th><th class="p-2">Size</th><th class="p-2">Modified</th><th class="p-2">Actions</th></tr></thead>
        <tbody><tr v-for="entry in results" :key="entry.path" class="border-t border-zinc-800" data-search-result>
          <td class="p-2 break-all"><i class="pi mr-2" :class="entry.is_dir ? 'pi-folder' : 'pi-file'" />{{ entry.name }}</td><td class="p-2 break-all text-zinc-500">{{ folder(entry) }}</td><td class="p-2 whitespace-nowrap">{{ entry.is_dir ? '—' : `${entry.size.toLocaleString()} B` }}</td><td class="p-2 whitespace-nowrap">{{ new Date(entry.modified).toLocaleString() }}</td>
          <td class="p-2"><div class="flex gap-1"><Button icon="pi pi-external-link" severity="secondary" text :aria-label="`Open ${entry.path}`" @click="emit('open', entry)" /><Button icon="pi pi-folder-open" severity="secondary" text :aria-label="`Show ${entry.path} in folder`" @click="emit('locate', entry)" /></div></td>
        </tr></tbody>
      </table>
    </div>
    <template #footer><Button label="Close" severity="secondary" @click="emit('update:visible', false)" /></template>
  </Dialog>
</template>
