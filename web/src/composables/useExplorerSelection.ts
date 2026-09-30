import { ref } from 'vue'

export function useExplorerSelection(initialSelected: string[], initialFocused: string) {
  const selectedPaths = ref<Set<string>>(new Set(initialSelected))
  const focusedPath = ref(initialFocused)
  const anchorPath = ref('')

  function toggle(path: string) {
    const next = new Set(selectedPaths.value)
    if (next.has(path)) next.delete(path)
    else next.add(path)
    selectedPaths.value = next
  }

  function clear() {
    selectedPaths.value = new Set()
    anchorPath.value = ''
  }

  function replace(paths: Iterable<string>) {
    selectedPaths.value = new Set(paths)
  }

  return { selectedPaths, focusedPath, anchorPath, toggle, clear, replace }
}
