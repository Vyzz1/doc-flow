import { defineComponent, h, ref } from 'vue'
import { render, waitFor } from '@testing-library/vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { describe, expect, it } from 'vitest'
import { useJob } from './useJob'
import { ApiError } from '@/lib/api'
import type { DocFlowApi, Job } from '@/lib/types'
import { doneJob, jobId } from '@/test/fixtures'

describe('reactive job query', () => {
  it('scopes cached data by account and cancels the old account request', async () => {
    const subject = ref<string | undefined>('user-a')
    let oldSignal: AbortSignal | undefined
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const api: DocFlowApi = {
      createJob: async () => {
        throw new Error('unused')
      },
      upload: async () => {},
      getJob: async (_id, signal) => {
        if (subject.value === 'user-a') {
          oldSignal = signal
          return new Promise<Job>(() => {})
        }
        return { ...doneJob, docType: 'image' }
      },
    }
    const view = render(
      defineComponent({
        setup() {
          const job = useJob(api, subject, ref(jobId))
          return () => h('div', job.data.value?.docType ?? 'Loading')
        },
      }),
      { global: { plugins: [[VueQueryPlugin, { queryClient: client }]] } },
    )
    await waitFor(() => expect(oldSignal).toBeDefined())
    subject.value = 'user-b'
    await waitFor(() => expect(view.getByText('image')).toBeTruthy())
    expect(oldSignal?.aborted).toBe(true)
    expect(client.getQueryData(['job', 'user-a', jobId])).toBeUndefined()
    client.clear()
  })
  it.each(['DONE', 'FAILED'] as const)('does not poll a %s job again', async (status) => {
    let reads = 0
    const api: DocFlowApi = {
      createJob: async () => {
        throw new Error('unused')
      },
      upload: async () => {},
      getJob: async () => {
        reads++
        return { ...doneJob, status }
      },
    }
    const client = new QueryClient()
    render(
      defineComponent({
        setup() {
          const query = useJob(api, ref('user'), ref(jobId))
          return () => h('div', query.data.value?.status)
        },
      }),
      { global: { plugins: [[VueQueryPlugin, { queryClient: client }]] } },
    )
    await waitFor(() =>
      expect(client.getQueryData(['job', 'user', jobId])).toMatchObject({ status }),
    )
    // Query's configured interval must stop after receiving the terminal result.
    const query = client.getQueryCache().find({ queryKey: ['job', 'user', jobId] })!
    const interval = (query.options as { refetchInterval: (query: unknown) => number | false })
      .refetchInterval
    expect(interval(query)).toBe(false)
    expect(reads).toBe(1)
    client.clear()
  })
  it('stops automatic polling after a permanent error', async () => {
    const client = new QueryClient()
    render(
      defineComponent({
        setup() {
          useJob(
            {
              createJob: async () => {
                throw new Error('unused')
              },
              upload: async () => {},
              getJob: async () => {
                throw new ApiError(404, 'Job not found.')
              },
            },
            ref('user'),
            ref(jobId),
          )
          return () => h('div')
        },
      }),
      { global: { plugins: [[VueQueryPlugin, { queryClient: client }]] } },
    )
    await waitFor(() => expect(client.getQueryState(['job', 'user', jobId])?.status).toBe('error'))
    const query = client.getQueryCache().find({ queryKey: ['job', 'user', jobId] })!
    expect(
      (query.options as { refetchInterval: (query: unknown) => number | false }).refetchInterval(
        query,
      ),
    ).toBe(false)
    client.clear()
  })
})
