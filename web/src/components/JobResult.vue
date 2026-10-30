<script setup lang="ts">
import type { Job } from '@/lib/types'
import { Copy, FileSearch, CircleAlert, Clock3 } from '@lucide/vue'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Button } from '@/components/ui/button'
import { useCopy } from '@/composables/useCopy'
defineProps<{ job: Job }>()
const { copy, feedback } = useCopy()
function percent(value: number) {
  return `${Math.round(Math.min(1, Math.max(0, value)) * 100)}%`
}
</script>
<template>
  <div v-if="job.status === 'FAILED'" role="alert" class="result-empty border-red-200 bg-red-50/50">
    <CircleAlert class="mb-4 size-9 text-destructive" aria-hidden="true" />
    <h2 class="text-lg font-semibold">We couldn't process this document</h2>
    <p class="mt-2 max-w-md text-sm leading-6 text-muted-foreground">
      {{ job.failureReason || 'The document could not be processed.' }}
    </p>
    <p class="mt-4 text-xs text-muted-foreground">
      Check your document, then start a new upload to try again.
    </p>
  </div>
  <div v-else-if="job.status !== 'DONE'" class="result-empty">
    <span
      class="mb-5 grid size-16 place-items-center rounded-2xl border border-primary/15 bg-primary/5"
      ><FileSearch class="size-7 text-primary" aria-hidden="true"
    /></span>
    <h2 class="text-lg font-semibold">Your results are on the way</h2>
    <p class="mt-2 max-w-sm text-sm leading-6 text-muted-foreground">
      We're turning your document into readable text and structured fields. You can leave this page
      and return using your job ID.
    </p>
    <span class="mt-6 flex items-center gap-2 text-xs text-muted-foreground"
      ><Clock3 class="size-3.5" aria-hidden="true" /> Updates automatically while processing</span
    >
  </div>
  <div v-else-if="!job.result" class="result-empty">
    <CircleAlert class="mb-4 size-8 text-amber-600" aria-hidden="true" />
    <h2 class="text-lg font-semibold">The result is not available</h2>
    <p class="mt-2 text-sm text-muted-foreground">
      The job completed, but no result was returned. Try refreshing this job.
    </p>
  </div>
  <div v-else class="space-y-6">
    <div
      class="grid grid-cols-3 divide-x divide-border rounded-xl border border-border bg-slate-50/60 px-2 py-4"
    >
      <div class="px-4">
        <p class="metric-label">CONFIDENCE</p>
        <p class="mt-1 text-xl font-semibold text-emerald-700">
          {{ percent(job.result.confidence) }}
        </p>
      </div>
      <div class="px-4">
        <p class="metric-label">PAGES</p>
        <p class="mt-1 text-xl font-semibold">{{ job.result.pages }}</p>
      </div>
      <div class="px-4">
        <p class="metric-label">OCR ENGINE</p>
        <p class="mt-2 text-sm font-medium capitalize">{{ job.result.provider }}</p>
      </div>
    </div>
    <Tabs default-value="fields" class="w-full">
      <TabsList class="mb-5"
        ><TabsTrigger value="fields">Extracted fields</TabsTrigger
        ><TabsTrigger value="text">Recognized text</TabsTrigger></TabsList
      >
      <TabsContent value="fields">
        <div
          v-if="job.result.fields?.length"
          class="overflow-x-auto rounded-lg border border-border"
        >
          <table class="w-full text-left text-sm">
            <caption class="sr-only">
              Extracted document fields and confidence
            </caption>
            <thead class="bg-slate-50 text-xs text-muted-foreground">
              <tr>
                <th scope="col" class="px-4 py-3 font-medium">Field</th>
                <th scope="col" class="px-4 py-3 font-medium">Value</th>
                <th scope="col" class="px-4 py-3 text-right font-medium">Confidence</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-border">
              <tr v-for="field in job.result.fields" :key="field.name">
                <th scope="row" class="px-4 py-4 font-medium capitalize">
                  {{ field.name.replaceAll('_', ' ') }}
                </th>
                <td class="max-w-72 break-words px-4 py-4">{{ field.value }}</td>
                <td class="px-4 py-4 text-right font-mono text-xs text-muted-foreground">
                  {{ percent(field.confidence) }}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <p
          v-else
          class="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground"
        >
          No structured fields were found. You can still review the recognized text.
        </p>
      </TabsContent>
      <TabsContent value="text" class="space-y-3">
        <div class="flex items-center justify-between">
          <span class="text-xs text-muted-foreground">Original OCR output</span
          ><Button variant="outline" size="sm" @click="copy(job.result.text)"
            ><Copy class="size-3.5" aria-hidden="true" /> Copy text</Button
          >
        </div>
        <pre
          class="max-h-[32rem] overflow-auto whitespace-pre-wrap break-words rounded-lg border border-border bg-slate-50 p-5 font-mono text-xs leading-7"
          >{{ job.result.text || 'No text was recognized.' }}</pre>
      </TabsContent>
    </Tabs>
    <p role="status" class="text-xs text-muted-foreground">{{ feedback }}</p>
    <p class="text-xs leading-5 text-muted-foreground">
      Automatically extracted. Review the results against your original document before using them.
    </p>
  </div>
</template>
