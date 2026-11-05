<script setup lang="ts">
import { ref, useAttrs } from 'vue'
import PrimeDialog from 'primevue/dialog'
import type { DialogProps } from 'primevue/dialog'

defineOptions({ inheritAttrs: false })
const props = defineProps<Pick<DialogProps, 'visible' | 'header' | 'modal'> & { busy?: boolean; confirmation?: boolean }>()
const emit = defineEmits<{ 'update:visible': [value: boolean] }>()
const attrs = useAttrs()
const dialog = ref<{ container?: HTMLElement }>()

function focusContent() {
  const root = dialog.value?.container
  if (!root) return
  const available = (selector: string) => Array.from(root.querySelectorAll<HTMLElement>(selector))
    .find(field => field.tabIndex >= 0 && field.getClientRects().length && !field.matches(':disabled, [readonly], [aria-disabled="true"]'))
  const cancel = available('[data-dialog-cancel]')
  const field = available('.p-dialog-content [aria-invalid="true"], .p-dialog-content :invalid') ??
    available('.p-dialog-content input:not([type="hidden"]):not([type="checkbox"]):not([type="radio"]), .p-dialog-content textarea, .p-dialog-content [role="combobox"], .p-dialog-content [contenteditable="true"]')
  const target = (props.confirmation ? cancel : field) ?? cancel ?? available('.p-dialog-footer button')
  target?.focus()
  if (target instanceof HTMLInputElement && target.dataset.selectOnOpen !== undefined) target.select()
}

function escape(event: KeyboardEvent) {
  const target = event.target
  if (!(target instanceof HTMLElement)) return
  if (event.key === 'Escape') {
    // Let Select consume the first Escape, even when its popup is teleported.
    if (target.closest('[role="combobox"][aria-expanded="true"]')) return
    event.preventDefault()
    event.stopPropagation()
    if (!props.busy && !event.repeat && !event.isComposing) emit('update:visible', false)
    return
  }
}

function keydown(event: KeyboardEvent) {
  const target = event.target
  if (!(target instanceof HTMLElement)) return
  if (event.key !== 'Enter') return
  // Suppress native implicit form submission too, not just our shortcut.
  if (event.repeat || event.isComposing || props.busy) {
    if (!target.closest('textarea, [contenteditable="true"]')) event.preventDefault()
    return
  }
  // Some browsers implicitly submit a form on checkbox/radio Enter.
  // These controls use Space; Enter must not activate the dialog's action.
  if (target instanceof HTMLInputElement && ['checkbox', 'radio'].includes(target.type)) {
    event.preventDefault()
    return
  }
  if (event.defaultPrevented || event.shiftKey || event.ctrlKey || event.altKey || event.metaKey ||
      !(target instanceof HTMLInputElement) || target.readOnly || target.closest('[role="combobox"], [role="listbox"], [role="checkbox"], [role="switch"]') ||
      ['checkbox', 'radio', 'button', 'submit', 'reset', 'file', 'range', 'color'].includes(target.type)) return
  event.preventDefault()
  const root = event.currentTarget as HTMLElement
  const primary = root.querySelector<HTMLButtonElement>('[data-dialog-primary]')
  if (!primary || primary.disabled || primary.getAttribute('aria-disabled') === 'true') return
  if (!target.reportValidity() || target.form && !target.form.reportValidity()) return
  primary.click()
}
</script>

<template>
  <PrimeDialog ref="dialog" v-bind="attrs" :visible="visible" :header="header" :modal="modal"
    :closable="!busy" :close-on-escape="false" :dismissable-mask="false" block-scroll
    :pt="{ transition: { onAfterEnter: focusContent }, root: { onKeydownCapture: escape, onKeydown: keydown, 'aria-busy': !!busy } }"
    @update:visible="!busy && emit('update:visible', $event)">
    <template v-for="(_, name) in $slots" #[name]="slotProps"><slot :name="name" v-bind="slotProps ?? {}" /></template>
  </PrimeDialog>
</template>
