<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useMutation } from '@tanstack/vue-query'
import {
  ArrowRight,
  ArrowUpFromLine,
  Check,
  FileImage,
  FileText,
  Fingerprint,
  FolderUp,
  Info,
  LoaderCircle,
  Search,
  ShieldCheck,
  X,
} from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { useRuntime } from '@/runtime'
import { useUpload } from '@/composables/useUpload'
import { allowedTypes, formatBytes, inspectFile, validJobId } from '@/lib/files'
import type { DocumentType } from '@/lib/types'
import { sampleJobId } from '@/lib/demo'

const { api, config } = useRuntime()
const router = useRouter()
const { file, docType, ticket, error, pending, phase, submit, reset } = useUpload(api)
const mutation = useMutation({
  mutationFn: submit,
  retry: false,
  onSuccess: (id) => {
    if (id) void router.push(`/jobs/${id}`)
  },
})
const picker = ref<HTMLInputElement>()
const dragging = ref(false)
const lookupId = ref('')
const lookupError = ref('')
const choices = [
  { id: 'invoice' as DocumentType, label: 'Invoice', detail: 'Bills & receipts', icon: FileText },
  {
    id: 'id_document' as DocumentType,
    label: 'ID document',
    detail: 'Identity cards',
    icon: Fingerprint,
  },
  { id: 'image' as DocumentType, label: 'Image', detail: 'Scans & photos', icon: FileImage },
]
const accepted = computed(() => allowedTypes[docType.value].join(','))
const formats = computed(() =>
  docType.value === 'invoice'
    ? 'PDF, PNG, JPG or TIFF'
    : docType.value === 'id_document'
      ? 'PNG or JPG'
      : 'PNG, JPG or TIFF',
)
const validation = computed(() => (file.value ? inspectFile(file.value, docType.value).error : ''))
function chooseFile(selected?: File) {
  if (!pending.value && !ticket.value && selected) {
    file.value = selected
    error.value = ''
  }
}
function drop(event: DragEvent) {
  dragging.value = false
  chooseFile(event.dataTransfer?.files[0])
}
function openJob() {
  const id = lookupId.value.trim()
  if (!validJobId(id)) {
    lookupError.value = 'Enter a valid 26-character job ID.'
    return
  }
  lookupError.value = ''
  void router.push(`/jobs/${id}`)
}
function clearFile() {
  reset()
  if (picker.value) picker.value.value = ''
}
</script>

