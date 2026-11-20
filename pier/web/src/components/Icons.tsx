// Icons, copied path for path from the design system on the board.

type P = { size?: number; color?: string }

const line = (size: number, color: string, width = 1.5) => ({
  viewBox: '0 0 24 24',
  fill: 'none',
  stroke: color,
  strokeWidth: width,
  strokeLinecap: 'round' as const,
  strokeLinejoin: 'round' as const,
  width: size,
  height: size,
  'aria-hidden': true,
})

export const Mark = ({ size = 18 }: P) => (
  <svg viewBox="0 0 22 22" fill="none" width={size} height={size} aria-hidden>
    <path d="M5 13A6 6 0 0 1 17 13Z" fill="#ECEEF4" />
    <path d="M2.5 16H19.5" stroke="#ECEEF4" strokeWidth="1.8" strokeLinecap="round" />
    <path d="M6 19.2H16" stroke="#ECEEF4" strokeWidth="1.8" strokeLinecap="round" strokeOpacity="0.45" />
  </svg>
)

export const UpDown = ({ size = 14, color = '#8A92A8' }: P) => (
  <svg {...line(size, color, 1.6)}>
    <path d="M8 10L12 6L16 10M8 14L12 18L16 14" />
  </svg>
)

export const Search = ({ size = 15, color = '#8A92A8' }: P) => (
  <svg {...line(size, color)}>
    <circle cx="11" cy="11" r="6.5" />
    <path d="M16 16L20.5 20.5" />
  </svg>
)

export const Projects = ({ size = 17, color = 'currentColor' }: P) => (
  <svg {...line(size, color)}>
    <rect x="4" y="4" width="6.5" height="6.5" rx="1.5" />
    <rect x="13.5" y="4" width="6.5" height="6.5" rx="1.5" />
    <rect x="4" y="13.5" width="6.5" height="6.5" rx="1.5" />
    <rect x="13.5" y="13.5" width="6.5" height="6.5" rx="1.5" />
  </svg>
)

export const Activity = ({ size = 17, color = 'currentColor' }: P) => (
  <svg {...line(size, color)}>
    <path d="M3.5 12.5H8L10 8L14 17L16 12.5H20.5" />
  </svg>
)

export const Integrations = ({ size = 17, color = 'currentColor' }: P) => (
  <svg {...line(size, color)}>
    <path d="M9 4.5V8.5M15 4.5V8.5M6.5 8.5H17.5V11.5A5.5 5.5 0 0 1 6.5 11.5ZM12 17V20" />
  </svg>
)

export const Braces = ({ size = 17, color = 'currentColor' }: P) => (
  <svg {...line(size, color)}>
    <path d="M8.5 5.5C6.5 5.5 6.5 7 6.5 8.5S6 11.5 4.5 12C6 12.5 6.5 14 6.5 15.5S6.5 18.5 8.5 18.5M15.5 5.5C17.5 5.5 17.5 7 17.5 8.5S18 11.5 19.5 12C18 12.5 17.5 14 17.5 15.5S17.5 18.5 15.5 18.5" />
  </svg>
)

export const Members = ({ size = 17, color = 'currentColor' }: P) => (
  <svg {...line(size, color)}>
    <circle cx="9" cy="8.5" r="3" />
    <path d="M3.5 19A5.5 5.5 0 0 1 14.5 19M15.5 5.8A3 3 0 0 1 15.5 11.2M17.5 14.5A5.5 5.5 0 0 1 20.5 19" />
  </svg>
)

export const Server = ({ size = 17, color = 'currentColor' }: P) => (
  <svg {...line(size, color)}>
    <rect x="4" y="4.5" width="16" height="6" rx="1.5" />
    <rect x="4" y="13.5" width="16" height="6" rx="1.5" />
    <path d="M7.5 7.5H8M7.5 16.5H8" />
  </svg>
)

export const Settings = ({ size = 17, color = 'currentColor' }: P) => (
  <svg {...line(size, color)}>
    <path d="M4.5 8H7M11 8H19.5M4.5 16H13M17 16H19.5" />
    <circle cx="9" cy="8" r="2" />
    <circle cx="15" cy="16" r="2" />
  </svg>
)

export const Docs = ({ size = 17, color = 'currentColor' }: P) => (
  <svg {...line(size, color)}>
    <path d="M5 5.5A1.5 1.5 0 0 1 6.5 4H18V17H6.5A1.5 1.5 0 0 0 5 18.5ZM5 18.5A1.5 1.5 0 0 0 6.5 20H18V17" />
  </svg>
)

