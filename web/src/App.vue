<script setup lang="ts">
import { onMounted, ref } from 'vue'
import ConfirmDialog from 'primevue/confirmdialog'
import ProgressSpinner from 'primevue/progressspinner'
import Toast from 'primevue/toast'
import FileExplorer from '@/components/FileExplorer.vue'
import LoginView from '@/components/LoginView.vue'
import { api, type SessionStatus } from '@/api/client'

const session = ref<SessionStatus | null>(null)
const checking = ref(true)
const loginBusy = ref(false)
const loginError = ref('')

onMounted(refreshSession)

async function refreshSession() {
  checking.value = true
  loginError.value = ''
  try {
    session.value = await api.session()
  } catch (err) {
    session.value = null
    loginError.value = err instanceof Error ? err.message : String(err)
  } finally {
    checking.value = false
  }
}

async function login(username: string, password: string) {
  loginBusy.value = true
  loginError.value = ''
  try {
    session.value = await api.login(username, password)
  } catch (err) {
    loginError.value = err instanceof Error ? err.message : String(err)
  } finally {
    loginBusy.value = false
  }
}

async function logout() {
  await api.logout()
  session.value = await api.session()
}
</script>

<template>
  <main class="h-full bg-zinc-950 text-zinc-100">
    <Toast position="bottom-right" />
    <ConfirmDialog />

    <div v-if="checking" class="grid h-full place-items-center">
      <ProgressSpinner class="h-10 w-10" />
    </div>
    <LoginView
      v-else-if="!session?.authenticated"
      :busy="loginBusy"
      :error="loginError"
      @login="login"
    />
    <FileExplorer
      v-else
      :username="session.user?.username ?? ''"
      :show-logout="session.mode === 'pam'"
      @logout="logout"
    />
  </main>
</template>
