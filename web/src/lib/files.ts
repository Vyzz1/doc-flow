import type { DocumentType } from './types'

export const maxUploadBytes = 10 * 1024 * 1024
export const allowedTypes: Record<DocumentType, string[]> = {
  invoice: ['application/pdf', 'image/jpeg', 'image/png', 'image/tiff'],
  id_document: ['image/jpeg', 'image/png'],
  image: ['image/jpeg', 'image/png', 'image/tiff'],
}
const extensions: Record<string, string> = { pdf: 'application/pdf', jpg: 'image/jpeg', jpeg: 'image/jpeg', png: 'image/png', tif: 'image/tiff', tiff: 'image/tiff' }

export function inspectFile(file: File, docType: DocumentType): { contentType?: string; error?: string } {
  if (file.size === 0) return { error: 'This file is empty. Choose a document with content.' }
  if (file.size > maxUploadBytes) return { error: 'This file exceeds the 10 MiB upload limit.' }
  const contentType = file.type.split(';')[0].trim().toLowerCase() || extensions[file.name.split('.').pop()?.toLowerCase() ?? '']
  if (!contentType || !allowedTypes[docType].includes(contentType)) return { error: 'This file type is not supported for the selected document type.' }
  return { contentType }
}
export function validJobId(id: string): boolean { return /^[0-7][0-9A-HJKMNP-TV-Z]{25}$/.test(id) }

export function formatBytes(bytes: number): string {
  return bytes < 1024 * 1024 ? `${(bytes / 1024).toFixed(1)} KB` : `${(bytes / (1024 * 1024)).toFixed(1)} MiB`
}
