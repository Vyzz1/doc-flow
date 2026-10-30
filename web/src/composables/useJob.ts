import { useQuery } from '@tanstack/vue-query'
import { computed, type Ref } from 'vue'
import type { DocFlowApi, Job } from '@/lib/types'
import { validJobId } from '@/lib/files'
import { isPermanentError } from '@/lib/api'

export function useJob(api: DocFlowApi, subject: Ref<string | undefined>, id: Ref<string>) {
  return useQuery<Job>({
    queryKey: computed(() => ['job', subject.value, id.value]),
    enabled: computed(() => Boolean(subject.value) && validJobId(id.value)),
    queryFn: ({ signal }) => api.getJob(id.value, signal),
    refetchInterval: query => {
      if (query.state.status === 'error') return false
      const status = query.state.data?.status
      return status === 'PENDING' || status === 'PROCESSING' ? 3000 : false
    },
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: query => !isPermanentError(query.state.error) && !['DONE', 'FAILED'].includes(query.state.data?.status ?? ''),
    retry: (count, error) => !isPermanentError(error) && count < 2,
    retryDelay: attempt => Math.min(1000 * 2 ** attempt, 5000),
    staleTime: 2000,
  })
}
