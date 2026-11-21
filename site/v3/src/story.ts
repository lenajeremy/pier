// The story sheet: every name, ID and number on the page comes from here, so
// the board, the push, the rollback and the dashboard tell one story.

export const INK = {
  pink: 'var(--pink)', // Caddy: production
  violet: 'var(--violet)', // git: building, preview
  teal: 'var(--teal)', // Docker: healthy, awake
  blue: 'var(--blue)', // pierd
  asleep: 'var(--asleep)',
}

export type BoardRow = { address: string; sub: string; deployment: string; branch: string; status: string; ink: string }

export const board: BoardRow[] = [
  { address: 'blog.pier.example.com', sub: 'blog', deployment: '8f3a2c', branch: 'main', status: 'live', ink: INK.pink },
  { address: 'feat-search--blog', sub: 'feat-search', deployment: 'c19e77', branch: 'feat-search', status: 'building', ink: INK.violet },
  { address: 'docs.pier.example.com', sub: 'docs', deployment: '2b71e0', branch: 'main', status: 'live', ink: INK.pink },
  { address: 'status.pier.example.com', sub: 'status', deployment: '3d9c04', branch: 'main', status: 'healthy', ink: INK.teal },
  { address: 'shop.pier.example.com', sub: 'shop', deployment: '7b44d2', branch: 'main', status: 'rollback', ink: INK.pink },
  { address: 'feat-cart--shop', sub: 'feat-cart', deployment: '9a0f31', branch: 'feat-cart', status: 'asleep', ink: INK.asleep },
  { address: 'fix-rss--blog', sub: 'fix-rss', deployment: '41d0e9', branch: 'fix-rss', status: 'preview', ink: INK.violet },
]

/** Column widths, in tiles. */
export const COLS = { address: 23, deployment: 6, branch: 11, status: 8 }

export const pushLog: { at: string; tag?: [string, string]; text: string; strong?: boolean }[] = [
  { at: '0.00', text: '$ git push pier feat-search', strong: true },
  { at: '0.18', text: 'Writing objects: 100% (24/24), 8.31 KiB, done.' },
  { at: '0.21', tag: ['pier', INK.violet], text: 'received feat-search at c19e77' },
  { at: '0.40', tag: ['build', INK.teal], text: 'astro 4.16, found in package.json' },
  { at: '6.51', tag: ['build', INK.teal], text: 'npm ci, 312 packages in 6.1s' },
  { at: '8.27', tag: ['build', INK.teal], text: 'astro build, 214 files, 1.3 MB' },
  { at: '9.71', tag: ['build', INK.teal], text: 'go build api/og.go, 1.4s' },
  { at: '10.6', tag: ['deploy', INK.blue], text: 'kept as c19e77, never changed again' },
  { at: '11.0', tag: ['alias', INK.pink], text: 'feat-search--blog.pier.example.com' },
  { at: '11.2', tag: ['pier', INK.violet], text: 'preview ready in 11.2s', strong: true },
]

export const steps = [
  { title: 'Push', text: 'git push pier feat-search', time: '0.2s', until: 0.21 },
  { title: 'Build', text: 'astro build in a throwaway Docker container', time: '9.5s', until: 9.71 },
  { title: 'Deploy', text: 'kept as c19e77, never changed again', time: '0.9s', until: 10.6 },
  { title: 'Go live', text: 'feat-search--blog gets its address', time: '0.6s', until: 11.2 },
]
