import { useData } from './data'

// Links into the Grafana pierd runs, when traces, metrics and app logs are
// on: its Explore, opened on the same query and time.

type Sources = { on: boolean; path: string; traces: string; logs: string; metrics: string }

export function useGrafana(project: string) {
  const g = useData<Sources>(`/api/projects/${encodeURIComponent(project)}/grafana`)
  return g.data?.on ? g.data : null
}

export function explore(g: Sources, source: 'traces' | 'logs' | 'metrics', query: Record<string, unknown>, from: number, to: number) {
  const uid = g[source]
  const panes = { a: { datasource: uid, queries: [{ refId: 'A', datasource: { uid }, ...query }], range: { from: String(Math.floor(from)), to: String(Math.ceil(to)) } } }
  return `${g.path}explore?schemaVersion=1&orgId=1&panes=${encodeURIComponent(JSON.stringify(panes))}`
}
