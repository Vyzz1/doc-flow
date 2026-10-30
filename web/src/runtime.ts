import { inject, type InjectionKey } from 'vue'
import type { QueryClient } from '@tanstack/vue-query'
import type { AppConfig } from './lib/config'
import type { AuthService, DocFlowApi } from './lib/types'

export interface Runtime {
  config: AppConfig
  auth: AuthService
  api: DocFlowApi
  queryClient: QueryClient
}
export const runtimeKey: InjectionKey<Runtime> = Symbol('DocFlow runtime')
export function useRuntime(): Runtime {
  const runtime = inject(runtimeKey)
  if (!runtime) throw new Error('DocFlow runtime is unavailable.')
  return runtime
}
