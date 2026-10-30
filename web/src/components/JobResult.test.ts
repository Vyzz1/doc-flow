import { fireEvent, render } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'
import JobResult from './JobResult.vue'
import { doneJob } from '@/test/fixtures'

describe('document results', () => {
  it('shows extracted fields and renders OCR markup as literal text', async () => {
    const view = render(JobResult, { props: { job: doneJob } })
    expect(view.getByText('INV-2026-042')).toBeInTheDocument()
    await fireEvent.mouseDown(view.getByRole('tab', { name: 'Recognized text' }), { button: 0, ctrlKey: false })
    expect(await view.findByText('<script>alert("document")</script>')).toBeInTheDocument()
    expect(view.container.querySelector('script')).toBeNull()
  })
  it('handles DONE without a stored result', () => {
    const view = render(JobResult, { props: { job: { ...doneJob, result: null } } })
    expect(view.getByText(/result is not available/i)).toBeInTheDocument()
  })
  it('handles null extracted fields without crashing', () => {
    const view = render(JobResult, { props: { job: { ...doneJob, result: { ...doneJob.result!, fields: null } } } })
    expect(view.getByText(/no structured fields/i)).toBeInTheDocument()
  })
  it('shows the safe failure reason for a FAILED job', () => {
    const view = render(JobResult, { props: { job: { ...doneJob, status: 'FAILED', result: null, failureReason: 'The file is not a supported document type.' } } })
    expect(view.getByRole('alert')).toHaveTextContent('The file is not a supported document type.')
  })
})
