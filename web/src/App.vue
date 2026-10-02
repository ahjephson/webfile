<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import ConfirmDialog from 'primevue/confirmdialog'
import ProgressSpinner from 'primevue/progressspinner'
import Toast from 'primevue/toast'
import FileExplorer from '@/components/FileExplorer.vue'
import LoginView from '@/components/LoginView.vue'
import ElevationDialog from '@/components/ElevationDialog.vue'
import { api, type SessionStatus } from '@/api/client'

const session = ref<SessionStatus | null>(null)
const checking = ref(true)
const loginBusy = ref(false)
const loginError = ref('')
const elevationVisible = ref(false)
const elevationBusy = ref(false)
const elevationError = ref('')
const explorerVersion = ref(0)
let elevationTimer: number | null = null

onMounted(refreshSession)
onBeforeUnmount(clearElevationTimer)

function clearElevationTimer() {
  if (elevationTimer !== null) window.clearTimeout(elevationTimer)
  elevationTimer = null
}

function scheduleElevationExpiry() {
  clearElevationTimer()
  if (!session.value?.elevated || !session.value.elevation_expires_at) return
  const delay = Math.max(0, Date.parse(session.value.elevation_expires_at) - Date.now())
  elevationTimer = window.setTimeout(async () => {
    await refreshSession()
    explorerVersion.value++
  }, delay + 250)
}

async function refreshSession() {
  checking.value = true
  loginError.value = ''
  try {
    session.value = await api.session()
    scheduleElevationExpiry()
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

async function elevate(password: string) {
  elevationBusy.value = true
  elevationError.value = ''
  try {
    session.value = await api.elevate(password)
    elevationVisible.value = false
    explorerVersion.value++
    scheduleElevationExpiry()
  } catch (err) {
    elevationError.value = err instanceof Error ? err.message : String(err)
  } finally {
    elevationBusy.value = false
  }
}

async function dropElevation() {
  await api.dropElevation()
  session.value = await api.session()
  explorerVersion.value++
  scheduleElevationExpiry()
}

function requestElevation() {
  elevationError.value = ''
  elevationVisible.value = true
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
      :key="explorerVersion"
      :username="session.user?.username ?? ''"
      :show-logout="session.mode === 'pam'"
      :elevation-available="session.elevation_available === true"
      :elevated="session.elevated === true"
      :elevation-expires-at="session.elevation_expires_at"
      @logout="logout"
      @request-elevation="requestElevation"
      @drop-elevation="dropElevation"
    />
    <ElevationDialog
      v-model:visible="elevationVisible"
      :username="session?.user?.username ?? ''"
      :busy="elevationBusy"
      :error="elevationError"
      @submit="elevate"
    />
  </main>
</template>
