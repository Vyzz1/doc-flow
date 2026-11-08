import { render } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'
import StepTimeline from './StepTimeline.vue'

describe('step outcomes', () => {
  it('renders null steps as unreported, without pretending a step is running', () => {
    const view = render(StepTimeline, { props: { steps: null, status: 'PROCESSING' } })
    // One row for each step the backend records: validate, ocr and extract.
    expect(view.getAllByText('Not reported')).toHaveLength(3)
    expect(view.queryByText('Running')).toBeNull()
  })
  it('lists only the steps the pipeline records', () => {
    const view = render(StepTimeline, { props: { steps: null, status: 'PENDING' } })
    expect(view.getAllByRole('listitem').map((item) => item.textContent)).toEqual([
      expect.stringContaining('Validate document'),
      expect.stringContaining('Prepare and recognize text'),
      expect.stringContaining('Extract fields'),
    ])
  })
  it('shows a step it does not know instead of dropping it', () => {
    const view = render(StepTimeline, {
      props: {
        steps: [{ step: 'virus_scan', status: 'SUCCEEDED', durationMs: 1500 }],
        status: 'PROCESSING',
      },
    })
    expect(view.getByText('Virus scan')).toBeInTheDocument()
    expect(view.getByText('1.50s')).toBeInTheDocument()
  })
  it('shows failed outcomes and leaves later steps unreported', () => {
    const view = render(StepTimeline, {
      props: {
        steps: [
          { step: 'validate', status: 'FAILED', durationMs: 40, error: 'Unsupported document.' },
        ],
        status: 'FAILED',
      },
    })
    expect(view.getByText('Unsupported document.')).toBeInTheDocument()
    expect(view.getAllByText('Not reported')).toHaveLength(2)
  })
})
