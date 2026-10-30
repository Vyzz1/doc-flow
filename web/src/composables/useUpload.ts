import { getCurrentScope, onScopeDispose, ref, shallowRef } from 'vue'
import type { DocFlowApi, DocumentType, UploadTicket } from '@/lib/types'
import { errorMessage } from '@/lib/api'
import { inspectFile } from '@/lib/files'

export function useUpload(api: DocFlowApi) {
  const file = shallowRef<File>()
  const docType = ref<DocumentType>('invoice')
  const ticket = shallowRef<UploadTicket>()
  const error = ref('')
  const pending = ref(false)
  const phase = ref<'idle' | 'creating' | 'uploading'>('idle')
  let signedFile: File | undefined
  let controller: AbortController | undefined
  if (getCurrentScope()) onScopeDispose(() => controller?.abort())

  function reset() {
    if (pending.value) return
    file.value = undefined
    signedFile = undefined
    ticket.value = undefined
    error.value = ''
  }
  async function submit(): Promise<string | undefined> {
    if (pending.value) return
    error.value = ''
    const selected = signedFile ?? file.value
    if (!selected) { error.value = 'Choose a document to upload.'; return }
    const inspected = inspectFile(selected, docType.value)
    if (inspected.error) { error.value = inspected.error; return }
    pending.value = true
    controller = new AbortController()
    try {
      if (!ticket.value) {
        phase.value = 'creating'
        ticket.value = await api.createJob(docType.value, inspected.contentType!, controller.signal)
        signedFile = selected
      }
      if (Date.parse(ticket.value.expiresAt) <= Date.now()) throw new Error('This upload has expired. Start a new submission to continue.')
      if (selected.size > ticket.value.maxBytes) throw new Error('This file exceeds the server upload limit. Start a new submission with a smaller file.')
      phase.value = 'uploading'
      await api.upload(ticket.value, selected, controller.signal)
      return ticket.value.jobId
    } catch (failure) {
      if (!controller.signal.aborted) error.value = errorMessage(failure)
    } finally {
      phase.value = 'idle'
      pending.value = false
    }
  }
  return { file, docType, ticket, error, pending, phase, submit, reset }
}
