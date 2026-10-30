import type { DocFlowApi, Job, UploadTicket } from './types'
import { validJobId } from './files'

export class ApiError extends Error { constructor(public status: number, message: string) { super(message) } }
export function isPermanentError(error: unknown): boolean {
  return error instanceof ApiError && error.status >= 400 && error.status < 500 && error.status !== 408 && error.status !== 429
}
export function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : 'Something went wrong. Please try again.'
}

async function timedFetch(url: string, init: RequestInit, signal?: AbortSignal): Promise<Response> {
  const controller = new AbortController()
  const abort = () => controller.abort(signal?.reason)
  if (signal?.aborted) abort()
  else signal?.addEventListener('abort', abort, { once: true })
  const timeout = setTimeout(() => controller.abort(), 60000)
  try {
    return await fetch(url, { ...init, signal: controller.signal })
  } catch (error) {
    if (signal?.aborted) throw error
    throw new ApiError(0, controller.signal.aborted
      ? 'The request timed out. Check your connection and try again.'
      : 'Could not connect. Check your connection and try again.')
  } finally {
    clearTimeout(timeout)
    signal?.removeEventListener('abort', abort)
  }
}

async function responseError(response: Response, upload = false): Promise<ApiError> {
  const defaults: Record<number, string> = {
    401: 'Your session has expired. Please sign in again.',
    403: 'You do not have permission to access this resource.',
    404: 'Job not found. Check the ID; documents expire after 7 days.',
    429: 'Too many requests. Please wait a moment and try again.',
  }
  let message = upload ? 'Upload failed. Check your connection and retry the upload.'
    : defaults[response.status] ?? 'The service is temporarily unavailable. Please try again.'
  if (!upload && response.status < 500) {
    try {
      const body = await response.json() as { message?: string; error?: string }
      if (typeof body.message === 'string') message = body.message
      else if (typeof body.error === 'string') message = body.error
    } catch { /* Gateways may return an empty or non-JSON error response. */ }
  }
  return new ApiError(response.status, message)
}

export function createApi(base: string, token: (refresh?: boolean) => Promise<string>, unauthorized?: () => void): DocFlowApi {
  async function request<T>(path: string, init: RequestInit, signal?: AbortSignal): Promise<T> {
    for (let attempt = 0; attempt < 2; attempt++) {
      let idToken: string
      try { idToken = await token(attempt === 1) } catch {
        unauthorized?.()
        throw new ApiError(401, 'Your session has expired. Please sign in again.')
      }
      signal?.throwIfAborted()
      const response = await timedFetch(`${base}${path}`, {
        ...init, headers: { ...init.headers, Authorization: `Bearer ${idToken}` },
      }, signal)
      if (response.status === 401 && attempt === 0) continue
      if (!response.ok) {
        if (response.status === 401) unauthorized?.()
        throw await responseError(response)
      }
      return await response.json() as T
    }
    throw new ApiError(401, 'Please sign in again.')
  }
  return {
    createJob(docType, contentType, signal) {
      return request<UploadTicket>('/jobs', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ docType, contentType }) }, signal)
    },
    async upload(ticket, file, signal) {
      const form = new FormData()
      for (const [key, value] of Object.entries(ticket.uploadFields)) form.append(key, value)
      form.append('file', file, file.name)
      const response = await timedFetch(ticket.uploadUrl, { method: 'POST', body: form }, signal)
      if (!response.ok) throw await responseError(response, true)
    },
    getJob(id, signal) {
      if (!validJobId(id)) return Promise.reject(new ApiError(400, 'Enter a valid 26-character job ID.'))
      return request<Job>(`/jobs/${id}`, { method: 'GET' }, signal)
    },
  }
}
