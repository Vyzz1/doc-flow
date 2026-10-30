import { describe, expect, it } from 'vitest'
import { useUpload } from './useUpload'
import type { DocFlowApi } from '@/lib/types'
import { doneJob, ticket } from '@/test/fixtures'

function fakeApi(overrides: Partial<DocFlowApi> = {}): DocFlowApi {
  return {
    createJob: async () => ticket,
    upload: async () => {},
    getJob: async () => doneJob,
    ...overrides,
  }
}

describe('upload recovery', () => {
  it('retains the job ID on upload failure and reuses its signed ticket on retry', async () => {
    let creates = 0
    let uploads = 0
    const state = useUpload(
      fakeApi({
        createJob: async () => {
          creates++
          return ticket
        },
        upload: async () => {
          if (++uploads === 1) throw new Error('Network disconnected')
        },
      }),
    )
    state.file.value = new File(['invoice'], 'invoice.png', { type: 'image/png' })
    expect(await state.submit()).toBeUndefined()
    expect(state.ticket.value?.jobId).toBe(ticket.jobId)
    expect(state.error.value).toContain('Network disconnected')
    expect(await state.submit()).toBe(ticket.jobId)
    expect(creates).toBe(1)
    expect(uploads).toBe(2)
  })
  it('does not create another job when a signed ticket has expired', async () => {
    let creates = 0
    const state = useUpload(
      fakeApi({
        createJob: async () => {
          creates++
          return { ...ticket, expiresAt: '2000-01-01T00:00:00Z' }
        },
      }),
    )
    state.file.value = new File(['invoice'], 'invoice.png', { type: 'image/png' })
    await state.submit()
    await state.submit()
    expect(creates).toBe(1)
    expect(state.error.value).toContain('expired')
  })
  it('prevents double submissions while an upload is in flight', async () => {
    let creates = 0
    let finish!: () => void
    const state = useUpload(
      fakeApi({
        createJob: async () => {
          creates++
          return ticket
        },
        upload: () =>
          new Promise((resolve) => {
            finish = resolve
          }),
      }),
    )
    state.file.value = new File(['invoice'], 'invoice.png', { type: 'image/png' })
    const first = state.submit()
    await Promise.resolve()
    await state.submit()
    expect(creates).toBe(1)
    finish()
    await first
  })
  it('rejects a PDF ID document without creating a job', async () => {
    let creates = 0
    const state = useUpload(
      fakeApi({
        createJob: async () => {
          creates++
          return ticket
        },
      }),
    )
    state.docType.value = 'id_document'
    state.file.value = new File(['pdf'], 'id.pdf', { type: 'application/pdf' })
    await state.submit()
    expect(creates).toBe(0)
    expect(state.error.value).toContain('not supported')
  })
})
