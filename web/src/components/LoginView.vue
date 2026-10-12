<script setup lang="ts">
import { ref } from 'vue'
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import Password from 'primevue/password'
import Message from 'primevue/message'

const props = defineProps<{
  busy?: boolean
  error?: string
}>()

const emit = defineEmits<{
  login: [username: string, password: string]
}>()

const username = ref('')
const password = ref('')

function submit() {
  if (!username.value.trim() || !password.value) return
  emit('login', username.value.trim(), password.value)
}
</script>

<template>
  <section class="grid h-full place-items-center bg-zinc-950 p-5">
    <div class="w-full max-w-sm rounded-2xl border border-zinc-800 bg-zinc-900 p-6 shadow-2xl">
      <div class="mb-6">
        <div class="mb-2 flex h-11 w-11 items-center justify-center rounded-xl bg-zinc-800">
          <i class="webfile-accent-warning pi pi-folder-open text-xl" />
        </div>
        <h1 class="text-xl font-semibold text-zinc-100">WebFile</h1>
        <p class="mt-1 text-sm text-zinc-400">Sign in with your Linux account.</p>
      </div>

      <Message v-if="props.error" severity="error" class="mb-4">{{ props.error }}</Message>

      <form class="space-y-4" @submit.prevent="submit">
        <div class="space-y-2">
          <label for="username" class="block text-sm font-medium">Username</label>
          <InputText id="username" v-model="username" autofocus fluid autocomplete="username" />
        </div>
        <div class="space-y-2">
          <label for="password" class="block text-sm font-medium">Password</label>
          <Password id="password" v-model="password" fluid :feedback="false" toggle-mask autocomplete="current-password" />
        </div>
        <Button
          type="submit"
          label="Sign in"
          icon="pi pi-sign-in"
          fluid
          :loading="busy"
          :disabled="!username.trim() || !password"
        />
      </form>
    </div>
  </section>
</template>
