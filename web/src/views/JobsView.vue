<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import {
  ArrowRight,
  FilePlus2,
  FileStack,
  FileText,
  LoaderCircle,
  RefreshCw,
  Trash2,
} from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import StatusBadge from '@/components/StatusBadge.vue'
import DeleteJobDialog from '@/components/DeleteJobDialog.vue'
import { useRuntime } from '@/runtime'
import { useJobs } from '@/composables/useJobs'
import { errorMessage } from '@/lib/api'
import type { JobSummary } from '@/lib/types'
import type { DeleteOutcome } from '@/composables/useDeleteJob'

const { api, auth } = useRuntime()
const route = useRoute()
const subject = computed(() => auth.user.value?.id)
const {
  jobs,
  error,
  isPending,
  isFetching,
  isFetchingNextPage,
  isFetchNextPageError,
  hasNextPage,
  fetchNextPage,
  refetch,
} = useJobs(api, subject)
const selected = ref<JobSummary>()
const notice = ref(
  route.query.deleted === '1'
    ? 'Document deleted.'
    : route.query.deleted === 'missing'
      ? 'This document was already deleted.'
      : '',
)
const labels = { invoice: 'Invoice', id_document: 'ID document', image: 'Image' }
function date(value: string) {
  return new Date(value).toLocaleString('en', { dateStyle: 'medium', timeStyle: 'short' })
}
function deleted(outcome: DeleteOutcome) {
  selected.value = undefined
  notice.value = outcome.alreadyMissing ? 'This document was already deleted.' : 'Document deleted.'
}
</script>

<template>
  <section class="page-intro flex flex-wrap items-end justify-between gap-5">
    <div>
      <p class="eyebrow">DOCUMENT LIBRARY</p>
      <h1 class="page-title">Your documents, in one place<span class="text-primary">.</span></h1>
      <p class="page-subtitle">Pick up a result, follow a job, or clear what you no longer need.</p>
    </div>
    <div class="flex gap-3">
      <Button variant="outline" :disabled="isFetching" @click="refetch()"
        ><RefreshCw
          class="size-3.5"
          :class="{ 'motion-safe:animate-spin': isFetching }"
          aria-hidden="true"
        />
        Refresh</Button
      ><Button as-child
        ><RouterLink to="/"
          ><FilePlus2 class="size-4" aria-hidden="true" /> New document</RouterLink
        ></Button
      >
    </div>
  </section>
  <p
    v-if="notice"
    role="status"
    class="mb-5 rounded-lg border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-800"
  >
    {{ notice }}
  </p>
  <div v-if="error" role="alert" class="error-notice mb-5">
    <p>{{ errorMessage(error) }}</p>
    <Button
      variant="outline"
      size="sm"
      class="mt-3"
      :disabled="isFetching"
      @click="isFetchNextPageError ? fetchNextPage() : refetch()"
      >{{ isFetchNextPageError ? 'Retry loading more' : 'Try again' }}</Button
    >
  </div>
  <div v-if="isPending && !error" role="status" aria-label="Loading documents" class="space-y-3">
    <Skeleton v-for="row in 4" :key="row" class="h-20 w-full rounded-xl" /><span class="sr-only"
      >Loading your documents…</span
    >
  </div>
  <Card v-else-if="!jobs.length && !error" class="main-card"
    ><CardContent class="flex min-h-80 flex-col items-center justify-center py-12 text-center"
      ><span
        class="mb-5 grid size-16 place-items-center rounded-2xl border border-primary/15 bg-primary/5"
        ><FileStack class="size-7 text-primary" aria-hidden="true"
      /></span>
      <h2 class="text-xl font-semibold">No documents yet</h2>
      <p class="mt-3 max-w-sm text-sm leading-6 text-muted-foreground">
        Upload an invoice, ID or image to start turning your documents into useful data.
      </p>
      <Button as-child class="mt-6"
        ><RouterLink to="/"
          >Upload your first document
          <ArrowRight class="size-4" aria-hidden="true" /></RouterLink></Button></CardContent
  ></Card>
  <Card v-if="jobs.length" class="main-card min-w-0">
    <div class="flex flex-wrap items-center justify-between gap-2 border-b border-border px-6 py-5">
      <h2 class="text-sm font-semibold">My documents</h2>
      <span class="text-xs text-muted-foreground">Newest first · {{ jobs.length }} loaded</span>
    </div>
    <div class="w-full overflow-x-auto">
      <table class="w-full min-w-[680px] text-left text-sm">
        <caption class="sr-only">
          Your documents, newest first
        </caption>
        <thead class="bg-slate-50 text-xs text-muted-foreground">
          <tr>
            <th scope="col" class="px-6 py-3 font-medium">Document</th>
            <th scope="col" class="px-4 py-3 font-medium">Status</th>
            <th scope="col" class="px-4 py-3 font-medium">Created</th>
            <th scope="col" class="px-4 py-3 font-medium">Updated</th>
            <th scope="col" class="px-6 py-3 text-right font-medium">Actions</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-border">
          <tr v-for="job in jobs" :key="job.jobId" class="transition-colors hover:bg-slate-50/50">
            <th scope="row" class="px-6 py-5 font-normal">
              <RouterLink
                :to="`/jobs/${job.jobId}`"
                :aria-label="`Open job ${job.jobId}`"
                class="group flex items-center gap-3"
                ><span
                  class="grid size-9 shrink-0 place-items-center rounded-lg border border-border bg-slate-50 text-primary"
                  ><FileText class="size-4" aria-hidden="true" /></span
                ><span
                  ><span class="block text-sm font-medium group-hover:text-primary">{{
                    labels[job.docType] ?? 'Document'
                  }}</span
                  ><span class="mt-1 block font-mono text-[10px] text-muted-foreground">{{
                    job.jobId
                  }}</span
                  ><span
                    v-if="job.failureReason"
                    class="mt-1 block max-w-64 text-xs leading-5 text-destructive"
                    >{{ job.failureReason }}</span
                  ></span
                ></RouterLink
              >
            </th>
            <td class="px-4 py-5"><StatusBadge :status="job.status" /></td>
            <td class="whitespace-nowrap px-4 py-5 text-xs text-muted-foreground">
              {{ date(job.createdAt) }}
            </td>
            <td class="whitespace-nowrap px-4 py-5 text-xs text-muted-foreground">
              {{ date(job.updatedAt) }}
            </td>
            <td class="px-6 py-5 text-right">
              <Button
                variant="ghost"
                size="icon-sm"
                class="text-muted-foreground hover:bg-red-50 hover:text-destructive"
                aria-label="Delete document"
                :title="
                  job.status === 'PROCESSING'
                    ? 'Processing jobs cannot be deleted. Refresh after processing finishes.'
                    : 'Delete document'
                "
                :disabled="job.status === 'PROCESSING'"
                @click="selected = job"
                ><Trash2 class="size-4"
              /></Button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div class="flex flex-wrap items-center justify-between gap-3 border-t border-border px-6 py-4">
      <p class="text-xs text-muted-foreground">
        Documents expire after 7 days. Refresh to update statuses.
      </p>
      <Button
        v-if="hasNextPage"
        variant="outline"
        size="sm"
        :disabled="isFetching"
        @click="fetchNextPage()"
        ><LoaderCircle
          v-if="isFetchingNextPage"
          class="size-3.5 motion-safe:animate-spin"
          aria-hidden="true"
        />{{ isFetchingNextPage ? 'Loading…' : 'Load more' }}</Button
      >
    </div>
  </Card>
  <DeleteJobDialog :job="selected" @close="selected = undefined" @deleted="deleted" />
</template>
