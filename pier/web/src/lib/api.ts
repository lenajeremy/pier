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
  sent_by?: string // who uploaded it or asked for the rebuild; empty for a push
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

// Health is how production's server did at its health checks, which pierd
// runs every 30 seconds while production serves it.
export interface Health {
  deployment: string
  state: 'healthy' | 'failing' | 'unhealthy' | 'restarting'
  asked: string // "GET /health", or "the process"
  failures: number // in a row
  checked_at?: string
  since?: string // when the state began
  last_failure?: string
  last_failure_at?: string
  restarts: number
  restarted_at?: string
  next_restart?: string
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
  health?: Health | null
  preview: Address | null
  branches: Address[]
  deployments: Deployment[]
  stats: BuildStats
  // For a project GitHub deploys: its repo, the deliveries that haven't
  // started building yet (an import's first build among them), and the ones
  // waiting for the owner's approval.
  github?: { repo: string; queued: number; approval: number } | null
}

export interface LogLine {
  ms: number
  tag: string
  text: string
}

// MemberRole is what someone may do across the whole server: the owner
// everything, a member deploy and change projects, a viewer only look.
export type MemberRole = 'owner' | 'member' | 'viewer'

export interface Session {
  user: { id: number; name: string; email: string; role: MemberRole } | null
  server: { address: string; base_domain: string; domain: string; production_branch: string; version?: string }
}

export interface Member {
  id: number
  name: string
  email: string
  role: MemberRole
  created_at: string
  last_active_at: string
  you: boolean
}

export interface Invite {
  id: number
  role: 'member' | 'viewer'
  created_by: string
  created_at: string
  expires_at: string
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
    // A refusal that names the role it needs: the role changed since the
    // page loaded.
    if (resp.status === 403 && typeof body.need === 'string') window.dispatchEvent(new Event('pier:role-changed'))
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
  health_off: boolean // check only that the process runs
  memory_mb: number
  cpus: number
}

export interface Settings {
  project: { name: string; production_branch: string; repo: string; build: Build; runtime: RuntimeSettings }
  default_branch: string
  default_memory_mb?: number
  last_build_plan: Record<string, string> | null
}

// A scheduled job: a command run in a fresh container from production's
// image, or a request to a path on production.
export interface Job {
  id: number
  project: string
  name: string
  kind: 'command' | 'path'
  schedule: string
  timezone: string
  command?: string
  method?: 'GET' | 'POST'
  path?: string
  timeout_s: number
  enabled: boolean
  created_at: string
  updated_at: string
  description: string // the schedule in words
  next_run: string | null
  last_run: JobRun | null
  running: boolean
}

