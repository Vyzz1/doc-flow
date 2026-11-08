import { shallowRef } from 'vue'
import { ApiError } from './api'
import type { AuthService, DocFlowApi, DocumentType, Job, Step, User } from './types'

export const sampleJobId = '01J8ZQJ9X7K3M4N5P6Q7R8S9TV'
// The three steps the backend records. Rendering and the thumbnail are inside
// `ocr`, so its time covers them.
const steps: Step[] = [
  { step: 'validate', status: 'SUCCEEDED', durationMs: 180 },
  { step: 'ocr', status: 'SUCCEEDED', durationMs: 3290 },
  { step: 'extract', status: 'SUCCEEDED', durationMs: 95 },
]
function result(docType: DocumentType): Job['result'] {
  if (docType === 'invoice')
    return {
      provider: 'tesseract',
      pages: 1,
      confidence: 0.967,
      text: 'NORTHSTAR STUDIO\nINVOICE INV-2026-042\nDate: October 30, 2026\nBill to: Acme Design Co.\n\nDesign services     $1,200.00\nConsulting           $350.00\nSubtotal           $1,550.00\nTax (10%)            $155.00\nTOTAL              $1,705.00\n\nThank you for your business.',
      fields: [
        { name: 'invoiceNumber', value: 'INV-2026-042', confidence: 0.99 },
        { name: 'invoiceDate', value: '2026-10-30', confidence: 0.97 },
        { name: 'total', value: '1705.00', confidence: 0.96 },
        { name: 'currency', value: 'USD', confidence: 0.99 },
      ],
    }
  return {
    provider: 'tesseract',
    pages: 1,
    confidence: 0.94,
    text:
      docType === 'image'
        ? 'Sample recognized text from your image. This is a demonstration, not OCR of the uploaded file.'
        : 'SAMPLE ID DOCUMENT\nName: Alex Morgan\nDocument number: SAMPLE-123456',
    fields:
      docType === 'image'
        ? []
        : [
            { name: 'fullName', value: 'Alex Morgan', confidence: 0.95 },
            { name: 'documentNumber', value: 'SAMPLE-123456', confidence: 0.93 },
          ],
  }
}
function newId(): string {
  const alphabet = '0123456789ABCDEFGHJKMNPQRSTVWXYZ'
  let time = Date.now()
  let prefix = ''
  for (let i = 0; i < 10; i++) {
    prefix = alphabet[time % 32] + prefix
    time = Math.floor(time / 32)
  }
  return (
    prefix +
    [...crypto.getRandomValues(new Uint8Array(16))].map((value) => alphabet[value % 32]).join('')
  )
}
export function createDemo() {
  const user = shallowRef<User>()
  const records = new Map<string, { type: DocumentType; created: number; uploaded?: number }>()
  function seedSample() {
    records.set(sampleJobId, {
      type: 'invoice',
      created: Date.now() - 15000,
      uploaded: Date.now() - 15000,
    })
  }
  seedSample()
  const auth: AuthService = {
    user,
    async restore() {},
    async signIn() {
      if (!records.size) seedSample()
      user.value = { id: 'demo-user', email: 'alex@demo.docflow' }
      return 'DONE'
    },
    async confirmPassword() {
      return 'DONE'
    },
    async token() {
      return 'local-demo-token'
    },
    async signOut() {
      user.value = undefined
      records.clear()
    },
  }
  const api: DocFlowApi = {
    async listJobs(options = {}, signal) {
      signal?.throwIfAborted()
      const limit = options.limit ?? 20
      if (
        !Number.isInteger(limit) ||
        limit < 1 ||
        limit > 50 ||
        (options.next && !/^[0-7][0-9A-HJKMNP-TV-Z]{25}$/.test(options.next))
      ) {
        throw new ApiError(400, 'Invalid page size or job cursor.')
      }
      const ids = [...records.keys()]
        .sort()
        .reverse()
        .filter((id) => !options.next || id < options.next)
      const selected = ids.slice(0, limit)
      const jobs = await Promise.all(
        selected.map(async (id) => {
          const job = await api.getJob(id, signal)
          return {
            jobId: job.jobId,
            status: job.status,
            docType: job.docType,
            createdAt: job.createdAt,
            updatedAt: job.updatedAt,
            failureReason: job.failureReason,
          }
        }),
      )
      return { jobs, next: ids.length > limit ? selected.at(-1) : undefined }
    },
    async deleteJob(id, signal) {
      signal?.throwIfAborted()
      const job = await api.getJob(id, signal)
      if (job.status === 'PROCESSING') throw new ApiError(409, 'the job is still being processed')
      records.delete(id)
    },
    async createJob(docType) {
      const jobId = newId()
      records.set(jobId, { type: docType, created: Date.now() })
      return {
        jobId,
        status: 'PENDING',
        uploadUrl: 'demo://local-only',
        uploadFields: {},
        maxBytes: 10485760,
        expiresAt: new Date(Date.now() + 900000).toISOString(),
      }
    },
    async upload(ticket, _file, signal) {
      signal?.throwIfAborted()
      const record = records.get(ticket.jobId)
      if (!record) throw new ApiError(404, 'Demo job not found.')
      record.uploaded = Date.now()
    },
    async getJob(jobId, signal) {
      signal?.throwIfAborted()
      const record = records.get(jobId)
      if (!record)
        throw new ApiError(404, 'Job not found. Demo jobs exist only in this page session.')
      const elapsed = record.uploaded ? Date.now() - record.uploaded : 0
      const status = !record.uploaded ? 'PENDING' : elapsed >= 9000 ? 'DONE' : 'PROCESSING'
      return {
        jobId,
        status,
        docType: record.type,
        createdAt: new Date(record.created).toISOString(),
        updatedAt: new Date(
          record.uploaded ? Math.min(Date.now(), record.uploaded + 9000) : record.created,
        ).toISOString(),
        steps: status === 'DONE' ? steps : steps.slice(0, Math.floor(elapsed / 3000)),
        result: status === 'DONE' ? result(record.type) : undefined,
      }
    },
  }
  return { api, auth }
}
