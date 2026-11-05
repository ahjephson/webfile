import { shallowRef } from 'vue'

interface Confirmation {
  header: string
  message: string
  icon?: string
  rejectLabel: string
  acceptLabel: string
  acceptProps?: { severity?: 'danger' }
  accept: () => unknown | Promise<unknown>
}

export const confirmation = shallowRef<Confirmation | null>(null)
export function useAppConfirm() {
  return { require: (options: Confirmation) => { confirmation.value = options } }
}
