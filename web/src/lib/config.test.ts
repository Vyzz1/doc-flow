import { describe, expect, it } from 'vitest'
import { readConfig } from './config'

describe('configuration', () => {
  it('requires an explicit demo flag and does not silently enable fake data', () => {
    expect(readConfig({}).mode).toBe('setup')
    expect(readConfig({ VITE_DEMO_MODE: 'true' }).mode).toBe('demo')
    expect(readConfig({ VITE_DEMO_MODE: 'false' }).mode).toBe('setup')
  })
  it('accepts public Cognito config and normalizes the API base', () => {
    expect(
      readConfig({
        VITE_API_BASE_URL: 'https://api.example.test/',
        VITE_COGNITO_USER_POOL_ID: 'ap-southeast-1_pool',
        VITE_COGNITO_CLIENT_ID: 'client',
      }),
    ).toMatchObject({ mode: 'real', apiBaseUrl: 'https://api.example.test' })
  })
  it('rejects non-HTTP API URLs', () => {
    expect(
      readConfig({
        VITE_API_BASE_URL: 'javascript:alert(1)',
        VITE_COGNITO_USER_POOL_ID: 'pool',
        VITE_COGNITO_CLIENT_ID: 'client',
      }).mode,
    ).toBe('setup')
  })
})
