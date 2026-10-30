import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createCognitoAuth } from './auth'

const sdk = vi.hoisted(() => ({
  signIn: vi.fn(), confirmSignIn: vi.fn(), signOut: vi.fn(), fetchAuthSession: vi.fn(),
}))
vi.mock('aws-amplify/auth', () => sdk)
vi.mock('aws-amplify', () => ({ Amplify: { configure: vi.fn() } }))
vi.mock('aws-amplify/auth/cognito', () => ({ cognitoUserPoolsTokenProvider: { setKeyValueStorage: vi.fn() } }))
const config = { mode: 'real' as const, apiBaseUrl: 'https://api.example.test', userPoolId: 'region_pool', clientId: 'client' }
beforeEach(() => vi.resetAllMocks())

describe('Cognito session state', () => {
  it('supports a temporary password challenge and derives user identity from the ID token', async () => {
    sdk.signIn.mockResolvedValue({ isSignedIn: false, nextStep: { signInStep: 'CONFIRM_SIGN_IN_WITH_NEW_PASSWORD_REQUIRED' } })
    sdk.confirmSignIn.mockResolvedValue({ isSignedIn: true, nextStep: { signInStep: 'DONE' } })
    sdk.fetchAuthSession.mockResolvedValue({ tokens: { idToken: { payload: { sub: 'verified-user', email: 'person@example.test' }, toString: () => 'id-token' } } })
    const auth = createCognitoAuth(config)
    expect(await auth.signIn('person@example.test', 'temporary')).toBe('NEW_PASSWORD')
    expect(auth.user.value).toBeUndefined()
    expect(await auth.confirmPassword('new-password')).toBe('DONE')
    expect(auth.user.value).toEqual({ id: 'verified-user', email: 'person@example.test' })
    expect(await auth.token()).toBe('id-token')
  })
  it('does not keep a user when token refresh fails', async () => {
    sdk.fetchAuthSession.mockResolvedValueOnce({ tokens: { idToken: { payload: { sub: 'user' }, toString: () => 'token' } } }).mockRejectedValue(new Error('expired'))
    const auth = createCognitoAuth(config)
    await auth.restore()
    expect(auth.user.value?.id).toBe('user')
    await expect(auth.token(true)).rejects.toThrow()
    expect(auth.user.value).toBeUndefined()
  })
  it('keeps the local account cleared even if sign-out fails', async () => {
    sdk.signOut.mockRejectedValue(new Error('offline'))
    const auth = createCognitoAuth(config)
    auth.user.value = { id: 'old-user', email: 'old@example.test' }
    await expect(auth.signOut()).rejects.toThrow('offline')
    expect(auth.user.value).toBeUndefined()
  })
})
