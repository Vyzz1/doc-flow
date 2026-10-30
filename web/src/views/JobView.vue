<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { ArrowLeft, Copy, FileText, RefreshCw, CircleAlert, CheckCheck } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import StatusBadge from '@/components/StatusBadge.vue'
import StepTimeline from '@/components/StepTimeline.vue'
import JobResult from '@/components/JobResult.vue'
import { useRuntime } from '@/runtime'
import { useJob } from '@/composables/useJob'
import { useCopy } from '@/composables/useCopy'
import { errorMessage } from '@/lib/api'
import { validJobId } from '@/lib/files'

const { api, auth } = useRuntime()
const route = useRoute()
const id = computed(() => String(route.params.id ?? ''))
const subject = computed(() => auth.user.value?.id)
const { data: job, isPending, isFetching, error, refetch } = useJob(api, subject, id)
const { copy, feedback } = useCopy()
const labels = { invoice: 'Invoice', id_document: 'ID document', image: 'Image' }
function date(value: string) {
  return new Date(value).toLocaleString('en', { dateStyle: 'medium', timeStyle: 'short' })
}
</script>

<template>
  <RouterLink
    to="/"
    class="mb-7 inline-flex items-center gap-2 text-xs text-muted-foreground transition-colors hover:text-primary"
    ><ArrowLeft class="size-3.5" aria-hidden="true" /> Back to workspace</RouterLink
  >
  <section class="page-intro flex flex-wrap items-end justify-between gap-5">
    <div>
      <p class="eyebrow">DOCUMENT DETAILS</p>
      <h1 class="page-title">
        {{ job?.status === 'DONE' ? 'The details, uncovered.' : 'Every step, in view.' }}
      </h1>
      <p class="page-subtitle">
        {{
          job?.status === 'DONE'
            ? 'Your document is ready. Take a closer look.'
            : 'Follow your document from upload to insight.'
        }}
      </p>
    </div>
    <Button variant="outline" :disabled="isFetching || !validJobId(id)" @click="refetch()"
      ><RefreshCw
        class="size-3.5"
        :class="{ 'motion-safe:animate-spin': isFetching }"
        aria-hidden="true"
      />
      Refresh</Button
    >
  </section>
  <div v-if="!validJobId(id)" role="alert" class="error-notice">
    This job ID is invalid. Return to the workspace and enter a valid 26-character ID.
  </div>
  <div v-else-if="error" role="alert" class="error-notice mb-6 flex items-start gap-2">
    <CircleAlert class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
    <div>
      <p>{{ errorMessage(error) }}</p>
      <p v-if="job" class="mt-1 text-xs">
        Showing the last received status. Use Refresh to try again.
      </p>
    </div>
  </div>
  <div
    v-if="validJobId(id) && isPending && !error"
    class="space-y-5"
    role="status"
    aria-label="Loading job"
  >
    <Skeleton class="h-24 w-full rounded-xl" />
    <div class="grid gap-6 lg:grid-cols-[1fr_2fr]">
      <Skeleton class="h-80 rounded-xl" /><Skeleton class="h-96 rounded-xl" />
    </div>
    <span class="sr-only">Loading your document…</span>
  </div>
  <template v-if="job">
    <div
      class="mb-6 flex flex-wrap items-center justify-between gap-4 rounded-xl border border-border bg-white px-5 py-4"
    >
      <div class="flex min-w-0 items-center gap-3">
        <span class="grid size-10 shrink-0 place-items-center rounded-lg bg-primary/5"
          ><FileText class="size-5 text-primary" aria-hidden="true"
        /></span>
        <div class="min-w-0">
          <p class="text-sm font-semibold">{{ labels[job.docType] ?? 'Document' }}</p>
          <p class="mt-1 break-all font-mono text-[11px] text-muted-foreground">
            {{ job.jobId
            }}<Button
              variant="ghost"
              size="icon-xs"
              class="ml-1 align-middle"
              aria-label="Copy job ID"
              @click="copy(job.jobId)"
              ><Copy class="size-3"
            /></Button>
          </p>
        </div>
      </div>
      <div class="flex flex-wrap items-center gap-5">
        <span class="text-[11px] text-muted-foreground">Created {{ date(job.createdAt) }}</span
        ><StatusBadge :status="job.status" />
      </div>
    </div>
    <p role="status" class="mb-3 text-xs text-muted-foreground">{{ feedback }}</p>
    <div class="grid items-start gap-6 xl:grid-cols-[minmax(260px,0.85fr)_minmax(0,1.6fr)]">
      <Card class="main-card"
        ><CardHeader class="border-b border-border pb-5"
          ><CardTitle class="text-sm">Processing journey</CardTitle
          ><CardDescription class="text-xs">{{
            job.status === 'DONE'
              ? 'Your document has completed processing.'
              : job.status === 'FAILED'
                ? 'Processing ended with an error.'
                : 'Updates every few seconds while this tab is active.'
          }}</CardDescription></CardHeader
        ><CardContent class="pt-5"
          ><StepTimeline :steps="job.steps" :status="job.status" />
          <p class="mt-4 border-t pt-4 text-[10px] text-muted-foreground">
            Last update {{ date(job.updatedAt) }}
          </p></CardContent
        ></Card
      >
      <Card class="main-card"
        ><CardHeader class="border-b border-border pb-5"
          ><CardTitle class="flex items-center gap-2 text-sm"
            ><CheckCheck class="size-4 text-primary" aria-hidden="true" /> Document
            results</CardTitle
          ><CardDescription class="text-xs"
            >Recognized text and the information that matters.</CardDescription
          ></CardHeader
        ><CardContent class="pt-6"><JobResult :job="job" /></CardContent
      ></Card>
    </div>
  </template>
</template>
