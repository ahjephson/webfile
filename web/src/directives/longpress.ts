import type { Directive, DirectiveBinding } from 'vue'

type LongPressHandler = (event: PointerEvent) => void

type LongPressState = {
  pointerId: number
  startX: number
  startY: number
  triggered: boolean
  timer: number | null
  openingEvent: PointerEvent
}

type LongPressBinding = {
  handler: LongPressHandler
  state: LongPressState | null
  suppressNativeUntil: number
  clickCleanupTimer: number | null
  suppressClick: ((event: MouseEvent) => void) | null
}

const bindings = new WeakMap<HTMLElement, LongPressBinding>()
const holdDelay = 500
const moveTolerance = 10
const compatibilityWindow = 800

function clearTimer(binding: LongPressBinding) {
  if (binding.state?.timer !== null && binding.state?.timer !== undefined) {
    window.clearTimeout(binding.state.timer)
    binding.state.timer = null
  }
}

function removeClickSuppression(binding: LongPressBinding) {
  if (binding.suppressClick) window.removeEventListener('click', binding.suppressClick, true)
  if (binding.clickCleanupTimer !== null) window.clearTimeout(binding.clickCleanupTimer)
  binding.suppressClick = null
  binding.clickCleanupTimer = null
}

function installClickSuppression(binding: LongPressBinding, startX: number, startY: number) {
  removeClickSuppression(binding)
  binding.suppressClick = event => {
    removeClickSuppression(binding)
    if (Math.abs(event.clientX - startX) > moveTolerance || Math.abs(event.clientY - startY) > moveTolerance) return
    event.preventDefault()
    event.stopImmediatePropagation()
  }
  window.addEventListener('click', binding.suppressClick, true)
}

function finishPointer(binding: LongPressBinding, event: PointerEvent) {
  const state = binding.state
  if (!state || state.pointerId !== event.pointerId) return
  clearTimer(binding)
  if (state.triggered) {
    binding.suppressNativeUntil = performance.now() + compatibilityWindow
    binding.clickCleanupTimer = window.setTimeout(() => removeClickSuppression(binding), compatibilityWindow)
  }
  binding.state = null
}

function mounted(element: HTMLElement, directive: DirectiveBinding<LongPressHandler>) {
  const binding: LongPressBinding = {
    handler: directive.value,
    state: null,
    suppressNativeUntil: 0,
    clickCleanupTimer: null,
    suppressClick: null,
  }
  bindings.set(element, binding)

  element.addEventListener('pointerdown', event => {
    if (!event.isPrimary || (event.pointerType !== 'touch' && event.pointerType !== 'pen')) return
    clearTimer(binding)
    const state: LongPressState = {
      pointerId: event.pointerId,
      startX: event.clientX,
      startY: event.clientY,
      triggered: false,
      timer: null,
      openingEvent: event,
    }
    binding.state = state
    state.timer = window.setTimeout(() => {
      if (binding.state !== state) return
      state.timer = null
      state.triggered = true
      binding.suppressNativeUntil = Number.POSITIVE_INFINITY
      installClickSuppression(binding, state.startX, state.startY)
      binding.handler(state.openingEvent)
    }, holdDelay)
  })

  element.addEventListener('pointermove', event => {
    const state = binding.state
    if (!state || state.pointerId !== event.pointerId || state.triggered) return
    if (Math.abs(event.clientX - state.startX) <= moveTolerance && Math.abs(event.clientY - state.startY) <= moveTolerance) return
    clearTimer(binding)
    binding.state = null
  })

  element.addEventListener('pointerup', event => finishPointer(binding, event))
  element.addEventListener('pointercancel', event => finishPointer(binding, event))
  element.addEventListener('contextmenu', event => {
    if (binding.suppressNativeUntil === Number.POSITIVE_INFINITY || performance.now() < binding.suppressNativeUntil) {
      event.preventDefault()
      event.stopImmediatePropagation()
      return
    }
    clearTimer(binding)
    binding.state = null
  }, true)
}

function updated(element: HTMLElement, directive: DirectiveBinding<LongPressHandler>) {
  const binding = bindings.get(element)
  if (binding) binding.handler = directive.value
}

function unmounted(element: HTMLElement) {
  const binding = bindings.get(element)
  if (!binding) return
  clearTimer(binding)
  removeClickSuppression(binding)
  bindings.delete(element)
}

export const longpress: Directive<HTMLElement, LongPressHandler> = { mounted, updated, unmounted }