<template>
  <section class="page-intro">
    <p class="eyebrow">DOCUMENT WORKSPACE</p>
    <h1 class="page-title">From document to data<span class="text-primary">.</span></h1>
    <p class="page-subtitle">Upload a document. We'll take care of the reading.</p>
  </section>
  <div class="upload-grid">
    <Card class="main-card">
      <CardHeader class="gap-2 border-b border-border px-6 py-5 sm:px-7"
        ><CardTitle class="flex items-center gap-2.5 text-base"
          ><FolderUp class="size-4.5 text-primary" aria-hidden="true" /> New document</CardTitle
        ><CardDescription>Start with a file and a document type.</CardDescription></CardHeader
      >
      <CardContent class="space-y-7 px-6 py-7 sm:px-7">
        <form class="space-y-7" @submit.prevent="mutation.mutate()">
          <fieldset :disabled="pending || Boolean(ticket)">
            <legend class="mb-3 text-sm font-medium">What are you uploading?</legend>
            <div class="grid grid-cols-3 gap-2.5">
              <label
                v-for="choice in choices"
                :key="choice.id"
                :class="[
                  'document-choice',
                  { selected: docType === choice.id, locked: pending || ticket },
                ]"
              >
                <input
                  v-model="docType"
                  type="radio"
                  name="document-type"
                  :value="choice.id"
                  class="sr-only"
                />
                <component :is="choice.icon" class="mb-3 size-5" aria-hidden="true" />
                <span class="text-xs font-semibold sm:text-sm">{{ choice.label }}</span>
                <span class="mt-1 hidden text-[10px] text-muted-foreground sm:block">{{
                  choice.detail
                }}</span>
                <Check
                  v-if="docType === choice.id"
                  class="absolute right-2 top-2 size-3 text-primary"
                  aria-hidden="true"
                />
              </label>
            </div>
          </fieldset>
          <div>
            <Label for="document-file" class="mb-3 inline-block">Choose your file</Label>
            <input
              id="document-file"
              ref="picker"
              type="file"
              :accept="accepted"
              :disabled="pending || Boolean(ticket)"
              class="sr-only"
              :aria-describedby="validation ? 'file-error' : 'file-help'"
              @change="chooseFile(($event.target as HTMLInputElement).files?.[0])"
            />
            <button
              v-if="!file"
              type="button"
              :class="['drop-zone', { dragging }]"
              :disabled="pending"
              @click="picker?.click()"
              @dragover.prevent="dragging = true"
              @dragleave.prevent="dragging = false"
              @drop.prevent="drop"
            >
              <span
                class="mb-5 grid size-14 place-items-center rounded-2xl border border-slate-200 bg-white shadow-xs"
                ><ArrowUpFromLine class="size-6 text-primary" aria-hidden="true"
              /></span>
              <span class="text-sm font-medium">Drop your document here</span>
              <span class="mt-2 text-xs text-muted-foreground"
                >or
                <span class="font-medium text-primary underline underline-offset-4"
                  >browse files</span
                ></span
              >
              <span id="file-help" class="mt-6 text-[11px] text-muted-foreground"
                >{{ formats }} · Up to 10 MiB</span
              >
            </button>
            <div
              v-else
              class="flex items-center gap-4 rounded-xl border border-primary/20 bg-primary/[0.025] p-5"
            >
              <span
                class="grid size-11 shrink-0 place-items-center rounded-lg border border-border bg-white"
                ><FileText class="size-5 text-primary" aria-hidden="true"
              /></span>
              <div class="min-w-0 flex-1">
                <p class="truncate text-sm font-medium">{{ file.name }}</p>
                <p class="mt-1 text-xs text-muted-foreground">
                  {{ formatBytes(file.size) }} · {{ formats }}
                </p>
              </div>
              <Button
                v-if="!ticket"
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label="Remove selected file"
                :disabled="pending"
                @click="clearFile"
                ><X class="size-4"
              /></Button>
              <Check v-else class="size-4 text-primary" aria-hidden="true" />
            </div>
            <p v-if="validation" id="file-error" role="alert" class="mt-3 text-xs text-destructive">
              {{ validation }}
            </p>
          </div>
          <p v-if="error" role="alert" class="error-notice">{{ error }}</p>
          <div
            v-if="ticket && error"
            class="space-y-3 rounded-lg border border-border p-4 text-xs leading-5 text-muted-foreground"
          >
            <p>
              Your job ID:
              <span class="break-all font-mono text-foreground">{{ ticket.jobId }}</span>
            </p>
            <p>
              Retry uses the same job and selected file. If the upload has expired, start a new
              submission.
            </p>
            <Button type="button" variant="outline" size="sm" :disabled="pending" @click="clearFile"
              >Start new submission</Button
            >
          </div>
          <div
            class="flex flex-col gap-4 border-t border-border pt-5 sm:flex-row sm:items-center sm:justify-between"
          >
            <span class="flex items-center gap-1.5 text-[11px] text-muted-foreground"
              ><ShieldCheck class="size-3.5" aria-hidden="true" /> Private, encrypted uploads</span
            >
            <Button
              type="submit"
              :disabled="pending || !file || Boolean(validation)"
              class="h-10 px-5"
              ><LoaderCircle
                v-if="pending"
                class="size-4 motion-safe:animate-spin"
                aria-hidden="true" />{{
                pending
                  ? phase === 'creating'
                    ? 'Creating job…'
                    : 'Uploading…'
                  : ticket
                    ? 'Retry upload'
                    : 'Upload & process'
              }}<ArrowRight v-if="!pending" class="ml-3 size-4" aria-hidden="true"
            /></Button>
          </div>
          <p role="status" class="sr-only">
            {{ pending ? 'Uploading document. Please wait.' : '' }}
          </p>
        </form>
      </CardContent>
    </Card>
    <aside class="space-y-5" aria-label="Upload help and job lookup">
      <Card class="main-card">
        <CardHeader class="pb-2"
          ><CardTitle class="text-sm">A little upload. A lot less work.</CardTitle></CardHeader
        ><CardContent>
          <ol class="how-it-works">
            <li>
              <span>01</span>
              <div>
                <h3>Upload your document</h3>
                <p>Choose a file and tell us what it is.</p>
              </div>
            </li>
            <li>
              <span>02</span>
              <div>
                <h3>Let DocFlow read it</h3>
                <p>We validate, prepare and recognize the text.</p>
              </div>
            </li>
            <li>
              <span>03</span>
              <div>
                <h3>Make sense of the details</h3>
                <p>Review extracted fields and the full text.</p>
              </div>
            </li>
          </ol>
        </CardContent>
      </Card>
      <Card class="main-card">
        <CardHeader class="gap-1.5 pb-4"
          ><CardTitle class="flex items-center gap-2 text-sm"
            ><Search class="size-4 text-muted-foreground" aria-hidden="true" /> Picking up where you
            left off?</CardTitle
          ><CardDescription class="text-xs leading-5"
            >Enter a job ID to open its progress or results.</CardDescription
          ></CardHeader
        ><CardContent>
          <form class="space-y-3" @submit.prevent="openJob">
            <Label for="lookup-id" class="sr-only">Job ID</Label
            ><Input
              id="lookup-id"
              v-model="lookupId"
              placeholder="Paste your job ID"
              class="font-mono text-xs"
              :aria-invalid="Boolean(lookupError)"
              aria-describedby="lookup-error"
            />
            <p v-if="lookupError" id="lookup-error" role="alert" class="text-xs text-destructive">
              {{ lookupError }}
            </p>
            <Button type="submit" variant="outline" class="w-full"
              >Open job<ArrowRight class="ml-auto size-3.5" aria-hidden="true"
            /></Button>
          </form>
          <Button
            v-if="config.mode === 'demo'"
            variant="link"
            class="mt-3 h-auto px-0 text-xs"
            @click="router.push(`/jobs/${sampleJobId}`)"
            >View a sample result <ArrowRight class="size-3" aria-hidden="true"
          /></Button>
        </CardContent>
      </Card>
      <p class="flex items-start gap-2 px-1 text-[11px] leading-5 text-muted-foreground">
        <Info class="mt-0.5 size-3.5 shrink-0" aria-hidden="true" /><span
          >Documents and results are retained for 7 days. Keep your job ID to return to a
          result.</span
        >
      </p>
    </aside>
  </div>
</template>
