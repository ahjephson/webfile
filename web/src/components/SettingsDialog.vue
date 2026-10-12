<script setup lang="ts">
import { ref, watch } from 'vue'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import InputNumber from 'primevue/inputnumber'
import Select from 'primevue/select'
import ToggleSwitch from 'primevue/toggleswitch'
import { api, type HealthStatus } from '@/api/client'
import type { UserPreferences } from '@/preferences'

const props = defineProps<{
  visible: boolean
  preferences: UserPreferences
  saving: boolean
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  save: [preferences: UserPreferences]
}>()

const draft = ref<UserPreferences>({ ...props.preferences })
const build = ref<HealthStatus | null>(null)
const themeOptions = [
  { label: 'System', value: 'system' },
  { label: 'Light', value: 'light' },
  { label: 'Dark', value: 'dark' },
]
const viewOptions = [
  { label: 'List', value: 'list' },
  { label: 'Grid', value: 'grid' },
]
const gridOptions = [
  { label: 'Small', value: 'small' },
  { label: 'Medium', value: 'medium' },
  { label: 'Large', value: 'large' },
]
const toolbarOptions = [
  { label: 'Compact', value: 'compact' },
  { label: 'Rich', value: 'rich' },
]
const chunkOptions = [
  { label: '1 MiB', value: 1 },
  { label: '4 MiB', value: 4 },
  { label: '16 MiB', value: 16 },
  { label: '64 MiB', value: 64 },
]
const extractExistingOptions = [
  { label: 'Skip existing files', value: 'skip' },
  { label: 'Overwrite existing files', value: 'overwrite' },
]

watch(() => props.visible, visible => {
  if (!visible) return
  draft.value = { ...props.preferences }
  void api.health().then(value => { build.value = value }).catch(() => { build.value = null })
})

function close() {
  emit('update:visible', false)
}

function save() {
  emit('save', { ...draft.value })
}
</script>

