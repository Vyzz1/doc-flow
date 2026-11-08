import { computed, type Ref } from 'vue'
import { useInfiniteQuery } from '@tanstack/vue-query'
import type { DocFlowApi } from '@/lib/types'
import { isPermanentError } from '@/lib/api'

export function useJobs(api: DocFlowApi, subject: Ref<string | undefined>) {
  const query = useInfiniteQuery({
    queryKey: computed(() => ['jobs', subject.value]),
    enabled: computed(() => Boolean(subject.value)),
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam, signal }) => api.listJobs({ limit: 20, next: pageParam }, signal),
    getNextPageParam: (last, _pages, _param, params) =>
      last.next && !params.includes(last.next) ? last.next : undefined,
    retry: (count, error) => !isPermanentError(error) && count < 2,
    retryDelay: (attempt) => Math.min(1000 * 2 ** attempt, 5000),
    refetchOnWindowFocus: false,
    staleTime: 0,
  })
  const jobs = computed(() => {
    const seen = new Set<string>()
    return (query.data.value?.pages.flatMap((page) => page.jobs ?? []) ?? []).filter((job) => {
      if (seen.has(job.jobId)) return false
      seen.add(job.jobId)
      return true
    })
  })
  return { ...query, jobs }
}
