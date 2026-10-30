<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  ArrowUpRight,
  FilePlus2,
  FileStack,
  LogOut,
  ShieldCheck,
  ChevronRight,
  FlaskConical,
  PanelLeft,
} from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { useRuntime } from '@/runtime'
import { errorMessage } from '@/lib/api'

const { auth, config, queryClient } = useRuntime()
const route = useRoute()
const router = useRouter()
const user = auth.user
const mobileMenu = ref(false)
const signingOut = ref(false)
const signOutError = ref('')
const initials = computed(() => (user.value?.email ?? 'DF').slice(0, 2).toUpperCase())
async function logout() {
  signingOut.value = true
  queryClient.clear()
  try {
    await auth.signOut()
  } catch (error) {
    signOutError.value = errorMessage(error)
  } finally {
    signingOut.value = false
    await router.replace('/sign-in')
  }
}
</script>

<template>
  <div class="min-h-screen bg-background">
    <a href="#main-content" class="skip-link">Skip to content</a>
    <aside :class="['app-sidebar', { 'mobile-open': mobileMenu }]">
      <RouterLink to="/" class="brand" @click="mobileMenu = false">
        <span class="brand-symbol"><FileStack class="size-5" aria-hidden="true" /></span>
        <span>docflow<span class="text-primary">.</span></span>
      </RouterLink>
      <div class="mt-10 px-3 text-[10px] font-semibold tracking-[0.16em] text-muted-foreground">
        WORKSPACE
      </div>
      <nav aria-label="Main navigation" class="mt-3 space-y-1">
        <RouterLink
          to="/"
          :class="['nav-item', { active: route.path === '/' }]"
          @click="mobileMenu = false"
        >
          <FilePlus2 class="size-[18px]" aria-hidden="true" /> New document
          <span class="ml-auto rounded border border-primary/15 px-1.5 text-[10px] text-primary"
            >＋</span
          >
        </RouterLink>
        <div v-if="route.path.startsWith('/jobs/')" class="nav-item active">
          <FileStack class="size-[18px]" aria-hidden="true" /> Document details
        </div>
      </nav>
      <div class="mt-auto space-y-5 pt-12">
        <div class="rounded-xl border border-border bg-white p-4">
          <ShieldCheck class="mb-2 size-5 text-primary" aria-hidden="true" />
          <p class="text-xs font-semibold">Your documents, protected.</p>
          <p class="mt-1 text-xs leading-5 text-muted-foreground">
            Private uploads. Documents and results expire after 7 days.
          </p>
        </div>
        <div class="flex items-center justify-between px-2 text-[11px] text-muted-foreground">
          <span>DocFlow workspace</span><ArrowUpRight class="size-3.5" aria-hidden="true" />
        </div>
      </div>
    </aside>
    <div class="app-body">
      <header class="workspace-header">
        <div class="flex min-w-0 items-center gap-3">
          <Button
            variant="ghost"
            size="icon"
            class="md:hidden"
            aria-label="Toggle navigation"
            :aria-expanded="mobileMenu"
            @click="mobileMenu = !mobileMenu"
            ><PanelLeft class="size-5"
          /></Button>
          <span class="hidden text-sm text-muted-foreground sm:inline">Workspace</span>
          <ChevronRight
            class="hidden size-3.5 text-muted-foreground/60 sm:block"
            aria-hidden="true"
          />
          <span class="truncate text-sm font-medium">{{
            route.path === '/' ? 'New document' : 'Document details'
          }}</span>
        </div>
        <div class="flex items-center gap-3">
          <span v-if="config.mode === 'demo'" class="demo-pill"
            ><FlaskConical class="size-3.5" aria-hidden="true" /> Demo mode</span
          >
          <span class="hidden max-w-52 truncate text-xs text-muted-foreground lg:inline">{{
            user?.email
          }}</span>
          <span
            class="grid size-8 place-items-center rounded-full bg-slate-200/70 text-[10px] font-bold text-slate-600"
            aria-hidden="true"
            >{{ initials }}</span
          >
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="Sign out"
            :disabled="signingOut"
            @click="logout"
            ><LogOut class="size-4"
          /></Button>
        </div>
      </header>
      <main id="main-content" class="workspace-main" tabindex="-1">
        <div
          v-if="config.mode === 'demo'"
          class="mb-7 flex items-start gap-2 rounded-lg border border-amber-200 bg-amber-50/70 px-4 py-3 text-xs leading-5 text-amber-900"
        >
          <FlaskConical class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
          <p>
            This is a demo workspace. Results are sample data; your files stay in this browser and
            are not sent to AWS.
          </p>
        </div>
        <p v-if="signOutError" role="alert">{{ signOutError }}</p>
        <slot />
      </main>
      <footer class="workspace-footer">
        <span>Less paperwork. More possibility.</span
        ><span class="flex items-center gap-1.5"
          ><ShieldCheck class="size-3.5" aria-hidden="true" /> Private by design</span
        >
      </footer>
    </div>
  </div>
</template>
