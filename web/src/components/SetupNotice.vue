<script setup lang="ts">
import { FileStack, Settings2 } from '@lucide/vue'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
</script>
<template>
  <main class="grid min-h-screen place-items-center bg-background px-6 py-16">
    <Card class="w-full max-w-xl border-border shadow-sm">
      <CardHeader class="gap-4">
        <FileStack class="size-9 text-primary" aria-hidden="true" />
        <CardTitle class="text-2xl">Welcome to DocFlow</CardTitle>
        <CardDescription>Connect your workspace to get started.</CardDescription>
      </CardHeader>
      <CardContent class="space-y-5 text-sm leading-6 text-muted-foreground">
        <p class="flex items-center gap-2 font-medium text-foreground"><Settings2 class="size-4" aria-hidden="true" /> Frontend configuration is incomplete</p>
        <p>For a local preview without AWS, run <code>npm run dev:demo</code> in <code>web/</code>.</p>
        <p>To connect your existing backend, copy <code>.env.example</code> to <code>.env.local</code>, set the public API URL and Cognito identifiers, then restart Vite.</p>
        <p>See <code>web/README.md</code> for setup instructions. No AWS access keys belong in the frontend.</p>
      </CardContent>
    </Card>
  </main>
</template>
