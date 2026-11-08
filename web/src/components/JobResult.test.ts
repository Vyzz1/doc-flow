import { fireEvent, render } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'
import JobResult from './JobResult.vue'
import { doneJob } from '@/test/fixtures'

describe('document results', () => {
  it('shows extracted fields and renders OCR markup as literal text', async () => {
    const view = render(JobResult, { props: { job: doneJob } })
    expect(view.getByText('INV-2026-042')).toBeInTheDocument()
    await fireEvent.mouseDown(view.getByRole('tab', { name: 'Recognized text' }), {
      button: 0,
      ctrlKey: false,
    })
    expect(await view.findByText('<script>alert("document")</script>')).toBeInTheDocument()
    expect(view.container.querySelector('script')).toBeNull()
  })
  it('reads the API camelCase field names as words', () => {
    // These are the names internal/extract produces.
    const names = ['invoiceNumber', 'dateOfBirth', 'total', 'document_number']
    const view = render(JobResult, {
      props: {
        job: {
          ...doneJob,
          result: {
            ...doneJob.result!,
            fields: names.map((name) => ({ name, value: 'x', confidence: 0.9 })),
          },
        },
      },
    })
    for (const label of ['invoice number', 'date of birth', 'total', 'document number'])
      expect(view.getByRole('rowheader', { name: label })).toBeInTheDocument()
    expect(view.queryByText('invoiceNumber')).toBeNull()
  })
  it('handles DONE without a stored result', () => {
    const view = render(JobResult, { props: { job: { ...doneJob, result: null } } })
    expect(view.getByText(/result is not available/i)).toBeInTheDocument()
  })
  it('handles null extracted fields without crashing', () => {
    const view = render(JobResult, {
      props: { job: { ...doneJob, result: { ...doneJob.result!, fields: null } } },
    })
    expect(view.getByText(/no labelled fields matched/i)).toBeInTheDocument()
  })
  it('opens an image on its text and explains why it has no fields', async () => {
    const view = render(JobResult, {
      props: {
        job: {
          ...doneJob,
          docType: 'image',
          result: { ...doneJob.result!, fields: [], text: 'Hello from an image' },
        },
      },
    })
    expect(view.getByRole('tab', { name: 'Recognized text' })).toHaveAttribute(
      'aria-selected',
      'true',
    )
    expect(view.getByText('Hello from an image')).toBeInTheDocument()
    await fireEvent.mouseDown(view.getByRole('tab', { name: 'Extracted fields' }), {
      button: 0,
      ctrlKey: false,
    })
    expect(await view.findByText(/images are read as text only/i)).toBeInTheDocument()
  })
  it('keeps an invoice on its fields tab even when none matched', () => {
    const view = render(JobResult, {
      props: { job: { ...doneJob, result: { ...doneJob.result!, fields: [] } } },
    })
    expect(view.getByRole('tab', { name: 'Extracted fields' })).toHaveAttribute(
      'aria-selected',
      'true',
    )
  })
  it('shows the safe failure reason for a FAILED job', () => {
    const view = render(JobResult, {
      props: {
        job: {
          ...doneJob,
          status: 'FAILED',
          result: null,
          failureReason: 'The file is not a supported document type.',
        },
      },
    })
    expect(view.getByRole('alert')).toHaveTextContent('The file is not a supported document type.')
  })
})
