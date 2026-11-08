import { defineComponent, h, ref } from 'vue'
import { render } from '@testing-library/vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { describe, expect, it } from 'vitest'
import { useDeleteJob } from './useDeleteJob'
import { ApiError } from '@/lib/api'
import { createDemo } from '@/lib/demo'
import { doneJob, jobId } from '@/test/fixtures'

function mountDelete(deleteJob: (id: string, signal?: AbortSignal) => Promise<void>) {
  const client = new QueryClient()
  const subject = ref<string | undefined>('user-a')
  const local = createDemo()
  let remove!: ReturnType<typeof useDeleteJob>['remove']
  const view = render(
    defineComponent({
      setup() {
        remove = useDeleteJob({ ...local.api, deleteJob }, subject).remove
        return () => h('div')
      },
    }),
    { global: { plugins: [[VueQueryPlugin, { queryClient: client }]] } },
  )
  client.setQueryData(['jobs', 'user-a'], { pages: [{ jobs: [doneJob] }], pageParams: [undefined] })
  client.setQueryData(['job', 'user-a', jobId], doneJob)
  return { client, subject, view, remove: (id: string) => remove(id) }
}

describe('delete mutation lifecycle', () => {
  it('cancels an in-flight detail read and removes its cached result after deletion', async () => {
    const state = mountDelete(async () => {})
    let signal: AbortSignal | undefined
    const read = state.client
      .fetchQuery({
        queryKey: ['job', 'user-a', jobId],
        staleTime: 0,
        queryFn: (context) => {
          signal = context.signal
          return new Promise(() => {})
        },
      })
      .catch(() => undefined)
    const result = await state.remove(jobId)
    expect(result.alreadyMissing).toBe(false)
    expect(signal?.aborted).toBe(true)
    expect(state.client.getQueryData(['job', 'user-a', jobId])).toBeUndefined()
    expect(state.client.getQueryState(['jobs', 'user-a'])?.isInvalidated).toBe(true)
    await read
    state.view.unmount()
    state.client.clear()
  })
  it('treats a 404 as already removed and refreshes the list', async () => {
    const state = mountDelete(async () => {
      throw new ApiError(404, 'job not found')
    })
    expect((await state.remove(jobId)).alreadyMissing).toBe(true)
    expect(state.client.getQueryData(['job', 'user-a', jobId])).toBeUndefined()
    expect(state.client.getQueryState(['jobs', 'user-a'])?.isInvalidated).toBe(true)
    state.view.unmount()
    state.client.clear()
  })
  it.each([409, 500])(
    'keeps an uncertain/conflicted job visible and refreshes caches after %s without retries',
    async (status) => {
      let attempts = 0
      const state = mountDelete(async () => {
        attempts++
        throw new ApiError(status, 'delete outcome')
      })
      await expect(state.remove(jobId)).rejects.toMatchObject({ status })
      expect(attempts).toBe(1)
      expect(state.client.getQueryData(['job', 'user-a', jobId])).toEqual(doneJob)
      expect(state.client.getQueryState(['jobs', 'user-a'])?.isInvalidated).toBe(true)
      state.view.unmount()
      state.client.clear()
    },
  )
  it('does not clear a new account cache when an old deletion finishes', async () => {
    let finish!: () => void
    const state = mountDelete(
      () =>
        new Promise((resolve) => {
          finish = resolve
        }),
    )
    const deletion = state.remove(jobId)
    await Promise.resolve()
    await Promise.resolve()
    state.subject.value = 'user-b'
    state.client.setQueryData(['job', 'user-b', jobId], { ...doneJob, docType: 'image' })
    state.client.setQueryData(['jobs', 'user-b'], {
      pages: [{ jobs: [] }],
      pageParams: [undefined],
    })
    finish()
    await deletion
    expect(state.client.getQueryData(['job', 'user-b', jobId])).toMatchObject({ docType: 'image' })
    expect(state.client.getQueryState(['jobs', 'user-b'])?.isInvalidated).toBe(false)
    state.view.unmount()
    state.client.clear()
  })
})
