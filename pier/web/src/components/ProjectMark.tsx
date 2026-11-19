import { useState } from 'react'
import { Mark } from './Icons'

// ProjectMark is the project's own favicon, from its production deployment,
// or the Pier mark on a plain tile when it has none.
export function ProjectMark({ name, size = 32 }: { name: string; size?: number }) {
  const [failed, setFailed] = useState(false)
  const radius = size >= 36 ? 9 : 8
  if (failed) {
    return (
      <span className="project-mark" style={{ width: size, height: size, borderRadius: radius }}>
        <Mark size={Math.round(size * 0.56)} />
      </span>
    )
  }
  return (
    <img
      src={`/api/projects/${encodeURIComponent(name)}/favicon`}
      alt=""
      width={size}
      height={size}
      style={{ borderRadius: radius, objectFit: 'cover', flex: 'none', background: 'rgba(255,255,255,0.07)' }}
      onError={() => setFailed(true)}
    />
  )
}
