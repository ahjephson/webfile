<script setup lang="ts">
import { computed } from 'vue'
import DOMPurify from 'dompurify'
import MarkdownIt from 'markdown-it'
import { api } from '@/api/client'

const props = defineProps<{
  content: string
  basePath: string
}>()

const markdown = new MarkdownIt({
  html: true,
  linkify: true,
  typographer: true,
})

const rendered = computed(() => {
  const container = document.createElement('div')
  container.innerHTML = DOMPurify.sanitize(markdown.render(props.content))
  container.querySelectorAll('img').forEach(image => {
    const source = image.getAttribute('src')
    if (source && isRelativeUrl(source)) {
      image.src = api.previewUrl(resolvePath(props.basePath, source))
    }
  })
  return container.innerHTML
})

function isRelativeUrl(value: string): boolean {
  return !value.startsWith('/') && !value.startsWith('#') && !/^[a-z][a-z\d+.-]*:/i.test(value)
}

function resolvePath(basePath: string, relativePath: string): string {
  const parts = `${basePath}/${relativePath}`.split('/')
  const resolved: string[] = []
  for (const part of parts) {
    if (!part || part === '.') continue
    if (part === '..') resolved.pop()
    else resolved.push(part)
  }
  return `/${resolved.join('/')}`
}
</script>

<template>
  <aside class="min-h-0 min-w-0 basis-1/2 overflow-auto border-l border-zinc-700 bg-zinc-950" aria-label="Markdown preview">
    <article v-if="content.trim()" class="markdown-preview mx-auto max-w-4xl px-8 py-6" v-html="rendered" />
    <div v-else class="grid h-full place-items-center text-sm text-zinc-500">Nothing to preview</div>
  </aside>
</template>

<style scoped>
.markdown-preview {
  color: #e4e4e7;
  font-size: 0.9375rem;
  line-height: 1.7;
}

.markdown-preview :deep(h1),
.markdown-preview :deep(h2),
.markdown-preview :deep(h3),
.markdown-preview :deep(h4) {
  color: #fafafa;
  font-weight: 650;
  line-height: 1.25;
  margin: 1.5em 0 0.6em;
}

.markdown-preview :deep(h1) {
  border-bottom: 1px solid #3f3f46;
  font-size: 2rem;
  padding-bottom: 0.35em;
}

.markdown-preview :deep(h2) {
  border-bottom: 1px solid #27272a;
  font-size: 1.5rem;
  padding-bottom: 0.3em;
}

.markdown-preview :deep(h3) {
  font-size: 1.2rem;
}

.markdown-preview :deep(p),
.markdown-preview :deep(ul),
.markdown-preview :deep(ol),
.markdown-preview :deep(blockquote),
.markdown-preview :deep(pre),
.markdown-preview :deep(table) {
  margin: 0 0 1em;
}

.markdown-preview :deep(ul),
.markdown-preview :deep(ol) {
  padding-left: 1.5rem;
}

.markdown-preview :deep(ul) {
  list-style: disc;
}

.markdown-preview :deep(ol) {
  list-style: decimal;
}

.markdown-preview :deep(a) {
  color: #38bdf8;
  text-decoration: underline;
  text-underline-offset: 2px;
}

.markdown-preview :deep(blockquote) {
  border-left: 3px solid #52525b;
  color: #a1a1aa;
  padding-left: 1rem;
}

.markdown-preview :deep(code) {
  background: #27272a;
  border-radius: 0.25rem;
  color: #f4f4f5;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 0.875em;
  padding: 0.15em 0.35em;
}

.markdown-preview :deep(pre) {
  background: #18181b;
  border: 1px solid #3f3f46;
  border-radius: 0.5rem;
  overflow: auto;
  padding: 1rem;
}

.markdown-preview :deep(pre code) {
  background: transparent;
  padding: 0;
}

.markdown-preview :deep(hr) {
  border: 0;
  border-top: 1px solid #3f3f46;
  margin: 1.5rem 0;
}

.markdown-preview :deep(img) {
  border-radius: 0.5rem;
  display: block;
  height: auto;
  max-width: 100%;
}

.markdown-preview :deep(table) {
  border-collapse: collapse;
  width: 100%;
}

.markdown-preview :deep(th),
.markdown-preview :deep(td) {
  border: 1px solid #3f3f46;
  padding: 0.45rem 0.65rem;
  text-align: left;
}

.markdown-preview :deep(th) {
  background: #27272a;
}
</style>
