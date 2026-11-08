import { defineComponent, h, ref } from 'vue'
import { fireEvent, render, waitFor } from '@testing-library/vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { describe, expect, it } from 'vitest'
import { useJobs } from './useJobs'
import { createDemo } from '@/lib/demo'
import { doneJob, jobId } from '@/test/fixtures'

describe('paginated job queries', () => {
  it('appends the server cursor page and does not replace the first page', async () => {
    const local = createDemo()
    const seen: (string | undefined)[] = []
    const client = new QueryClient()
    const secondId = '01J8ZQJ9X7K3M4N5P6Q7R8S9TW'
    const view = render(
      defineComponent({
        setup() {
          const query = useJobs(
            {
              ...local.api,
              listJobs: async (options) => {
                seen.push(options?.next)
                return options?.next
                  ? { jobs: [{ ...doneJob, jobId: secondId }] }
                  : { jobs: [doneJob], next: jobId }
              },
            },
            ref('user'),
          )
          return () =>
            h('div', [
              ...query.jobs.value.map((job) => h('p', job.jobId)),
              h(
                'button',
                { disabled: !query.hasNextPage.value, onClick: () => query.fetchNextPage() },
                'Load more',
              ),
            ])
        },
      }),
      { global: { plugins: [[VueQueryPlugin, { queryClient: client }]] } },
    )
    expect(await view.findByText(jobId)).toBeInTheDocument()
    await fireEvent.click(view.getByRole('button', { name: 'Load more' }))
    expect(await view.findByText(secondId)).toBeInTheDocument()
    expect(view.getByText(jobId)).toBeInTheDocument()
    expect(seen).toEqual([undefined, jobId])
    expect(view.getByRole('button', { name: 'Load more' })).toBeDisabled()
    view.unmount()
    client.clear()
  })
  it('does not show the old account list while a new account is loading', async () => {
    const subject = ref<string | undefined>('user-a')
    const local = createDemo()
    const client = new QueryClient()
    let oldSignal: AbortSignal | undefined
    const view = render(
      defineComponent({
        setup() {
          const query = useJobs(
            {
              ...local.api,
              listJobs: async (_options, signal) => {
                if (subject.value === 'user-a') {
                  oldSignal = signal
                  return { jobs: [doneJob] }
                }
                return new Promise(() => {})
              },
            },
            subject,
          )
          return () =>
            h(
              'div',
              query.jobs.value.map((job) => h('p', job.jobId)),
            )
        },
      }),
      { global: { plugins: [[VueQueryPlugin, { queryClient: client }]] } },
    )
    expect(await view.findByText(jobId)).toBeInTheDocument()
    subject.value = 'user-b'
    await waitFor(() => expect(view.queryByText(jobId)).toBeNull())
    expect(oldSignal).toBeDefined()
    view.unmount()
    client.clear()
  })
})
