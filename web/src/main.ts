import { createApp, watch } from 'vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import App from './App.vue'
import { readConfig } from './lib/config'
import { createDemo } from './lib/demo'
import { createApi } from './lib/api'
import { createAppRouter } from './router'
import { runtimeKey, type Runtime } from './runtime'
import './style.css'

async function start() {
  const config = readConfig(import.meta.env)
  const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false } } })
  const local = createDemo()
  const auth =
    config.mode === 'real' ? (await import('./lib/auth')).createCognitoAuth(config) : local.auth
  await auth.restore()
  const api =
    config.mode === 'real'
      ? createApi(config.apiBaseUrl, auth.token, () => {
          auth.user.value = undefined
        })
      : local.api
  const runtime: Runtime = { config, auth, api, queryClient }
  const router = createAppRouter(runtime)
  watch(
    () => auth.user.value?.id,
    (id, previous) => {
      if (id !== previous) {
        queryClient.clear()
        if (previous && !id)
          void router.replace({
            path: '/sign-in',
            query: { redirect: router.currentRoute.value.fullPath },
          })
      }
    },
    { flush: 'sync' },
  )
  createApp(App)
    .provide(runtimeKey, runtime)
    .use(VueQueryPlugin, { queryClient })
    .use(router)
    .mount('#app')
}
void start().catch(() => {
  const root = document.getElementById('app')
  if (root)
    root.textContent =
      'DocFlow could not start. Check browser storage permissions and reload the page.'
})
