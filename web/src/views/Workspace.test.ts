import { fireEvent, render, waitFor } from '@testing-library/vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { createMemoryHistory, createRouter } from 'vue-router'
import { defineComponent, shallowRef } from 'vue'
import { describe, expect, it } from 'vitest'
import SignInView from './SignInView.vue'
import UploadView from './UploadView.vue'
import { runtimeKey, type Runtime } from '@/runtime'
import { createDemo } from '@/lib/demo'

async function renderView(component: typeof SignInView | typeof UploadView, overrides: Partial<Runtime> = {}) {
  const local = createDemo()
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const runtime: Runtime = { ...local, queryClient, config: { mode: 'demo', apiBaseUrl: '', userPoolId: '', clientId: '' }, ...overrides }
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: '/', component }, { path: '/jobs/:id', component: defineComponent({ template: '<div>Job opened</div>' }) },
  ] })
  await router.push('/')
  await router.isReady()
  const view = render(component, { global: { provide: { [runtimeKey as symbol]: runtime }, plugins: [router, [VueQueryPlugin, { queryClient }]] } })
  return { ...view, router, queryClient }
}

describe('workspace interactions', () => {
  it('completes the new-password challenge before navigating to the workspace', async () => {
    const local = createDemo()
    const view = await renderView(SignInView, {
      config: { mode: 'real', apiBaseUrl: 'https://api.example.test', userPoolId: 'region_pool', clientId: 'client' },
      auth: { ...local.auth, user: shallowRef(), signIn: async () => 'NEW_PASSWORD', confirmPassword: async () => 'DONE' },
    })
    await fireEvent.update(view.getByLabelText('Email address'), 'person@example.test')
    await fireEvent.update(view.getByLabelText('Password', { exact: true }), 'temporary')
    await fireEvent.submit(view.getByRole('button', { name: 'Sign in to workspace' }).closest('form')!)
    await waitFor(() => expect(view.getByLabelText('New password')).toBeInTheDocument())
    expect(view.queryByLabelText('Password', { exact: true })).toBeNull()
    await fireEvent.update(view.getByLabelText('New password'), 'NewPassword123!')
    await fireEvent.submit(view.getByRole('button', { name: 'Set password & continue' }).closest('form')!)
    await waitFor(() => expect(view.queryByRole('alert')).toBeNull())
    view.queryClient.clear()
  })
  it('gives visible feedback and prevents uploading a PDF as an ID', async () => {
    const view = await renderView(UploadView)
    await fireEvent.click(view.getByRole('radio', { name: /ID document/ }))
    await fireEvent.change(view.getByLabelText('Choose your file'), { target: { files: [new File(['pdf'], 'id.pdf', { type: 'application/pdf' })] } })
    expect(view.getByRole('alert')).toHaveTextContent('not supported')
    expect(view.getByRole('button', { name: 'Upload & process' })).toBeDisabled()
    view.queryClient.clear()
  })
  it('takes a demo document from file selection to its job route', async () => {
    const view = await renderView(UploadView)
    await fireEvent.change(view.getByLabelText('Choose your file'), { target: { files: [new File(['image'], 'invoice.png', { type: 'image/png' })] } })
    await fireEvent.submit(view.getByRole('button', { name: 'Upload & process' }).closest('form')!)
    await waitFor(() => expect(view.router.currentRoute.value.path).toMatch(/^\/jobs\/[0-7][0-9A-HJKMNP-TV-Z]{25}$/))
    view.queryClient.clear()
  })
})
