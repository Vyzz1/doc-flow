<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import {
  AlertDialogRoot,
  AlertDialogPortal,
  AlertDialogOverlay,
  AlertDialogContent,
  AlertDialogTitle,
  AlertDialogDescription,
  AlertDialogCancel,
} from 'reka-ui'
import { LoaderCircle, Trash2 } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { useRuntime } from '@/runtime'
import { useDeleteJob, type DeleteOutcome } from '@/composables/useDeleteJob'
import { ApiError, errorMessage } from '@/lib/api'
import type { JobSummary } from '@/lib/types'

const props = defineProps<{ job?: JobSummary }>()
const emit = defineEmits<{ close: []; deleted: [outcome: DeleteOutcome] }>()
const { api, auth } = useRuntime()
const subject = computed(() => auth.user.value?.id)
const { remove, isPending, reset } = useDeleteJob(api, subject)
const failure = ref<unknown>()
const blocked = computed(
  () =>
    props.job?.status === 'PROCESSING' ||
    (failure.value instanceof ApiError && failure.value.status === 409),
)
watch(
  () => props.job?.jobId,
  () => {
    failure.value = undefined
    reset()
  },
)
function setOpen(open: boolean) {
  if (!open && !isPending.value) emit('close')
}
async function confirm() {
  if (!props.job || isPending.value || blocked.value) return
  failure.value = undefined
  try {
    const result = await remove(props.job.jobId)
    if (subject.value === result.owner) emit('deleted', result)
  } catch (error) {
    failure.value = error
  }
}
</script>

<template>
  <AlertDialogRoot :open="Boolean(job)" @update:open="setOpen">
    <AlertDialogPortal>
      <AlertDialogOverlay class="fixed inset-0 z-40 bg-slate-950/40 backdrop-blur-[2px]" />
      <AlertDialogContent
        class="fixed left-1/2 top-1/2 z-50 w-[calc(100%-2rem)] max-w-md -translate-x-1/2 -translate-y-1/2 rounded-2xl border border-border bg-white p-6 shadow-xl"
        @escape-key-down="isPending && $event.preventDefault()"
      >
        <span
          class="mb-4 grid size-11 place-items-center rounded-xl border border-red-100 bg-red-50 text-destructive"
          ><Trash2 class="size-5" aria-hidden="true"
        /></span>
        <AlertDialogTitle class="text-xl font-semibold tracking-tight"
          >Delete this document?</AlertDialogTitle
        >
        <AlertDialogDescription class="mt-3 text-sm leading-6 text-muted-foreground"
          >This permanently deletes the job, extracted results, uploaded file and derived files.
          This cannot be undone.</AlertDialogDescription
        >
        <p
          class="mt-4 break-all rounded-lg bg-slate-50 px-3 py-2 font-mono text-[11px] text-muted-foreground"
        >
          {{ job?.jobId }}
        </p>
        <p v-if="job?.status === 'PENDING'" class="mt-3 text-xs leading-5 text-muted-foreground">
          If an upload is already in flight, its file may remain until automatic expiry.
        </p>
        <p v-if="failure" role="alert" class="error-notice mt-4">{{ errorMessage(failure) }}</p>
        <p v-if="blocked" class="mt-3 text-xs leading-5 text-muted-foreground">
          Wait until processing has finished, then refresh the document list before trying again.
        </p>
        <div class="mt-6 flex flex-wrap justify-end gap-3">
          <AlertDialogCancel as-child
            ><Button variant="outline" :disabled="isPending">Cancel</Button></AlertDialogCancel
          >
          <Button variant="destructive" :disabled="isPending || blocked" @click="confirm"
            ><LoaderCircle
              v-if="isPending"
              class="size-4 motion-safe:animate-spin"
              aria-hidden="true"
            />{{ isPending ? 'Deleting…' : 'Delete permanently' }}</Button
          >
        </div>
      </AlertDialogContent>
    </AlertDialogPortal>
  </AlertDialogRoot>
</template>
