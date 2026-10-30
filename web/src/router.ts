import { createRouter, createWebHashHistory } from 'vue-router'
import type { Runtime } from './runtime'

export function createAppRouter(runtime: Runtime) {
  const router = createRouter({
    history: createWebHashHistory(),
    routes: [
      { path: '/sign-in', component: () => import('./views/SignInView.vue') },
      { path: '/', component: () => import('./views/UploadView.vue') },
      { path: '/jobs/:id', component: () => import('./views/JobView.vue') },
      { path: '/:pathMatch(.*)*', redirect: '/' },
    ],
    scrollBehavior: () => ({ top: 0 }),
  })
  router.beforeEach((to) => {
    if (runtime.config.mode === 'setup') return true
    if (to.path !== '/sign-in' && !runtime.auth.user.value)
      return { path: '/sign-in', query: { redirect: to.fullPath } }
    if (to.path === '/sign-in' && runtime.auth.user.value) return '/'
    return true
  })
  return router
}
