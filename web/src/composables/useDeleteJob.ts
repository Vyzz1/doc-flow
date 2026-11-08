import { getCurrentScope, onScopeDispose, type Ref } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import type { DocFlowApi } from '@/lib/types'
import { ApiError } from '@/lib/api'

export interface DeleteOutcome {
  jobId: string
  owner: string
  alreadyMissing: boolean
}
export function useDeleteJob(api: DocFlowApi, subject: Ref<string | undefined>) {
  const client = useQueryClient()
  let controller: AbortController | undefined
  if (getCurrentScope()) onScopeDispose(() => controller?.abort())
  const mutation = useMutation({
    retry: false,
    mutationFn: async ({
      jobId,
      owner,
    }: {
      jobId: string
      owner: string
    }): Promise<DeleteOutcome> => {
      if (owner !== subject.value) throw new ApiError(401, 'Please sign in again.')
      controller = new AbortController()
      try {
        await api.deleteJob(jobId, controller.signal)
        return { jobId, owner, alreadyMissing: false }
      } catch (error) {
        if (error instanceof ApiError && error.status === 404)
          return { jobId, owner, alreadyMissing: true }
        throw error
      }
    },
    onSettled: async (result, error, { owner, jobId }) => {
      if (owner !== subject.value) return
      const key = ['job', owner, jobId]
      await client.cancelQueries({ queryKey: key, exact: true })
      if (owner !== subject.value) return
      if (result) client.removeQueries({ queryKey: key, exact: true })
      else if (!(error instanceof ApiError && [401, 403].includes(error.status))) {
        void client.invalidateQueries({ queryKey: key, exact: true })
      }
      await client.invalidateQueries({ queryKey: ['jobs', owner] })
    },
  })
  function remove(jobId: string) {
    const owner = subject.value
    if (!owner) return Promise.reject(new ApiError(401, 'Please sign in again.'))
    return mutation.mutateAsync({ owner, jobId })
  }
  return { ...mutation, remove }
}