export interface JobRun {
  id: number
  job: number
  project: string
  started_by: 'schedule' | 'manual'
  run_by?: string
  status: 'running' | 'succeeded' | 'failed' | 'skipped'
  deployment?: string
  scheduled_for?: string
  started_at: string
  finished_at?: string
  exit_code?: number
  http_status?: number
  detail?: string
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

// A project's Postgres server, its databases, its external URLs and backups.
export type DatabaseBackup = {
  id: number
  project: string
  database: string
  reason: 'nightly' | 'manual' | 'before-restore' | 'before-delete'
  made_by?: string
  state: 'running' | 'done' | 'failed'
  bytes: number
  error?: string
  started_at: string
  finished_at?: string
  offsite?: string
}

export type ProjectDatabase = {
  id: number
  name: string
  environments: Environment[]
  variable: string
  created_at: string
  created_by?: string
  bytes: number
}
export type ExternalURL = { role: string; project: string; database: string; created_at: string; created_by?: string; host: string }

export type DatabaseStatus = {
  project: string
  image: string
  memory_mb: number
  created_at: string
  created_by?: string
  version: string
  container: {
    state: string
    health?: string
    started_at?: string
    restarts?: number
    oom_killed?: boolean
    memory_bytes?: number
    limit_bytes?: number
  }
  data_bytes: number
  disk_bytes: number
  connections: number
  max_connections: number
  databases: ProjectDatabase[]
  problem?: string
  host: string
  port: string
}

export type DatabaseView = {
  database: DatabaseStatus | null
  backups: DatabaseBackup[]
  memory_choices: number[]
  default_memory_mb: number
  image: string
  production: { deployment: string; connected: boolean } | null
  externals: ExternalURL[]
  external: string
  nightly_at: number
  timezone: string
}

// A database's tables, a page of one's rows, and a query's results.
export type DbTable = { schema: string; name: string; kind: string; rows: number; more: boolean; bytes: number }
export type DbColumn = { name: string; type: string }
export type DbPage = {
  table: DbTable
  columns: DbColumn[]
  rows: (string | null)[][]
  offset: number
  limit: number
  sort?: string
  desc?: boolean
  where?: string
  key?: string[]
  refs: Record<string, { schema: string; table: string; column: string }>
  count: number
  more: boolean
  ms: number
}
export type DbResult = { columns: DbColumn[] | null; rows: (string | null)[][]; truncated?: boolean; tag: string }
export type DbQueryError = { message: string; detail?: string; hint?: string; position?: number }
export type DbOutcome = { results: DbResult[]; error?: DbQueryError; ms: number }

export type Environment = 'production' | 'preview'
export type Bucket = {
  id: number
  project: string
  name: string
  bucket: string
  public: boolean
  public_url: string
  environments: Environment[]
  prefix: string
  created_at: string
  created_by?: string
  files: number
  bytes: number
  more: boolean
  count_error?: string
  variables: string[]
  address_ready: boolean
}
export type CloudflareView = { connected: boolean; account?: string; domain?: string; prefix?: string; buckets?: Bucket[] }
export type StorageView = { cloudflare: CloudflareView; buckets: Bucket[]; count_up_to: number }
export type StoredFile = { key: string; size: number; modified: string; etag?: string; type: string; view: string; download: string; url?: string }
export type FileListing = { folders: string[]; files: StoredFile[]; next?: string }
export type BucketVariable = { name: string; value: string; secret?: boolean }

// Analytics: the Overview, read from the request log.
export interface AnalyticsCount {
  name: string
  count: number
}
export interface AnalyticsList {
  top: AnalyticsCount[]
  rest: number // counted outside the top
  others: number // how many names the rest spans
}
export interface AnalyticsHeadline {
  requests: number
  errors: number
  visitors: number
  page_views: number
  p95_ms: number
}
export interface AnalyticsPoint {
  at: number
  requests: number
  errors: number
  visitors: number
  page_views: number
  p95_ms: number | null
}
export interface AnalyticsOverview extends AnalyticsHeadline {
  from: number
  to: number
  step: number
  before: AnalyticsHeadline
  trend: AnalyticsPoint[]
  pages: AnalyticsList
  referrers: AnalyticsList
  countries: AnalyticsList
  browsers: AnalyticsList
  devices: AnalyticsList
  bots: AnalyticsList
  slowest: { route: string; requests: number; errors: number; p95_ms: number }[]
}

// Events an app sends.
export interface EventRow {
  name: string
  count: number
  visitors: number
  value: number | null
  trend: number[]
}
export interface EventDetail {
  name: string
  by: string
  props: string[]
  step: number
  steps: number[]
  series: { key: string; counts: number[]; values: number[] }[]
  breakdown: { key: string; count: number; value: number | null }[]
  count: number
  value: number | null
}
export interface Funnel {
  steps: string[]
  counts: number[]
  names: string[]
}

// Traces, from Tempo through pierd.
export interface TraceRow {
  id: string
  service: string // the root span's
  name: string
  start: number // ms since the epoch
  ms: number
  spans: number
  errors: number
  services: Record<string, { spans: number; errors?: number }>
  status?: number // the request's, when Pier's router started the trace
  deployment?: string
  partial?: boolean // the root span hasn't arrived yet
}

export interface TraceList {
  on: boolean
  traces: TraceRow[]
  from: number
  to: number
  limit: number
}

export interface TraceSpan {
  id: string
  parent?: string
  service: string
  name: string
  kind?: string
  start: number // ms after the trace began
  ms: number
  error?: boolean
  message?: string
  attrs?: [string, string][]
  resource?: [string, string][]
  events?: { name: string; at: number; attrs?: [string, string][] }[]
}

export interface TraceLog {
  at: number // ms since the epoch
  level?: string
  service?: string
  span?: string
  stream?: string
  text: string
}

export interface TraceDoc {
  trace: { id: string; start: number; ms: number; spans: TraceSpan[] }
  request: { method: string; path: string; status: number; deployment: string; host?: string } | null
  logs: TraceLog[]
}
