<script setup lang="ts">
import { useRoute } from 'vue-router'
import { useRuntime } from './runtime'
import AppShell from './components/AppShell.vue'
import SetupNotice from './components/SetupNotice.vue'
const { config } = useRuntime()
const route = useRoute()
</script>

<template>
  <SetupNotice v-if="config.mode === 'setup'" />
  <RouterView v-else-if="route.path === '/sign-in'" />
  <AppShell v-else><RouterView :key="route.path" /></AppShell>
</template>
