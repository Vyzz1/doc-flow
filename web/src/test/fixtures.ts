import type { Job, UploadTicket } from '@/lib/types'

export const jobId = '01J8ZQJ9X7K3M4N5P6Q7R8S9TV'
export const ticket: UploadTicket = {
  jobId, status: 'PENDING', uploadUrl: 'https://uploads.example.test/',
  uploadFields: { key: 'uploads/user/job', policy: 'signed-policy', 'Content-Type': 'image/png' },
  maxBytes: 10485760, expiresAt: '2099-10-30T00:00:00Z',
}
export const doneJob: Job = {
  jobId, status: 'DONE', docType: 'invoice',
  createdAt: '2026-10-30T08:00:00Z', updatedAt: '2026-10-30T08:00:10Z',
  steps: [{ step: 'validate', status: 'SUCCEEDED', durationMs: 200 }],
  result: {
    provider: 'tesseract', confidence: 0.96, pages: 1,
    text: '<script>alert("document")</script>',
    fields: [{ name: 'invoice_number', value: 'INV-2026-042', confidence: 0.98 }],
  },
}
