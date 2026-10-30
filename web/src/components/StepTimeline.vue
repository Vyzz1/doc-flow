<script setup lang="ts">
import type { JobStatus, Step } from '@/lib/types'
import { computed } from 'vue'
import { Check, Minus, X } from '@lucide/vue'
const props = defineProps<{ steps: Step[] | null; status: JobStatus }>()
const names = [
  ['validate', 'Validate document'], ['preprocess', 'Prepare pages'],
  ['thumbnail', 'Create thumbnail'], ['ocr', 'Recognize text'],
  ['extract', 'Extract fields'], ['save', 'Save results'],
]
const rows = computed(() => names.map(([key, label]) => ({ key, label, result: props.steps?.find(step => step.step === key) })))
</script>
<template>
  <ol class="step-list" aria-label="Processing step outcomes">
    <li v-for="row in rows" :key="row.key" class="step-row">
      <span :class="['step-icon', row.result?.status === 'SUCCEEDED' ? 'complete' : row.result?.status === 'FAILED' ? 'failed' : '']">
        <Check v-if="row.result?.status === 'SUCCEEDED'" class="size-3.5" aria-hidden="true" />
        <X v-else-if="row.result?.status === 'FAILED'" class="size-3.5" aria-hidden="true" />
        <Minus v-else class="size-3" aria-hidden="true" />
      </span>
      <div class="min-w-0 flex-1">
        <p class="text-sm" :class="row.result ? 'font-medium text-foreground' : 'text-muted-foreground'">{{ row.label }}</p>
        <p v-if="row.result?.error" class="mt-1 text-xs leading-5 text-destructive">{{ row.result.error }}</p>
      </div>
      <span class="text-xs text-muted-foreground">{{ row.result ? row.result.status === 'SUCCEEDED' ? `${(row.result.durationMs / 1000).toFixed(2)}s` : 'Failed' : 'Not reported' }}</span>
    </li>
  </ol>
  <p class="mt-4 text-xs leading-5 text-muted-foreground">Steps appear as their outcomes are reported. Processing time varies by document.</p>
</template>
