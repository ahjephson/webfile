<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import editorWorker from 'monaco-editor/esm/vs/editor/editor.worker?worker'
import jsonWorker from 'monaco-editor/esm/vs/language/json/json.worker?worker'
import cssWorker from 'monaco-editor/esm/vs/language/css/css.worker?worker'
import htmlWorker from 'monaco-editor/esm/vs/language/html/html.worker?worker'
import typescriptWorker from 'monaco-editor/esm/vs/language/typescript/ts.worker?worker'
import * as monaco from 'monaco-editor'

(self as any).MonacoEnvironment = {
  getWorker(_moduleId: string, label: string) {
    if (label === 'json') return new jsonWorker()
    if (['css', 'scss', 'less'].includes(label)) return new cssWorker()
    if (['html', 'handlebars', 'razor'].includes(label)) return new htmlWorker()
    if (label === 'typescript' || label === 'javascript') return new typescriptWorker()
    return new editorWorker()
  },
}

const props = defineProps<{
  modelValue: string
  language?: string
  theme?: 'light' | 'dark'
  wordWrap?: boolean
  minimap?: boolean
  renderWhitespace?: boolean
  lineNumbers?: boolean
  fontSize?: number
}>()

const emit = defineEmits<{
  'update:modelValue': [value: string]
}>()

const host = ref<HTMLDivElement | null>(null)
let editor: monaco.editor.IStandaloneCodeEditor | null = null
let suppress = false

onMounted(() => {
  if (!host.value) return
  editor = monaco.editor.create(host.value, {
    value: props.modelValue,
    language: props.language ?? 'plaintext',
    automaticLayout: true,
    minimap: { enabled: props.minimap ?? false },
    find: { addExtraSpaceOnTop: false },
    scrollBeyondLastLine: false,
    fontSize: props.fontSize ?? 14,
    lineNumbers: props.lineNumbers === false ? 'off' : 'on',
    wordWrap: 'off',
    renderWhitespace: props.renderWhitespace ? 'all' : 'none',
    theme: props.theme === 'light' ? 'vs' : 'vs-dark',
  })
  editor.onDidChangeModelContent(() => {
    if (!editor || suppress) return
    emit('update:modelValue', editor.getValue())
  })
})

watch(() => props.modelValue, value => {
  if (!editor || editor.getValue() === value) return
  suppress = true
  editor.setValue(value)
  suppress = false
})

watch(() => props.language, language => {
  const model = editor?.getModel()
  if (model) monaco.editor.setModelLanguage(model, language ?? 'plaintext')
})

watch(() => props.theme, theme => {
  monaco.editor.setTheme(theme === 'light' ? 'vs' : 'vs-dark')
})

watch(() => props.wordWrap, enabled => {
  editor?.updateOptions({ wordWrap: enabled ? 'on' : 'off' })
})

watch(() => props.minimap, enabled => {
  editor?.updateOptions({ minimap: { enabled: enabled ?? false } })
})

watch(() => props.renderWhitespace, enabled => {
  editor?.updateOptions({ renderWhitespace: enabled ? 'all' : 'none' })
})

watch(() => props.lineNumbers, enabled => {
  editor?.updateOptions({ lineNumbers: enabled === false ? 'off' : 'on' })
})

watch(() => props.fontSize, size => {
  editor?.updateOptions({ fontSize: size ?? 14 })
})

function undo() {
  editor?.trigger('webfile', 'undo', null)
  editor?.focus()
}

function redo() {
  editor?.trigger('webfile', 'redo', null)
  editor?.focus()
}

function find() {
  editor?.trigger('webfile', 'actions.find', null)
  editor?.focus()
}

function replace() {
  editor?.trigger('webfile', 'editor.action.startFindReplaceAction', null)
  editor?.focus()
}

async function formatDocument() {
  await editor?.getAction('editor.action.formatDocument')?.run()
  editor?.focus()
}

async function closeSearch() {
  await editor?.getAction('closeFindWidget')?.run()
}

function hasTextFocus() {
  return editor?.hasTextFocus() ?? false
}

function ownsElement(target: EventTarget | null) {
  return target instanceof Node && (editor?.getDomNode()?.contains(target) ?? false)
}

defineExpose({ undo, redo, find, replace, formatDocument, closeSearch, hasTextFocus, ownsElement })

onBeforeUnmount(() => editor?.dispose())
</script>

<template>
  <div ref="host" class="h-full w-full" />
</template>
