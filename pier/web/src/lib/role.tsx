import { cloneElement, createContext, isValidElement, useContext, type ReactElement, type ReactNode } from 'react'
import type { MemberRole } from './api'
import { Link } from './router'

// What the signed-in person's role lets them do, so a page can switch off
// what they can't use and say why, instead of letting it fail. pierd checks
// the same on every request; this keeps the page honest about it.

const RoleContext = createContext<MemberRole>('viewer')

export function RoleProvider({ role, children }: { role: MemberRole; children: ReactNode }) {
  return <RoleContext.Provider value={role}>{children}</RoleContext.Provider>
}

export function useRole() {
  const role = useContext(RoleContext)
  return {
    role,
    // Deploying, promoting, rolling back, and changing any project.
    change: role !== 'viewer',
    // Members, the server's own settings and the GitHub App.
    owner: role === 'owner',
  }
}

export const roleName: Record<MemberRole, string> = { owner: 'Owner', member: 'Member', viewer: 'Viewer' }

// The reasons a switched-off control gives, in the words pierd uses.
export const viewerCant = (does: string) => `Viewers can't ${does}. Ask the owner to make you a member.`
export const onlyOwner = (does: string) => `Only the owner can ${does}.`

// Gate shows a control as it is when the role allows it. Otherwise the
// control stays in its place, switched off, and says why on hover or focus.
export function Gate({ allow, reason, children }: { allow: boolean; reason: string; children: ReactElement }) {
  if (allow) return children
  if (!isValidElement<{ className?: string; children?: ReactNode }>(children)) return null
  const off =
    children.type === Link || children.type === 'a' ? (
      <button type="button" className={children.props.className} disabled>
        {children.props.children}
      </button>
    ) : (
      cloneElement(children as ReactElement<Record<string, unknown>>, { disabled: true, onClick: undefined, tabIndex: -1 })
    )
  return (
    <span className="locked" data-tip={reason} tabIndex={0} aria-label={reason}>
      {off}
    </span>
  )
}
