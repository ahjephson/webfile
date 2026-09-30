import { computed, ref } from 'vue'

export type ThemeMode = 'system' | 'dark' | 'light'

const stored = window.localStorage.getItem('webfile-theme')
export const themeMode = ref<ThemeMode>(stored === 'dark' || stored === 'light' ? stored : 'system')
const systemDark = ref(window.matchMedia('(prefers-color-scheme: dark)').matches)
export const resolvedTheme = computed<'dark' | 'light'>(() => themeMode.value === 'system' ? (systemDark.value ? 'dark' : 'light') : themeMode.value)

const media = window.matchMedia('(prefers-color-scheme: dark)')
media.addEventListener('change', event => {
  systemDark.value = event.matches
  applyTheme()
})

export function setTheme(mode: ThemeMode) {
  themeMode.value = mode
  window.localStorage.setItem('webfile-theme', mode)
  applyTheme()
}

export function applyTheme() {
  const dark = resolvedTheme.value === 'dark'
  document.documentElement.classList.toggle('webfile-dark', dark)
  document.documentElement.classList.toggle('webfile-light', !dark)
}

applyTheme()
