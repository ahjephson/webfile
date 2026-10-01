<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import * as monaco from 'monaco-editor'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import { api, ApiError } from '@/api/client'
import MarkdownPreview from '@/components/MarkdownPreview.vue'
import MonacoEditor from '@/components/MonacoEditor.vue'

const props = defineProps<{
  visible: boolean
  path: string
  isNew: boolean
  theme: 'light' | 'dark'
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  saved: [path: string]
}>()

const content = ref('')
const original = ref('')
const etag = ref<string | undefined>()
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const conflict = ref(false)
const saveAsPath = ref('')
const currentPath = ref('')
const newDocument = ref(false)
const wordWrap = ref(false)
const richToolbar = ref(false)
const minimap = ref(false)
const renderWhitespace = ref(false)
const lineNumbers = ref(true)
const fontSize = ref(14)
const markdownPreview = ref(false)
const language = ref('plaintext')
const languagePicker = ref(false)
const languageQuery = ref('')
const languageSearch = ref<HTMLInputElement | null>(null)
const languageList = ref<HTMLElement | null>(null)
const zenMode = ref(false)
const fullscreen = ref(false)
const editorRoot = ref<HTMLElement | null>(null)
const editorRef = ref<InstanceType<typeof MonacoEditor> | null>(null)

const dirty = computed(() => content.value !== original.value)
const canFormat = computed(() => ['javascript', 'typescript', 'json', 'html', 'css'].includes(language.value))
const canPreviewMarkdown = computed(() => language.value === 'markdown')
const currentDirectory = computed(() => currentPath.value.slice(0, currentPath.value.lastIndexOf('/')) || '/')
const languageOptions = monaco.languages.getLanguages()
  .map(definition => ({
    id: definition.id,
    label: definition.aliases?.[0] ?? definition.id,
    search: [definition.id, ...(definition.aliases ?? []), ...(definition.extensions ?? [])].join(' ').toLowerCase(),
  }))
  .sort((left, right) => left.label.localeCompare(right.label))
const filteredLanguages = computed(() => {
  const query = languageQuery.value.trim().toLowerCase()
  return query ? languageOptions.filter(option => option.search.includes(query)) : languageOptions
})
const languageLabel = computed(() => languageOptions.find(option => option.id === language.value)?.label ?? language.value)

watch(canPreviewMarkdown, enabled => {
  if (!enabled) markdownPreview.value = false
})

function syncFullscreenState() {
  fullscreen.value = document.fullscreenElement === editorRoot.value
}

function handleKeydown(event: KeyboardEvent) {
  if (event.key !== 'Escape') return
  if (languagePicker.value) {
    languagePicker.value = false
    return
  }
  if (zenMode.value) {
    zenMode.value = false
    return
  }
  if (editorRef.value?.ownsElement(event.target) && !editorRef.value.hasTextFocus()) return
  void close()
}

onMounted(() => {
  document.addEventListener('fullscreenchange', syncFullscreenState)
  window.addEventListener('keydown', handleKeydown, true)
})

onBeforeUnmount(() => {
  document.removeEventListener('fullscreenchange', syncFullscreenState)
  window.removeEventListener('keydown', handleKeydown, true)
})

watch(() => [props.visible, props.path, props.isNew] as const, async ([visible, path, isNew]) => {
  if (!visible) return
  currentPath.value = path
  language.value = languageForPath(path)
  languagePicker.value = false
  saveAsPath.value = path
  newDocument.value = isNew
  error.value = ''
  conflict.value = false
  etag.value = undefined
  if (isNew) {
    content.value = ''
    original.value = ''
    return
  }

  loading.value = true
  try {
    const file = await api.readFile(path)
    content.value = file.content
    original.value = file.content
    etag.value = file.etag
  } catch (err) {
    error.value = messageOf(err)
  } finally {
    loading.value = false
  }
}, { immediate: true })

async function save(options?: { force?: boolean; path?: string; createOnly?: boolean }) {
  saving.value = true
  error.value = ''
  try {
    const target = options?.path ?? currentPath.value
    const createOnly = options?.createOnly ?? newDocument.value
    let result
    try {
      result = await api.saveFile(target, content.value, {
        etag: etag.value,
        createOnly,
        force: options?.force,
      })
    } catch (err) {
      if (!(err instanceof ApiError && err.status === 404 && !createOnly)) throw err
      result = await api.saveFile(target, content.value, { createOnly: true })
    }
    currentPath.value = target
    saveAsPath.value = target
    newDocument.value = false
    etag.value = result.etag
    original.value = content.value
    conflict.value = false
    emit('saved', target)
  } catch (err) {
    if (err instanceof ApiError && err.status === 412) {
      conflict.value = true
    } else {
      error.value = messageOf(err)
    }
  } finally {
    saving.value = false
  }
}

