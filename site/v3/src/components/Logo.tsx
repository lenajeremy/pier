export function Logo({ size = 24 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 22 22" fill="none" aria-hidden="true" focusable="false">
      <path d="M5 13A6 6 0 0 1 17 13Z" fill="currentColor" />
      <path d="M2.5 16H19.5" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" />
      <path d="M6 19.2H16" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeOpacity={0.45} />
    </svg>
  )
}
