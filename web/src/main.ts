import { createApp } from 'vue'
import PrimeVue from 'primevue/config'
import ConfirmationService from 'primevue/confirmationservice'
import ToastService from 'primevue/toastservice'
import Tooltip from 'primevue/tooltip'
import Aura from '@primeuix/themes/aura'
import { longpress } from './directives/longpress'
import App from './App.vue'
import './style.css'
import './theme'

createApp(App)
  .use(PrimeVue, {
    theme: {
      preset: Aura,
      options: {
        darkModeSelector: '.webfile-dark',
      },
    },
  })
  .use(ConfirmationService)
  .use(ToastService)
  .directive('tooltip', Tooltip)
  .directive('longpress', longpress)
  .mount('#app')