async function overwrite() {
  await save({ force: true, createOnly: false })
}

async function saveAs() {
  const target = saveAsPath.value.trim()
  if (!target) return
  await save({ path: target, createOnly: true })
}

async function close() {
  if (dirty.value && !window.confirm('Discard unsaved changes?')) return
  await editorRef.value?.closeSearch()
  if (document.fullscreenElement === editorRoot.value) {
    await document.exitFullscreen()
  }
  zenMode.value = false
  emit('update:visible', false)
}

async function toggleFullscreen() {
  if (document.fullscreenElement === editorRoot.value) {
    await document.exitFullscreen()
    return
  }

  try {
    await editorRoot.value?.requestFullscreen()
  } catch (err) {
    error.value = messageOf(err)
  }
}

async function toggleLanguagePicker() {
  if (languagePicker.value) {
    languagePicker.value = false
    return
  }
  languageQuery.value = ''
  languagePicker.value = true
  await nextTick()
  languageSearch.value?.focus()
  languageList.value?.querySelector<HTMLElement>('[role="option"][aria-selected="true"]')?.scrollIntoView({ block: 'center' })
}

function selectLanguage(value: string) {
  language.value = value
  languagePicker.value = false
}

function languageForPath(path: string): string {
  const name = path.split('/').pop()?.toLowerCase() ?? ''
  const ext = name.includes('.') ? name.split('.').pop() ?? '' : ''
  const byExtension: Record<string, string> = {
    js: 'javascript', ts: 'typescript', json: 'json', html: 'html', css: 'css',
    md: 'markdown', yaml: 'yaml', yml: 'yaml', xml: 'xml', sh: 'shell',
    go: 'go', cs: 'csharp', py: 'python', sql: 'sql', toml: 'ini', ini: 'ini',
  }
  return byExtension[ext] ?? 'plaintext'
}

function messageOf(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}
</script>

