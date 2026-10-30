import { shallowRef } from 'vue'
import type { AuthService, User } from './types'
import type { AppConfig } from './config'
import { Amplify } from 'aws-amplify'
import { confirmSignIn, fetchAuthSession, signIn, signOut } from 'aws-amplify/auth'
import { cognitoUserPoolsTokenProvider } from 'aws-amplify/auth/cognito'
import { sessionStorage } from 'aws-amplify/utils'

export function createCognitoAuth(config: AppConfig): AuthService {
  Amplify.configure({
    Auth: { Cognito: { userPoolId: config.userPoolId, userPoolClientId: config.clientId } },
  })
  cognitoUserPoolsTokenProvider.setKeyValueStorage(sessionStorage)
  const user = shallowRef<User>()
  let epoch = 0
  async function token(forceRefresh = false): Promise<string> {
    const currentEpoch = epoch
    try {
      const session = await fetchAuthSession({ forceRefresh })
      if (currentEpoch !== epoch) throw new Error('Session changed. Please sign in again.')
      const idToken = session.tokens?.idToken
      if (!idToken || typeof idToken.payload.sub !== 'string')
        throw new Error('Please sign in again.')
      user.value = { id: idToken.payload.sub, email: String(idToken.payload.email ?? 'Account') }
      return idToken.toString()
    } catch (error) {
      if (currentEpoch === epoch) user.value = undefined
      throw error
    }
  }
  async function nextStep(result: Awaited<ReturnType<typeof signIn>>) {
    if (result.isSignedIn) {
      await token()
      return 'DONE' as const
    }
    if (result.nextStep.signInStep === 'CONFIRM_SIGN_IN_WITH_NEW_PASSWORD_REQUIRED')
      return 'NEW_PASSWORD' as const
    throw new Error(
      'This account requires an additional sign-in step. Contact your administrator, or restart sign-in.',
    )
  }
  return {
    user,
    token,
    async restore() {
      try {
        await token()
      } catch {
        user.value = undefined
      }
    },
    async signIn(email, password) {
      epoch++
      user.value = undefined
      return nextStep(
        await signIn({
          username: email.trim(),
          password,
          options: { authFlowType: 'USER_SRP_AUTH' },
        }),
      )
    },
    async confirmPassword(password) {
      return nextStep(await confirmSignIn({ challengeResponse: password }))
    },
    async signOut() {
      epoch++
      user.value = undefined
      await signOut()
    },
  }
}
