import { describe, expect, it } from 'vitest'
import { inspectFile, validJobId } from './files'

describe('document input', () => {
  it.each([
    ['invoice', 'scan.pdf', 'application/pdf', undefined],
    ['id_document', 'scan.pdf', 'application/pdf', 'not supported'],
    ['image', 'scan.tif', '', undefined],
    ['invoice', 'scan.exe', '', 'not supported'],
  ] as const)('validates %s %s against the backend allowlist', (type, name, mime, error) => {
    const result = inspectFile(new File(['content'], name, { type: mime }), type)
    if (error) expect(result.error).toContain(error)
    else expect(result.contentType).toBe(name.endsWith('.tif') ? 'image/tiff' : mime)
  })
  it('rejects empty and oversized documents before creating a job', () => {
    expect(inspectFile(new File([], 'empty.png', { type: 'image/png' }), 'image').error).toContain(
      'empty',
    )
    expect(
      inspectFile(new File([new Uint8Array(10485761)], 'large.png', { type: 'image/png' }), 'image')
        .error,
    ).toContain('10 MiB')
  })
  it.each(['../../other', '81J8ZQJ9X7K3M4N5P6Q7R8S9TV', '01j8zqj9x7k3m4n5p6q7r8s9tv', ''])(
    'rejects malformed job ID %s',
    (id) => {
      expect(validJobId(id)).toBe(false)
    },
  )
  it('accepts a canonical ULID', () => {
    expect(validJobId('01J8ZQJ9X7K3M4N5P6Q7R8S9TV')).toBe(true)
  })
})
