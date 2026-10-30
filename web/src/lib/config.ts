export interface AppConfig {
  mode: 'demo' | 'real' | 'setup'
  apiBaseUrl: string
  userPoolId: string
  clientId: string
}
export function readConfig(env: Record<string, unknown>): AppConfig {
  const config: AppConfig = {
    mode: 'setup', apiBaseUrl: String(env.VITE_API_BASE_URL ?? '').replace(/\/+$/, ''),
    userPoolId: String(env.VITE_COGNITO_USER_POOL_ID ?? ''), clientId: String(env.VITE_COGNITO_CLIENT_ID ?? ''),
  }
  if (env.VITE_DEMO_MODE === 'true') return { ...config, mode: 'demo' }
  try {
    const url = new URL(config.apiBaseUrl)
    if (['https:', 'http:'].includes(url.protocol) && !url.username && !url.password && !url.search && !url.hash && config.userPoolId && config.clientId) config.mode = 'real'
  } catch { /* Incomplete configuration is handled by the setup screen. */ }
  return config
}