<template>
  <Dialog
    :visible="visible"
    modal
    header="Settings"
    :style="{ width: 'min(48rem, 96vw)' }"
    :content-style="{ maxHeight: 'min(72vh, 46rem)', overflow: 'auto' }"
    @update:visible="emit('update:visible', $event)"
  >
    <div class="space-y-6">
      <section aria-labelledby="appearance-settings-heading">
        <h2 id="appearance-settings-heading" class="mb-1 text-sm font-semibold text-zinc-100">Appearance</h2>
        <p class="mb-3 text-xs text-zinc-500">Defaults used when WebFile opens.</p>
        <div class="webfile-section-list rounded-lg border border-zinc-800">
          <label class="settings-row">
            <span><strong>Theme</strong><small>Follow the device or choose a fixed theme.</small></span>
            <Select v-model="draft.theme" :options="themeOptions" option-label="label" option-value="value" aria-label="Theme" />
          </label>
          <label class="settings-row">
            <span><strong>Default view</strong><small>Open folders as a list or grid.</small></span>
            <Select v-model="draft.view_mode" :options="viewOptions" option-label="label" option-value="value" aria-label="Default view" />
          </label>
          <label class="settings-row" :class="{ 'opacity-50': draft.view_mode !== 'grid' }">
            <span><strong>Grid tile size</strong><small>Initial tile size for grid view.</small></span>
            <Select v-model="draft.grid_size" :options="gridOptions" option-label="label" option-value="value" aria-label="Grid tile size" :disabled="draft.view_mode !== 'grid'" />
          </label>
          <label class="settings-row">
            <span><strong>Show dotfiles</strong><small>Include names beginning with a dot.</small></span>
            <ToggleSwitch v-model="draft.show_hidden" aria-label="Show dotfiles by default" />
          </label>
          <label class="settings-row">
            <span><strong>Hide inaccessible files and folders</strong><small>Exclude files you cannot read and folders you cannot open.</small></span>
            <ToggleSwitch v-model="draft.hide_inaccessible" aria-label="Hide inaccessible files and folders" />
          </label>
        </div>
      </section>

      <section aria-labelledby="editor-settings-heading">
        <h2 id="editor-settings-heading" class="mb-1 text-sm font-semibold text-zinc-100">Editor</h2>
        <p class="mb-3 text-xs text-zinc-500">Defaults applied each time an editor is opened.</p>
        <div class="webfile-section-list rounded-lg border border-zinc-800">
          <label class="settings-row">
            <span><strong>Word wrap</strong><small>Wrap long lines to the editor width.</small></span>
            <ToggleSwitch v-model="draft.editor_word_wrap" aria-label="Word wrap by default" />
          </label>
          <label class="settings-row">
            <span><strong>Font size</strong><small>Editor text size from 10 to 28 pixels.</small></span>
            <InputNumber v-model="draft.editor_font_size" :min="10" :max="28" :step="1" suffix=" px" show-buttons input-class="w-20" aria-label="Editor font size" />
          </label>
          <label class="settings-row">
            <span><strong>Line numbers</strong><small>Show line numbers in the gutter.</small></span>
            <ToggleSwitch v-model="draft.editor_line_numbers" aria-label="Show line numbers by default" />
          </label>
          <label class="settings-row">
            <span><strong>Whitespace</strong><small>Render spaces and tabs.</small></span>
            <ToggleSwitch v-model="draft.editor_show_whitespace" aria-label="Show whitespace by default" />
          </label>
          <label class="settings-row">
            <span><strong>Minimap</strong><small>Show the document overview.</small></span>
            <ToggleSwitch v-model="draft.editor_minimap" aria-label="Show minimap by default" />
          </label>
          <label class="settings-row">
            <span><strong>Toolbar</strong><small>Open with compact or extended editor controls.</small></span>
            <Select v-model="draft.editor_toolbar" :options="toolbarOptions" option-label="label" option-value="value" aria-label="Default editor toolbar" />
          </label>
          <label class="settings-row">
            <span><strong>Markdown preview</strong><small>Open Markdown files with the preview pane visible.</small></span>
            <ToggleSwitch v-model="draft.editor_markdown_preview" aria-label="Markdown preview by default" />
          </label>
        </div>
      </section>

      <section aria-labelledby="transfer-settings-heading">
        <h2 id="transfer-settings-heading" class="mb-1 text-sm font-semibold text-zinc-100">Transfers</h2>
        <p class="mb-3 text-xs text-zinc-500">Advanced copy behaviour for this user.</p>
        <div class="webfile-section-list rounded-lg border border-zinc-800">
          <label class="settings-row">
            <span><strong>Copy chunk size</strong><small>Smaller chunks update progress and cancellation more frequently.</small></span>
            <Select v-model="draft.copy_chunk_mib" :options="chunkOptions" option-label="label" option-value="value" aria-label="Copy chunk size" />
          </label>
        </div>
      </section>

      <section aria-labelledby="archive-settings-heading">
        <h2 id="archive-settings-heading" class="mb-1 text-sm font-semibold text-zinc-100">Archives</h2>
        <p class="mb-3 text-xs text-zinc-500">Defaults used when extracting archives.</p>
        <div class="webfile-section-list rounded-lg border border-zinc-800">
          <label class="settings-row">
            <span><strong>Existing files when extracting</strong><small>Skip existing files by default, or replace regular files with extracted versions.</small></span>
            <Select v-model="draft.extract_existing_files" :options="extractExistingOptions" option-label="label" option-value="value" aria-label="Existing files when extracting" />
          </label>
        </div>
      </section>

      <section v-if="build" aria-labelledby="about-heading">
        <h2 id="about-heading" class="mb-1 text-sm font-semibold text-zinc-100">About</h2>
        <p class="text-xs text-zinc-500">
          WebFile {{ build.version }} · commit {{ build.commit }} · built {{ build.build_date }}
        </p>
      </section>
    </div>

    <template #footer>
      <div class="flex justify-end gap-2">
        <Button label="Cancel" severity="secondary" text :disabled="saving" @click="close" />
        <Button label="Save settings" icon="pi pi-check" :loading="saving" @click="save" />
      </div>
    </template>
  </Dialog>
</template>

<style scoped>
.settings-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(8rem, 12rem);
  align-items: center;
  gap: 1rem;
  padding: 0.75rem;
}

.settings-row > span {
  min-width: 0;
}

.settings-row strong,
.settings-row small {
  display: block;
}

.settings-row strong {
  font-size: 0.8125rem;
  font-weight: 500;
}

.settings-row small {
  margin-top: 0.125rem;
  color: #71717a;
  font-size: 0.75rem;
  line-height: 1rem;
}

@media (max-width: 36rem) {
  .settings-row {
    grid-template-columns: minmax(0, 1fr);
    gap: 0.5rem;
  }
}
</style>
