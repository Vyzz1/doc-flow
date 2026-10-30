export type DocumentType = 'invoice' | 'id_document' | 'image'
export type JobStatus = 'PENDING' | 'PROCESSING' | 'DONE' | 'FAILED'
export interface Step {
  step: string
  status: 'SUCCEEDED' | 'FAILED'
  durationMs: number
  error?: string
}
export interface Field {
  name: string
  value: string
  confidence: number
}
export interface DocumentResult {
  provider: string
  confidence: number
  pages: number
  text: string
  fields: Field[] | null
}
export interface Job {
  jobId: string
  status: JobStatus
  docType: DocumentType
  createdAt: string
  updatedAt: string
  failureReason?: string
  steps: Step[] | null
  result?: DocumentResult | null
}
export interface UploadTicket {
  jobId: string
  status: 'PENDING'
  uploadUrl: string
  uploadFields: Record<string, string>
  maxBytes: number
  expiresAt: string
}
export interface DocFlowApi {
  createJob(docType: DocumentType, contentType: string, signal?: AbortSignal): Promise<UploadTicket>
  upload(ticket: UploadTicket, file: File, signal?: AbortSignal): Promise<void>
  getJob(id: string, signal?: AbortSignal): Promise<Job>
}
export interface User {
  id: string
  email: string
}
export type SignInStep = 'DONE' | 'NEW_PASSWORD'
export interface AuthService {
  user: import('vue').Ref<User | undefined>
  restore(): Promise<void>
  signIn(email: string, password: string): Promise<SignInStep>
  confirmPassword(password: string): Promise<SignInStep>
  token(forceRefresh?: boolean): Promise<string>
  signOut(): Promise<void>
}
