import { fireEvent, render, waitFor, within } from '@testing-library/vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { createMemoryHistory, createRouter } from 'vue-router'
import { describe, expect, it } from 'vitest'
import JobsView from './JobsView.vue'
import JobView from './JobView.vue'
import { createDemo } from '@/lib/demo'
import { ApiError } from '@/lib/api'
import { runtimeKey } from '@/runtime'
import type { DocFlowApi } from '@/lib/types'
import { doneJob, jobId } from '@/test/fixtures'

async function mountJobs(overrides: Partial<DocFlowApi> = {}, detail = false) {
  const local = createDemo()
  await local.auth.signIn('', '')
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/jobs', component: JobsView },
      { path: '/jobs/:id', component: JobView },
      { path: '/', component: JobsView },
    ],
  })
  await router.push(detail ? `/jobs/${jobId}` : '/jobs')
  await router.isReady()
  const view = render(detail ? JobView : JobsView, {
    global: {
      provide: {
        [runtimeKey as symbol]: {
          ...local,
          api: { ...local.api, ...overrides },
          queryClient: client,
          config: { mode: 'real' },
        },
      },
      plugins: [router, [VueQueryPlugin, { queryClient: client }]],
    },
  })
  return {
    ...view,
    router,
    client,
    dispose() {
      view.unmount()
      client.clear()
    },
  }
}

describe('My documents', () => {
  it('shows server pages, links to details and disables deletion for processing jobs', async () => {
    const otherId = '01J8ZQJ9X7K3M4N5P6Q7R8S9TW'
    const view = await mountJobs({
      listJobs: async (options) =>
        options?.next
          ? { jobs: [{ ...doneJob, jobId: otherId, status: 'PROCESSING' }] }
          : { jobs: [doneJob], next: jobId },
    })
    expect(await view.findByRole('link', { name: `Open job ${jobId}` })).toBeInTheDocument()
    await fireEvent.click(view.getByRole('button', { name: 'Load more' }))
    const second = await view.findByRole('link', { name: `Open job ${otherId}` })
    expect(
      within(second.closest('tr')!).getByRole('button', { name: 'Delete document' }),
    ).toBeDisabled()
    expect(view.getByRole('link', { name: `Open job ${jobId}` })).toBeInTheDocument()
    await fireEvent.click(second)
    await waitFor(() => expect(view.router.currentRoute.value.path).toBe(`/jobs/${otherId}`))
    view.dispose()
  })
  it('shows an empty state and an upload link', async () => {
    const view = await mountJobs({ listJobs: async () => ({ jobs: [] }) })
    expect(await view.findByText('No documents yet')).toBeInTheDocument()
    expect(view.getByRole('link', { name: 'Upload your first document' })).toHaveAttribute(
      'href',
      '/',
    )
    view.dispose()
  })
  it('shows a permanent listing failure without pretending the list is empty', async () => {
    const view = await mountJobs({
      listJobs: async () => {
        throw new ApiError(403, 'Access denied.')
      },
    })
    expect(await view.findByRole('alert')).toHaveTextContent('Access denied.')
    expect(view.queryByText('No documents yet')).toBeNull()
    view.dispose()
  })
  it('only deletes after explicit confirmation, then removes the row', async () => {
    let deleted = false
    let attempts = 0
    const view = await mountJobs({
      listJobs: async () => ({ jobs: deleted ? [] : [doneJob] }),
      deleteJob: async () => {
        attempts++
        deleted = true
      },
    })
    await view.findByRole('link', { name: `Open job ${jobId}` })
    await fireEvent.click(view.getByRole('button', { name: 'Delete document' }))
    let dialog = await view.findByRole('alertdialog')
    expect(attempts).toBe(0)
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    expect(attempts).toBe(0)
    await fireEvent.click(await view.findByRole('button', { name: 'Delete document' }))
    dialog = await view.findByRole('alertdialog')
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Delete permanently' }))
    await waitFor(() => expect(view.queryByRole('link', { name: `Open job ${jobId}` })).toBeNull())
    expect(await view.findByText('Document deleted.')).toBeInTheDocument()
    expect(attempts).toBe(1)
    view.dispose()
  })
  it('keeps partial-deletion feedback visible even when the refreshed list loses the row', async () => {
    let deleted = false
    const explanation =
      'the job was deleted, but its files could not be removed; they expire on their own'
    const view = await mountJobs({
      listJobs: async () => ({ jobs: deleted ? [] : [doneJob] }),
      deleteJob: async () => {
        deleted = true
        throw new ApiError(500, explanation)
      },
    })
    await view.findByRole('link', { name: `Open job ${jobId}` })
    await fireEvent.click(view.getByRole('button', { name: 'Delete document' }))
    const dialog = await view.findByRole('alertdialog')
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Delete permanently' }))
    expect(await view.findByRole('alert')).toHaveTextContent(explanation)
    expect(view.getByRole('alertdialog')).toBeInTheDocument()
    expect(view.queryByRole('link', { name: `Open job ${jobId}` })).toBeNull()
    view.dispose()
  })
  it('offers deletion on details and returns to the document list on success', async () => {
    const view = await mountJobs({}, true)
    await view.findByRole('button', { name: 'Delete document' })
    await fireEvent.click(view.getByRole('button', { name: 'Delete document' }))
    const dialog = await view.findByRole('alertdialog')
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Delete permanently' }))
    await waitFor(() => expect(view.router.currentRoute.value.path).toBe('/jobs'))
    view.dispose()
  })
})
