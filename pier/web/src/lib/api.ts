// The dashboard's view of pierd's API. Every request carries X-Pier, which
// pierd requires for anything sent with the session cookie that changes
// something; another site's page can't add it.

export type Status = 'building' | 'live' | 'failed' | 'none'
export type Role = 'production' | 'preview' | 'building' | 'failed' | 'kept'

export interface Deployment {
  id: string
  project: string
  branch: string
  commit: string
  message: string
  status: 'building' | 'ready' | 'failed'
  framework: string
  runtime?: string
  files: number
  bytes: number
  build_ms: number
  error?: string
  created_at: string
  role: Role
  host: string
  url: string
  target: 'production' | 'preview'
  source: 'git' | 'cli' | 'github'
  plan?: string
}

export interface Address {
  host: string
  url: string
  kind: 'production' | 'branch' | 'deployment'
  branch?: string
  updated_at: string
  deployment?: Deployment
}

export interface ProjectSummary {
  name: string
  address: string
  url: string
  status: Status
  deployments: number
  latest: Deployment | null
  production: Deployment | null
}

export interface BuildStats {
  builds: { id: string; ms: number; status: string }[]
  median_ms: number
  failed: number
  kept: number
  bytes: number
}

export interface ContainerStatus {
  state: 'running' | 'restarting' | 'exited' | 'oom_killed' | 'dead' | 'paused' | 'not_found' | 'unknown' | string
  health?: string
  exit_code?: number
  oom_killed?: boolean
  restarts?: number
  started_at?: string
  memory_limit_mb?: number
  cpu_limit?: number
}

export interface Project {
  name: string
  address: string
  url: string
  production_branch: string
  rolled_back_from: string
  rollback_to: string
  status: Status
  production: Address | null
  container?: ContainerStatus | null
  preview: Address | null
  branches: Address[]
  deployments: Deployment[]
  stats: BuildStats
}

export interface LogLine {
  ms: number
  tag: string
  text: string
}

export interface Session {
  user: { name: string; email: string } | null
  server: { address: string; base_domain: string; domain: string; production_branch: string }
}

export class ApiError extends Error {
  status: number
  body: Record<string, unknown>
  constructor(status: number, body: Record<string, unknown>) {
    super(typeof body.error === 'string' ? body.error : `pierd answered ${status}`)
    this.status = status
    this.body = body
  }
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  headers.set('X-Pier', '1')
  if (init.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
  const resp = await fetch(path, { ...init, headers, credentials: 'same-origin' })
  if (resp.status === 204) return undefined as T
  let body: Record<string, unknown> = {}
  try {
    body = await resp.json()
  } catch {
    // not JSON: keep the empty body and report the status
  }
  if (!resp.ok) {
    if (resp.status === 401 && path !== '/api/session') window.dispatchEvent(new Event('pier:signed-out'))
    throw new ApiError(resp.status, body)
  }
  return body as T
}

export const signIn = (email: string, password: string) =>
  api<Session>('/api/session', { method: 'POST', body: JSON.stringify({ email, password }) })

export const signOut = () => api<void>('/api/session', { method: 'DELETE' })

export interface Build {
  root: string
  preset: '' | 'static' | 'vite' | 'server' | 'dockerfile'
  install: string
  command: string
  output: string
  always_build?: boolean // build on every push from GitHub, even one that doesn't change the root directory
}

export function changePreset(build: Build, preset: Build['preset']): Build {
  if (preset === 'server' || preset === 'dockerfile') {
    return { ...build, preset, install: '', output: '', command: preset === 'dockerfile' ? '' : build.command }
  }
  return { ...build, preset }
}

export interface RuntimeSettings {
  start_command: string
  health_path: string
  memory_mb: number
  cpus: number
}

export interface Settings {
  project: { name: string; production_branch: string; repo: string; build: Build; runtime: RuntimeSettings }
  default_branch: string
  default_memory_mb?: number
  last_build_plan: Record<string, string> | null
}

export interface SessionRow {
  id: string
  kind: 'browser' | 'cli'
  name: string
  ip: string
  created_at: string
  last_seen_at: string
  current: boolean
}

export interface LoginRequest {
  code: string
  name: string
  ip: string
  created_at: string
  expires_at: string
  status: 'pending' | 'approved' | 'denied'
}
