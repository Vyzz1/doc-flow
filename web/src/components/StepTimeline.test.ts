import { render } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'
import StepTimeline from './StepTimeline.vue'

describe('step outcomes', () => {
  it('renders null steps as unreported, without pretending a step is running', () => {
    const view = render(StepTimeline, { props: { steps: null, status: 'PROCESSING' } })
    expect(view.getAllByText('Not reported')).toHaveLength(6)
    expect(view.queryByText('Running')).toBeNull()
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
    expect(view.getAllByText('Not reported')).toHaveLength(5)
  })
})
