<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import Button from 'primevue/button'
import ConfirmDialog from 'primevue/confirmdialog'
import ProgressSpinner from 'primevue/progressspinner'
import Toast from 'primevue/toast'
import { useToast } from 'primevue/usetoast'
import FileExplorer from '@/components/FileExplorer.vue'
import LoginView from '@/components/LoginView.vue'
import ElevationDialog from '@/components/ElevationDialog.vue'
import { api, ApiError, type SessionStatus } from '@/api/client'
import { appPath } from '@/base'

const session = ref<SessionStatus | null>(null)
const toast = useToast()
const checking = ref(true)
const loginBusy = ref(false)
const loginError = ref('')
const sessionError = ref('')
const elevationVisible = ref(false)
const elevationBusy = ref(false)
const elevationError = ref('')
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
  }, delay + 250)
}

async function refreshSession() {
  checking.value = session.value === null
  sessionError.value = ''
  loginError.value = ''
  try {
    session.value = await api.session()
    scheduleElevationExpiry()
  } catch (err) {
    sessionError.value = err instanceof ApiError
      ? `WebFile could not check your session: ${err.message}`
      : 'WebFile could not be reached. Check your connection and try again.'
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
  try {
    const logoutURL = session.value?.logout_url
    await api.logout()
    if (logoutURL) {
      await postExternalLogout(logoutURL)
      return
    }
    session.value = await api.session()
  } catch (err) {
    toast.add({ severity: 'error', summary: 'Could not sign out', detail: err instanceof Error ? err.message : String(err), life: 6000 })
  }
}

async function postExternalLogout(url: string) {
  const target = new URL(url, window.location.href)
  if (target.origin === window.location.origin) {
    const response = await fetch(target, {
      method: 'POST',
      credentials: 'include',
      redirect: 'follow',
    })
    if (!response.ok) throw new Error(`External logout failed with status ${response.status}`)
    window.location.assign(response.redirected ? response.url : appPath())
    return
  }

  const form = document.createElement('form')
  form.method = 'post'
  form.action = target.href
  form.hidden = true
  document.body.appendChild(form)
  form.submit()
}

function retryExternalAuthentication() { window.location.reload() }

async function elevate(password: string) {
  elevationBusy.value = true
  elevationError.value = ''
  try {
    session.value = await api.elevate(password)
    elevationVisible.value = false
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
    <section v-else-if="sessionError && !session" class="grid h-full place-items-center p-6 text-center">
      <div class="max-w-md rounded-xl border border-zinc-800 bg-zinc-900 p-8 shadow-2xl">
        <i class="webfile-accent-warning pi pi-wifi mb-4 text-4xl" />
        <h1 class="text-xl font-semibold">Unable to connect to WebFile</h1>
        <p class="mt-2 text-sm text-zinc-400">{{ sessionError }}</p>
        <Button class="mt-6" label="Try again" icon="pi pi-refresh" @click="refreshSession" />
      </div>
    </section>
    <section v-else-if="!session?.authenticated && session?.mode === 'proxy'" class="grid h-full place-items-center p-6 text-center">
      <div class="max-w-md rounded-xl border border-zinc-800 bg-zinc-900 p-8 shadow-2xl">
        <i class="webfile-accent-info pi pi-shield mb-4 text-4xl" />
        <h1 class="text-xl font-semibold">External authentication required</h1>
        <p class="mt-2 text-sm text-zinc-400">Sign in through the configured authentication proxy, then try again.</p>
        <Button class="mt-6" label="Try again" icon="pi pi-refresh" @click="retryExternalAuthentication" />
      </div>
    </section>
    <LoginView
      v-else-if="!session?.authenticated"
      :busy="loginBusy"
      :error="loginError"
      @login="login"
    />
    <FileExplorer
      v-else
      :key="`${session.mode}:${session.user?.uid}:${session.user?.username}`"
      :username="session.user?.username ?? ''"
      :show-logout="session.mode === 'pam' || Boolean(session.logout_url)"
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