export const Plus = ({ size = 13, color = '#A3ABBE' }: P) => (
  <svg {...line(size, color, 2)}>
    <path d="M12 5V19M5 12H19" />
  </svg>
)

export const Branch = ({ size = 14, color = '#A3ABBE' }: P) => (
  <svg {...line(size, color, 1.6)}>
    <circle cx="7" cy="5.5" r="2" />
    <circle cx="7" cy="18.5" r="2" />
    <circle cx="17" cy="7.5" r="2" />
    <path d="M7 7.5V16.5M17 9.5V10.5A4 4 0 0 1 13 14.5H9A2 2 0 0 0 7 16.5" />
  </svg>
)

export const Commit = ({ size = 14, color = '#A3ABBE' }: P) => (
  <svg {...line(size, color, 1.6)}>
    <circle cx="12" cy="12" r="3.2" />
    <path d="M3 12H8.8M15.2 12H21" />
  </svg>
)

export const Visit = ({ size = 16, color = '#ECEEF4' }: P) => (
  <svg {...line(size, color)}>
    <path d="M7 17L17 7M9 7H17V15" />
  </svg>
)

export const Redeploy = ({ size = 16, color = '#ECEEF4' }: P) => (
  <svg {...line(size, color)}>
    <path d="M19 12A7 7 0 1 1 16.95 7.05M19 4.5V8.5H15" />
  </svg>
)

export const ChevronRight = ({ size = 14, color = '#5E667B' }: P) => (
  <svg {...line(size, color)}>
    <path d="M9.5 6L15.5 12L9.5 18" />
  </svg>
)

export const ChevronLeft = ({ size = 16, color = '#ECEEF4' }: P) => (
  <svg {...line(size, color)}>
    <path d="M14.5 6L8.5 12L14.5 18" />
  </svg>
)

export const Disclosure = ({ size = 14, color = '#A3ABBE' }: P) => (
  <svg {...line(size, color, 1.8)} className="chevron">
    <path d="M10 7L15 12L10 17" />
  </svg>
)

export const SortDown = ({ size = 12, color = '#8A92A8' }: P) => (
  <svg {...line(size, color, 2)}>
    <path d="M6 9.5L12 15.5L18 9.5" />
  </svg>
)

export const Check = ({ size = 16, color = '#62D8C6' }: P) => (
  <svg {...line(size, color, 1.8)}>
    <path d="M5 12.5L10 17.5L19 7" />
  </svg>
)

export const Done = ({ size = 16 }: P) => (
  <svg {...line(size, '#62D8C6', 1.6)}>
    <circle cx="12" cy="12" r="8" />
    <path d="M8.5 12.2L11 14.7L15.8 9.6" />
  </svg>
)

export const FailedMark = ({ size = 16 }: P) => (
  <svg {...line(size, '#FF8080', 1.6)}>
    <circle cx="12" cy="12" r="8" />
    <path d="M9.2 9.2L14.8 14.8M14.8 9.2L9.2 14.8" />
  </svg>
)

export const Working = ({ size = 16 }: P) => (
  <svg {...line(size, '#BCAEF8', 1.6)} className="spin">
    <path d="M12 4A8 8 0 1 1 4 12" />
  </svg>
)

export const More = ({ size = 16 }: P) => (
  <svg viewBox="0 0 24 24" fill="#ECEEF4" width={size} height={size} aria-hidden>
    <circle cx="6" cy="12" r="1.4" />
    <circle cx="12" cy="12" r="1.4" />
    <circle cx="18" cy="12" r="1.4" />
  </svg>
)

export const Info = ({ size = 20, color = '#8DB2F8' }: P) => (
  <svg {...line(size, color)}>
    <circle cx="12" cy="12" r="8" />
    <path d="M12 11V16M12 8V8.01" />
  </svg>
)

export const Copy = ({ size = 16, color = '#A3ABBE' }: P) => (
  <svg {...line(size, color)}>
    <rect x="9" y="9" width="11" height="11" rx="2" />
    <path d="M15 9V6.5A1.5 1.5 0 0 0 13.5 5H6.5A1.5 1.5 0 0 0 5 6.5V13.5A1.5 1.5 0 0 0 6.5 15H9" />
  </svg>
)

export const Warning = ({ size = 18, color = '#FFB4B4' }: P) => (
  <svg {...line(size, color)}>
    <path d="M12 4.5L20.5 19H3.5Z" />
    <path d="M12 10V13.5M12 16.3H12.01" />
  </svg>
)

