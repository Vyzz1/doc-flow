<script setup lang="ts">
import { Badge } from '@/components/ui/badge'
import type { JobStatus } from '@/lib/types'
defineProps<{ status: JobStatus }>()
const labels: Record<JobStatus, string> = { PENDING: 'Pending', PROCESSING: 'Processing', DONE: 'Completed', FAILED: 'Failed' }
</script>
<template>
  <Badge variant="outline" :class="['status-badge', status.toLowerCase()]"><span class="size-1.5 rounded-full bg-current" :class="{ 'motion-safe:animate-pulse': status === 'PROCESSING' }" />{{ labels[status] ?? status }}</Badge>
</template>
