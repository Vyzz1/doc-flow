import { describe, expect, it, vi } from 'vitest'
import { createDemo, sampleJobId } from './demo'

describe('demo job management', () => {
  it('lists newest first, follows the cursor, and removes deleted jobs', async () => {
    let now = Date.now()
    vi.spyOn(Date, 'now').mockImplementation(() => now)
    const { api } = createDemo()
    const first = await api.createJob('invoice', 'image/png')
    now += 1000
    const second = await api.createJob('image', 'image/png')
    const page1 = await api.listJobs({ limit: 1 })
    expect(page1.jobs.map((job) => job.jobId)).toEqual([second.jobId])
    expect(page1.next).toBe(second.jobId)
    const page2 = await api.listJobs({ limit: 1, next: page1.next })
    expect(page2.jobs.map((job) => job.jobId)).toEqual([first.jobId])
    const page3 = await api.listJobs({ limit: 1, next: page2.next })
    expect(page3.jobs.map((job) => job.jobId)).toEqual([sampleJobId])
    expect(page3.next).toBeUndefined()
    await api.deleteJob(first.jobId)
    expect((await api.listJobs()).jobs.map((job) => job.jobId)).not.toContain(first.jobId)
    await expect(api.getJob(first.jobId)).rejects.toMatchObject({ status: 404 })
  })
  it('refuses deleting a processing job until it completes', async () => {
    let now = Date.now()
    vi.spyOn(Date, 'now').mockImplementation(() => now)
    const { api } = createDemo()
    const ticket = await api.createJob('invoice', 'image/png')
    await api.upload(ticket, new File(['image'], 'invoice.png'))
    await expect(api.deleteJob(ticket.jobId)).rejects.toMatchObject({ status: 409 })
    expect((await api.getJob(ticket.jobId)).status).toBe('PROCESSING')
    now += 10000
    await expect(api.deleteJob(ticket.jobId)).resolves.toBeUndefined()
  })
  it('does not resurrect a deleted sample result', async () => {
    const { api } = createDemo()
    await api.deleteJob(sampleJobId)
    await expect(api.getJob(sampleJobId)).rejects.toMatchObject({ status: 404 })
    expect((await api.listJobs()).jobs).toEqual([])
  })
})
