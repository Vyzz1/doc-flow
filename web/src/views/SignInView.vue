<script setup lang="ts">
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  ArrowRight,
  FileStack,
  ShieldCheck,
  ScanText,
  Layers3,
  LoaderCircle,
  FlaskConical,
} from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useRuntime } from '@/runtime'
import { errorMessage } from '@/lib/api'

const { auth, config } = useRuntime()
const route = useRoute()
const router = useRouter()
const email = ref('')
const password = ref('')
const newPassword = ref('')
const challenge = ref(false)
const pending = ref(false)
const error = ref('')
async function submit() {
  if (pending.value) return
  pending.value = true
  error.value = ''
  try {
    const step = challenge.value
      ? await auth.confirmPassword(newPassword.value)
      : await auth.signIn(email.value, password.value)
    password.value = ''
    newPassword.value = ''
    if (step === 'NEW_PASSWORD') {
      challenge.value = true
      return
    }
    const redirect = route.query.redirect
    const safe =
      typeof redirect === 'string' &&
      (redirect === '/' || /^\/jobs\/[0-7][0-9A-HJKMNP-TV-Z]{25}$/.test(redirect))
        ? redirect
        : '/'
    await router.replace(safe)
  } catch (failure) {
    error.value = errorMessage(failure)
  } finally {
    pending.value = false
  }
}
function restart() {
  challenge.value = false
  password.value = ''
  newPassword.value = ''
  error.value = ''
}
</script>

<template>
  <main class="sign-in-layout">
    <section class="sign-in-story" aria-label="About DocFlow">
      <div class="brand text-white">
        <span class="brand-symbol bg-white/15"><FileStack class="size-5" aria-hidden="true" /></span
        ><span>docflow<span class="text-blue-300">.</span></span>
      </div>
      <div class="relative z-10 my-auto py-20">
        <span
          class="mb-7 inline-flex items-center gap-2 rounded-full border border-white/20 bg-white/5 px-3 py-1.5 text-xs text-blue-100"
          ><span class="size-1.5 rounded-full bg-blue-300" /> Your document workspace</span
        >
        <h1 class="max-w-lg text-4xl font-semibold leading-tight tracking-tight lg:text-5xl">
          Goodbye paperwork.<br /><span class="text-blue-300">Hello, possibility.</span>
        </h1>
        <p class="mt-6 max-w-sm text-sm leading-7 text-slate-300">
          Turn invoices, IDs and images into text and structured data. One upload. A clearer
          picture.
        </p>
        <div class="mt-10 flex flex-wrap gap-3">
          <span class="story-feature"
            ><ScanText class="size-4" aria-hidden="true" /> Text recognition</span
          >
          <span class="story-feature"
            ><Layers3 class="size-4" aria-hidden="true" /> Structured results</span
          >
        </div>
      </div>
      <div class="relative z-10 flex items-center gap-2 text-xs text-slate-400">
        <ShieldCheck class="size-4" aria-hidden="true" /> Private uploads. Automatic expiry after 7
        days.
      </div>
      <div class="story-orbit orbit-one" aria-hidden="true"></div>
      <div class="story-orbit orbit-two" aria-hidden="true"></div>
    </section>
    <section class="flex min-h-screen items-center justify-center px-6 py-12 sm:px-12">
      <div class="w-full max-w-sm">
        <div class="brand mb-14 lg:hidden">
          <span class="brand-symbol"><FileStack class="size-5" aria-hidden="true" /></span
          ><span>docflow.</span>
        </div>
        <p class="eyebrow">LET'S GET STARTED</p>
        <h2 class="mt-3 text-3xl font-semibold tracking-tight">
          {{ challenge ? 'Set your password' : 'Welcome back.' }}
        </h2>
        <p class="mt-3 text-sm leading-6 text-muted-foreground">
          {{
            challenge
              ? 'Choose a new password to finish setting up your account.'
              : 'Sign in to turn your next document into something useful.'
          }}
        </p>
        <form class="mt-9 space-y-5" @submit.prevent="submit">
          <template v-if="config.mode === 'demo'">
            <div
              class="rounded-xl border border-amber-200 bg-amber-50 p-4 text-sm leading-6 text-amber-900"
            >
              <FlaskConical class="mb-2 size-5" aria-hidden="true" /><strong
                >Explore the demo</strong
              >
              <p>No account needed. All results are sample data and files stay in your browser.</p>
            </div>
          </template>
          <template v-else-if="!challenge">
            <div class="space-y-2">
              <Label for="email">Email address</Label
              ><Input
                id="email"
                v-model="email"
                type="email"
                autocomplete="username"
                placeholder="you@company.com"
                required
                :disabled="pending"
                class="h-11"
              />
            </div>
            <div class="space-y-2">
              <Label for="password">Password</Label
              ><Input
                id="password"
                v-model="password"
                type="password"
                autocomplete="current-password"
                placeholder="Enter your password"
                required
                :disabled="pending"
                class="h-11"
              />
            </div>
          </template>
          <div v-else class="space-y-2">
            <Label for="new-password">New password</Label
            ><Input
              id="new-password"
              v-model="newPassword"
              type="password"
              autocomplete="new-password"
              required
              minlength="12"
              :disabled="pending"
              class="h-11"
              aria-describedby="password-help"
            />
            <p id="password-help" class="text-xs leading-5 text-muted-foreground">
              At least 12 characters with uppercase, lowercase, a number and a symbol.
            </p>
          </div>
          <p v-if="error" role="alert" class="error-notice">{{ error }}</p>
          <Button type="submit" class="h-11 w-full" :disabled="pending"
            ><LoaderCircle
              v-if="pending"
              class="size-4 motion-safe:animate-spin"
              aria-hidden="true" />{{
              pending
                ? 'Signing in…'
                : config.mode === 'demo'
                  ? 'Explore demo workspace'
                  : challenge
                    ? 'Set password & continue'
                    : 'Sign in to workspace'
            }}<ArrowRight v-if="!pending" class="ml-auto size-4" aria-hidden="true"
          /></Button>
          <Button
            v-if="challenge"
            type="button"
            variant="ghost"
            class="w-full"
            :disabled="pending"
            @click="restart"
            >Back to sign in</Button
          >
        </form>
        <p
          v-if="config.mode !== 'demo'"
          class="mt-7 text-center text-xs leading-6 text-muted-foreground"
        >
          Need an account or help signing in?<br />Contact your workspace administrator.
        </p>
        <p class="mt-12 border-t border-border pt-6 text-center text-[11px] text-muted-foreground">
          A little less manual. A lot more done.
        </p>
      </div>
    </section>
  </main>
</template>