<template>
  <section
    v-if="visible"
    ref="editorRoot"
    class="absolute inset-0 z-40 flex min-h-0 flex-col bg-zinc-950 text-zinc-100"
    aria-label="Text editor"
    :data-toolbar-mode="richToolbar ? 'rich' : 'compact'"
    :data-zen="zenMode"
    :data-fullscreen="fullscreen"
    :data-markdown-preview="markdownPreview"
    :data-language="language"
  >
    <header v-show="!zenMode" class="flex h-14 shrink-0 items-center gap-3 border-b border-zinc-800 bg-zinc-900 px-3">
      <Button v-tooltip.bottom="'Close editor'" icon="pi pi-arrow-left" severity="secondary" text rounded aria-label="Close editor" @click="close" />
      <div class="min-w-0">
        <div class="truncate font-semibold">{{ currentPath.split('/').pop() || 'Untitled' }}</div>
        <div class="truncate text-xs text-zinc-400">{{ currentPath }}<span v-if="dirty"> • unsaved</span></div>
      </div>
      <div class="ml-auto flex items-center gap-1">
        <Button v-tooltip.bottom="'Undo'" icon="pi pi-undo" severity="secondary" text aria-label="Undo" @click="editorRef?.undo()" />
        <Button v-tooltip.bottom="'Redo'" icon="pi pi-refresh" severity="secondary" text aria-label="Redo" @click="editorRef?.redo()" />
        <Button v-tooltip.bottom="'Find'" icon="pi pi-search" severity="secondary" text aria-label="Find" @click="editorRef?.find()" />
        <Button v-tooltip.bottom="'Replace'" icon="pi pi-pencil" severity="secondary" text aria-label="Replace" @click="editorRef?.replace()" />
        <Button v-tooltip.bottom="wordWrap ? 'Disable word wrap' : 'Enable word wrap'" icon="pi pi-align-left" severity="secondary" text class="webfile-toggle" :class="{ 'webfile-toggle-active': wordWrap }" :aria-label="wordWrap ? 'Disable word wrap' : 'Enable word wrap'" :aria-pressed="wordWrap" @click="wordWrap = !wordWrap" />
        <span class="mx-2 h-6 w-px bg-zinc-700" />
        <Button v-tooltip.bottom="richToolbar ? 'Hide rich toolbar' : 'Show rich toolbar'" icon="pi pi-sliders-h" severity="secondary" text class="webfile-toggle" :class="{ 'webfile-toggle-active': richToolbar }" :aria-label="richToolbar ? 'Hide rich toolbar' : 'Show rich toolbar'" :aria-pressed="richToolbar" @click="richToolbar = !richToolbar" />
        <Button v-tooltip.bottom="'Save'" icon="pi pi-save" :loading="saving" :disabled="loading || !dirty && !newDocument" aria-label="Save" @click="save()" />
      </div>
    </header>

    <nav v-if="richToolbar && !zenMode" aria-label="Rich editor toolbar" class="flex h-11 shrink-0 items-center gap-1 border-b border-zinc-800 bg-zinc-900 px-3">
      <Button v-tooltip.bottom="minimap ? 'Hide minimap' : 'Show minimap'" icon="pi pi-map" severity="secondary" text class="webfile-toggle" :class="{ 'webfile-toggle-active': minimap }" :aria-label="minimap ? 'Hide minimap' : 'Show minimap'" :aria-pressed="minimap" @click="minimap = !minimap" />
      <Button v-tooltip.bottom="renderWhitespace ? 'Hide whitespace' : 'Show whitespace'" icon="pi pi-eye" severity="secondary" text class="webfile-toggle" :class="{ 'webfile-toggle-active': renderWhitespace }" :aria-label="renderWhitespace ? 'Hide whitespace' : 'Show whitespace'" :aria-pressed="renderWhitespace" @click="renderWhitespace = !renderWhitespace" />
      <Button v-tooltip.bottom="lineNumbers ? 'Hide line numbers' : 'Show line numbers'" icon="pi pi-list" severity="secondary" text class="webfile-toggle" :class="{ 'webfile-toggle-active': lineNumbers }" :aria-label="lineNumbers ? 'Hide line numbers' : 'Show line numbers'" :aria-pressed="lineNumbers" @click="lineNumbers = !lineNumbers" />
      <Button v-if="canFormat" v-tooltip.bottom="'Format document'" icon="pi pi-code" severity="secondary" text aria-label="Format document" @click="editorRef?.formatDocument()" />
      <Button v-if="canPreviewMarkdown" v-tooltip.bottom="markdownPreview ? 'Hide Markdown preview' : 'Show Markdown preview'" icon="pi pi-book" severity="secondary" text class="webfile-toggle" :class="{ 'webfile-toggle-active': markdownPreview }" :aria-label="markdownPreview ? 'Hide Markdown preview' : 'Show Markdown preview'" :aria-pressed="markdownPreview" @click="markdownPreview = !markdownPreview" />
      <span class="mx-2 h-6 w-px bg-zinc-700" />
      <Button v-tooltip.bottom="'Decrease font size'" icon="pi pi-minus" severity="secondary" text aria-label="Decrease font size" :disabled="fontSize <= 10" @click="fontSize--" />
      <Button v-tooltip.bottom="'Reset font size'" icon="pi pi-refresh" severity="secondary" text aria-label="Reset font size" :disabled="fontSize === 14" @click="fontSize = 14" />
      <Button v-tooltip.bottom="'Increase font size'" icon="pi pi-plus" severity="secondary" text aria-label="Increase font size" :disabled="fontSize >= 28" @click="fontSize++" />
      <span class="mx-2 h-6 w-px bg-zinc-700" />
      <Button v-tooltip.bottom="'Enter Zen mode'" icon="pi pi-window-minimize" severity="secondary" text aria-label="Enter Zen mode" @click="zenMode = true" />
      <Button v-tooltip.bottom="fullscreen ? 'Exit fullscreen' : 'Enter fullscreen'" :icon="fullscreen ? 'pi pi-window-minimize' : 'pi pi-window-maximize'" severity="secondary" text class="webfile-toggle" :class="{ 'webfile-toggle-active': fullscreen }" :aria-label="fullscreen ? 'Exit fullscreen' : 'Enter fullscreen'" :aria-pressed="fullscreen" @click="toggleFullscreen" />
    </nav>

    <Button v-if="zenMode" v-tooltip.left="'Exit Zen mode'" icon="pi pi-window-minimize" severity="secondary" rounded class="z-10 opacity-70 hover:opacity-100" style="position: absolute; right: 0.75rem; top: 0.75rem" aria-label="Exit Zen mode" @click="zenMode = false" />

    <Message v-if="error" severity="error" class="m-3 shrink-0">{{ error }}</Message>
    <div v-if="loading" class="grid flex-1 place-items-center text-zinc-400"><i class="pi pi-spinner pi-spin mr-2" /> Loading</div>
    <div v-else class="flex min-h-0 flex-1">
      <MonacoEditor ref="editorRef" v-model="content" :language="language" :theme="theme" :word-wrap="wordWrap" :minimap="minimap" :render-whitespace="renderWhitespace" :line-numbers="lineNumbers" :font-size="fontSize" class="min-h-0 min-w-0 flex-1" :class="{ 'basis-1/2': markdownPreview }" />
      <MarkdownPreview v-if="markdownPreview" :content="content" :base-path="currentDirectory" />
    </div>

    <section v-if="languagePicker && !zenMode" class="absolute bottom-8 right-3 z-20 flex max-h-[28rem] w-[min(26rem,calc(100vw-1.5rem))] flex-col overflow-hidden rounded-md border border-zinc-700 bg-zinc-900 shadow-2xl" aria-label="Select language mode">
      <div class="border-b border-zinc-700 p-2">
        <input ref="languageSearch" v-model="languageQuery" class="h-9 w-full rounded border border-zinc-600 bg-zinc-950 px-3 text-sm text-zinc-100 outline-none focus:border-sky-500" aria-label="Search languages" placeholder="Search languages" @keydown.enter="filteredLanguages[0] && selectLanguage(filteredLanguages[0].id)" />
      </div>
      <div ref="languageList" class="min-h-0 overflow-auto py-1" role="listbox" aria-label="Language modes">
        <button v-for="option in filteredLanguages" :key="option.id" type="button" role="option" :aria-selected="option.id === language" class="flex w-full items-center justify-between px-3 py-1.5 text-left text-sm hover:bg-zinc-800 focus:bg-zinc-800 focus:outline-none" :class="{ 'bg-zinc-800 text-sky-300': option.id === language }" @click="selectLanguage(option.id)">
          <span>{{ option.label }}</span>
          <i v-if="option.id === language" class="pi pi-check text-xs" />
        </button>
        <div v-if="!filteredLanguages.length" class="px-3 py-6 text-center text-sm text-zinc-500">No matching languages</div>
      </div>
    </section>

    <footer v-show="!zenMode" class="flex h-8 shrink-0 items-center justify-between border-t border-zinc-800 bg-zinc-900 px-3 text-xs text-zinc-400">
      <span><template v-if="newDocument">New file — save to create</template><template v-else-if="dirty">Unsaved changes</template><template v-else>Saved</template></span>
      <span class="flex items-center">
        <button type="button" class="rounded px-1.5 py-0.5 hover:bg-zinc-800 hover:text-zinc-100 focus:outline-none focus:ring-1 focus:ring-sky-500" :class="{ 'bg-zinc-800 text-zinc-100': languagePicker }" :aria-label="`Select language mode, currently ${languageLabel}`" :aria-expanded="languagePicker" @click="toggleLanguagePicker">{{ languageLabel }}</button>
        <span>· {{ fontSize }}px · {{ wordWrap ? 'Wrap' : 'No wrap' }}<template v-if="minimap"> · Minimap</template><template v-if="renderWhitespace"> · Whitespace</template><template v-if="markdownPreview"> · Preview</template></span>
      </span>
    </footer>
  </section>

  <Dialog v-model:visible="conflict" modal header="The target changed" :style="{ width: 'min(34rem, 92vw)' }">
    <div class="space-y-4">
      <p class="text-sm text-zinc-300">
        The target now exists or changed since it was opened. Nothing has been overwritten.
      </p>
      <div class="space-y-2">
        <label class="block text-sm font-medium">Save as</label>
        <InputText v-model="saveAsPath" fluid autocomplete="off" />
      </div>
      <div class="flex flex-wrap justify-end gap-2">
        <Button label="Cancel" severity="secondary" text @click="conflict = false" />
        <Button label="Overwrite" severity="danger" outlined :loading="saving" @click="overwrite" />
        <Button label="Save as" :loading="saving" @click="saveAs" />
      </div>
    </div>
  </Dialog>
</template>
