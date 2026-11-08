// @vitest-environment node
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest'
import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import { ApiError, createApi } from './api'
import { doneJob, jobId, ticket } from '@/test/fixtures'

const server = setupServer()
beforeAll(() => server.listen({ onUnhandledFrame: 'error' }))
afterEach(() => server.resetHandlers())
afterAll(() => server.close())

describe('real HTTP adapter with local interception', () => {
  it('lists the next page with the backend query names and authentication', async () => {
    server.use(
      http.get('https://api.example.test/jobs', ({ request }) => {
        const url = new URL(request.url)
        expect(url.searchParams.get('limit')).toBe('20')
        expect(url.searchParams.get('next')).toBe(jobId)
        expect(request.headers.get('authorization')).toBe('Bearer id-token')
        return HttpResponse.json({ jobs: [doneJob], next: jobId })
      }),
    )
    const api = createApi('https://api.example.test', async () => 'id-token')
    expect(await api.listJobs({ limit: 20, next: jobId })).toMatchObject({
      jobs: [{ jobId }],
      next: jobId,
    })
  })
  it('accepts an empty 204 when deleting without attempting to parse JSON', async () => {
    server.use(
      http.delete(`https://api.example.test/jobs/${jobId}`, ({ request }) => {
        expect(request.headers.get('authorization')).toBe('Bearer id-token')
        return new HttpResponse(null, { status: 204 })
      }),
    )
    await expect(
      createApi('https://api.example.test', async () => 'id-token').deleteJob(jobId),
    ).resolves.toBeUndefined()
  })
  it('preserves the backend explanation when a delete has partially succeeded', async () => {
    const explanation =
      'the job was deleted, but its files could not be removed; they expire on their own'
    server.use(
      http.delete(`https://api.example.test/jobs/${jobId}`, () =>
        HttpResponse.json({ error: explanation }, { status: 500 }),
      ),
    )
    await expect(
      createApi('https://api.example.test', async () => 'token').deleteJob(jobId),
    ).rejects.toMatchObject({ status: 500, message: explanation })
  })
  it('does not retry a processing conflict and keeps its message', async () => {
    let requests = 0
    server.use(
      http.delete(`https://api.example.test/jobs/${jobId}`, () => {
        requests++
        return HttpResponse.json({ error: 'the job is still being processed' }, { status: 409 })
      }),
    )
    await expect(
      createApi('https://api.example.test', async () => 'token').deleteJob(jobId),
    ).rejects.toMatchObject({ status: 409, message: 'the job is still being processed' })
    expect(requests).toBe(1)
  })
  it('rejects a malformed delete ID before sending it to AWS', async () => {
    await expect(
      createApi('https://api.example.test', async () => 'token').deleteJob('../other'),
    ).rejects.toMatchObject({ status: 400 })
  })
  it('serializes create-job and attaches the ID token only to the API', async () => {
    server.use(
      http.post('https://api.example.test/jobs', async ({ request }) => {
        expect(request.headers.get('authorization')).toBe('Bearer id-token')
        expect(await request.json()).toEqual({ docType: 'invoice', contentType: 'image/png' })
        return HttpResponse.json(ticket, { status: 201 })
      }),
    )
    expect(
      await createApi('https://api.example.test', async () => 'id-token').createJob(
        'invoice',
        'image/png',
      ),
    ).toEqual(ticket)
  })
  it('uploads all signed fields before the file, without leaking API authorization', async () => {
    server.use(
      http.post('https://uploads.example.test/', async ({ request }) => {
        expect(request.headers.get('authorization')).toBeNull()
        const data = await request.formData()
        expect([...data.keys()]).toEqual(['key', 'policy', 'Content-Type', 'file'])
        expect(data.get('policy')).toBe('signed-policy')
        expect((data.get('file') as File).name).toBe('invoice.png')
        return new HttpResponse(null, { status: 204 })
      }),
    )
    await expect(
      createApi('https://api.example.test', async () => 'id-token').upload(
        ticket,
        new File(['data'], 'invoice.png', { type: 'image/png' }),
      ),
    ).resolves.toBeUndefined()
  })
  it('refreshes once after a 401 and returns the real job response', async () => {
    let reads = 0
    server.use(
      http.get(`https://api.example.test/jobs/${jobId}`, () =>
        ++reads === 1 ? new HttpResponse(null, { status: 401 }) : HttpResponse.json(doneJob),
      ),
    )
    const refreshed: boolean[] = []
    const api = createApi('https://api.example.test', async (force) => {
      refreshed.push(Boolean(force))
      return 'token'
    })
    expect((await api.getJob(jobId)).status).toBe('DONE')
    expect(refreshed).toEqual([false, true])
  })
  it('stops refreshing after a second 401', async () => {
    let requests = 0
    server.use(
      http.get(`https://api.example.test/jobs/${jobId}`, () => {
        requests++
        return new HttpResponse(null, { status: 401 })
      }),
    )
    await expect(
      createApi('https://api.example.test', async () => 'token').getJob(jobId),
    ).rejects.toMatchObject({ status: 401 })
    expect(requests).toBe(2)
  })
  it('reports a readable error for an HTML gateway failure', async () => {
    server.use(
      http.get(`https://api.example.test/jobs/${jobId}`, () =>
        HttpResponse.html('<h1>Bad gateway</h1>', { status: 502 }),
      ),
    )
    await expect(
      createApi('https://api.example.test', async () => 'token').getJob(jobId),
    ).rejects.toMatchObject({
      status: 502,
      message: 'The service is temporarily unavailable. Please try again.',
    })
  })
  it('rejects an invalid lookup before sending a request', async () => {
    await expect(
      createApi('https://api.example.test', async () => 'token').getJob('../secret'),
    ).rejects.toBeInstanceOf(ApiError)
  })
})