export const Trash = ({ size = 16, color = '#FFB4B4' }: P) => (
  <svg {...line(size, color)}>
    <path d="M4.5 7H19.5M10 4.5H14M7 7L7.8 18.2A1.5 1.5 0 0 0 9.3 19.5H14.7A1.5 1.5 0 0 0 16.2 18.2L17 7" />
  </svg>
)

export const ArrowUp = ({ size = 16, color = '#A3ABBE' }: P) => (
  <svg {...line(size, color)}>
    <path d="M12 19V5M6 11L12 5L18 11" />
  </svg>
)

export const Rows = ({ size = 16, color = '#A3ABBE' }: P) => (
  <svg {...line(size, color)}>
    <path d="M9 7H19.5M9 12H19.5M9 17H19.5M4.5 7H5M4.5 12H5M4.5 17H5" />
  </svg>
)

export const Layers = ({ size = 16, color = '#A3ABBE' }: P) => (
  <svg {...line(size, color)}>
    <path d="M12 4L20 8L12 12L4 8Z" />
    <path d="M4 12L12 16L20 12M4 16L12 20L20 16" />
  </svg>
)

export const RollBack = ({ size = 16, color = '#ECEEF4' }: P) => (
  <svg {...line(size, color)}>
    <path d="M5 12A7 7 0 1 0 7.05 7.05M5 4.5V8.5H9" />
  </svg>
)

export const Terminal = ({ size = 14, color = '#A3ABBE' }: P) => (
  <svg {...line(size, color, 1.6)}>
    <rect x="3.5" y="5" width="17" height="14" rx="2.5" />
    <path d="M7.5 10L10.5 12.5L7.5 15M13 15H16.5" />
  </svg>
)

export const Close = ({ size = 16, color = '#A3ABBE' }: P) => (
  <svg {...line(size, color)}>
    <path d="M6.5 6.5L17.5 17.5M17.5 6.5L6.5 17.5" />
  </svg>
)

export const Eye = ({ size = 16, color = '#8A92A8' }: P) => (
  <svg {...line(size, color, 1.6)}>
    <path d="M2.5 12C4.5 7.5 8 5.5 12 5.5S19.5 7.5 21.5 12C19.5 16.5 16 18.5 12 18.5S4.5 16.5 2.5 12Z" />
    <circle cx="12" cy="12" r="3" />
  </svg>
)

export const Pencil = ({ size = 16, color = '#A3ABBE' }: P) => (
  <svg {...line(size, color)}>
    <path d="M5 19L5.8 15.4L15.9 5.3A1.6 1.6 0 0 1 18.2 5.3L18.7 5.8A1.6 1.6 0 0 1 18.7 8.1L8.6 18.2Z" />
    <path d="M14.2 7L17 9.8" />
  </svg>
)

export const Calendar = ({ size = 14, color = 'currentColor' }: P) => (
  <svg {...line(size, color, 1.6)}>
    <rect x="4" y="5.5" width="16" height="14" rx="2" />
    <path d="M4 10H20M8.5 3.5V7M15.5 3.5V7" />
  </svg>
)

export const GitHubMark = ({ size = 20 }: { size?: number }) => (
  <svg viewBox="0 0 24 24" fill="#ECEEF4" width={size} height={size} aria-hidden>
    <path d="M12 .297c-6.63 0-12 5.373-12 12 0 5.303 3.438 9.8 8.205 11.385.6.113.82-.258.82-.577 0-.285-.01-1.04-.015-2.04-3.338.724-4.042-1.61-4.042-1.61C4.422 18.07 3.633 17.7 3.633 17.7c-1.087-.744.084-.729.084-.729 1.205.084 1.838 1.236 1.838 1.236 1.07 1.835 2.809 1.305 3.495.998.108-.776.417-1.305.76-1.605-2.665-.3-5.466-1.332-5.466-5.93 0-1.31.465-2.38 1.235-3.22-.135-.303-.54-1.523.105-3.176 0 0 1.005-.322 3.3 1.23.96-.267 1.98-.399 3-.405 1.02.006 2.04.138 3 .405 2.28-1.552 3.285-1.23 3.285-1.23.645 1.653.24 2.873.12 3.176.765.84 1.23 1.91 1.23 3.22 0 4.61-2.805 5.625-5.475 5.92.42.36.81 1.096.81 2.22 0 1.606-.015 2.896-.015 3.286 0 .315.21.69.825.57C20.565 22.092 24 17.592 24 12.297c0-6.627-5.373-12-12-12" />
  </svg>
)
