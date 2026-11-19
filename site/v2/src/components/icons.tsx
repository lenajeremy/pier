import type { SVGProps } from 'react'

// Icons from the design system: 16px grid, 1.2px stroke, round caps and joins.
// They inherit currentColor and are decorative unless given a label.
type IconProps = SVGProps<SVGSVGElement> & { size?: number }

function Icon({ size = 16, children, ...rest }: IconProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.2}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
      {...rest}
    >
      {children}
    </svg>
  )
}

export function LogoMark({ size = 22, ...rest }: SVGProps<SVGSVGElement> & { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 22 22" fill="none" aria-hidden="true" focusable="false" {...rest}>
      <path d="M5 13A6 6 0 0 1 17 13Z" fill="currentColor" />
      <path d="M2.5 16H19.5" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" />
      <path d="M6 19.2H16" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeOpacity={0.45} />
    </svg>
  )
}

export const FrontendIcon = (p: IconProps) => (
  <Icon {...p}>
    <rect x="1.75" y="2.75" width="12.5" height="10.5" rx="1.5" />
    <path d="M1.75 5.75H14.25" />
  </Icon>
)

export const FunctionIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M5.5 2.5C4 2.5 4 3.5 4 5V6.2C4 7.2 3.4 8 2.5 8C3.4 8 4 8.8 4 9.8V11C4 12.5 4 13.5 5.5 13.5M10.5 2.5C12 2.5 12 3.5 12 5V6.2C12 7.2 12.6 8 13.5 8C12.6 8 12 8.8 12 9.8V11C12 12.5 12 13.5 10.5 13.5" />
  </Icon>
)

export const ServiceIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M8 1.75L13.75 5V11L8 14.25L2.25 11V5L8 1.75Z" />
    <path d="M2.25 5L8 8.25L13.75 5M8 8.25V14.25" />
  </Icon>
)

export const CheckIcon = (p: IconProps) => (
  <Icon strokeWidth={1.6} {...p}>
    <path d="M3 8.5L6.5 12L13 4.5" />
  </Icon>
)

export const PlusIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M3.5 8H12.5M8 3.5V12.5" />
  </Icon>
)

export const MinusIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M3.5 8H12.5" />
  </Icon>
)

export const MenuIcon = (p: IconProps) => (
  <Icon strokeWidth={1.4} {...p}>
    <path d="M3 5.8H13M3 10.2H13" />
  </Icon>
)

export const CloseIcon = (p: IconProps) => (
  <Icon strokeWidth={1.4} {...p}>
    <path d="M4 4L12 12M12 4L4 12" />
  </Icon>
)

export const ChevronDownIcon = (p: IconProps) => (
  <Icon strokeWidth={1.4} {...p}>
    <path d="M4.5 6.5L8 10L11.5 6.5" />
  </Icon>
)

export const CopyIcon = (p: IconProps) => (
  <Icon {...p}>
    <rect x="5.25" y="5.25" width="8.5" height="8.5" rx="1.5" />
    <path d="M10.75 5.25V3.75C10.75 2.92 10.08 2.25 9.25 2.25H3.75C2.92 2.25 2.25 2.92 2.25 3.75V9.25C2.25 10.08 2.92 10.75 3.75 10.75H5.25" />
  </Icon>
)

export const ChevronRightIcon = (p: IconProps) => (
  <Icon strokeWidth={1.4} {...p}>
    <path d="M6.5 4.5L10 8L6.5 11.5" />
  </Icon>
)

export const ArrowUpIcon = (p: IconProps) => (
  <Icon strokeWidth={1.4} {...p}>
    <path d="M8 13V3.5M4 7.5L8 3.5L12 7.5" />
  </Icon>
)

export const RollbackIcon = (p: IconProps) => (
  <Icon strokeWidth={1.4} {...p}>
    <path d="M3.5 6.5H10.5C12.16 6.5 13.5 7.84 13.5 9.5C13.5 11.16 12.16 12.5 10.5 12.5H6" />
    <path d="M6 3.5L3 6.5L6 9.5" />
  </Icon>
)

export const LockIcon = (p: IconProps) => (
  <Icon {...p}>
    <rect x="3.25" y="7.25" width="9.5" height="6.5" rx="1.5" />
    <path d="M5.25 7.25V5.25C5.25 3.73 6.48 2.75 8 2.75C9.52 2.75 10.75 3.73 10.75 5.25V7.25" />
  </Icon>
)

export const GlobeIcon = (p: IconProps) => (
  <Icon {...p}>
    <circle cx="8" cy="8" r="5.75" />
    <path d="M2.25 8H13.75M8 2.25C6.25 4 6.25 12 8 13.75M8 2.25C9.75 4 9.75 12 8 13.75" />
  </Icon>
)

export const TerminalIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M3 4.5L6.5 8L3 11.5M8.5 11.5H13" />
  </Icon>
)

export const RefreshIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M13 7.5A5 5 0 0 0 4 4.8M3 8.5A5 5 0 0 0 12 11.2" />
    <path d="M4 2.5V5H6.5M12 13.5V11H9.5" />
  </Icon>
)

export const HeartbeatIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M1.75 8.5H4.5L6 5L8.5 11.5L10 8.5H14.25" />
  </Icon>
)

export const MoonIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M12.5 10A5.5 5.5 0 0 1 6 3.5A5.5 5.5 0 1 0 12.5 10Z" />
  </Icon>
)

export const LayersIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M8 2.5L13.5 5.5L8 8.5L2.5 5.5L8 2.5Z" />
    <path d="M2.5 8.25L8 11.25L13.5 8.25M2.5 10.75L8 13.75L13.5 10.75" />
  </Icon>
)

export const ListIcon = (p: IconProps) => (
  <Icon {...p}>
    <path d="M5.5 4.5H13.5M5.5 8H13.5M5.5 11.5H13.5M2.5 4.5H2.75M2.5 8H2.75M2.5 11.5H2.75" />
  </Icon>
)

export const GithubIcon = ({ size = 16, ...rest }: IconProps) => (
  <svg width={size} height={size} viewBox="0 0 16 16" fill="currentColor" aria-hidden="true" focusable="false" {...rest}>
    <path d="M8 0.5C3.86 0.5 0.5 3.86 0.5 8C0.5 11.31 2.65 14.12 5.63 15.11C6 15.18 6.14 14.95 6.14 14.75V13.48C4.05 13.93 3.61 12.59 3.61 12.59C3.27 11.72 2.78 11.49 2.78 11.49C2.1 11.02 2.83 11.03 2.83 11.03C3.58 11.08 3.98 11.8 3.98 11.8C4.65 12.95 5.74 12.62 6.17 12.42C6.24 11.93 6.43 11.6 6.65 11.42C4.98 11.23 3.23 10.58 3.23 7.7C3.23 6.88 3.52 6.21 4 5.68C3.92 5.49 3.67 4.73 4.07 3.7C4.07 3.7 4.7 3.5 6.13 4.47C6.73 4.3 7.37 4.22 8 4.22C8.64 4.22 9.28 4.3 9.88 4.47C11.31 3.5 11.94 3.7 11.94 3.7C12.35 4.73 12.09 5.49 12.02 5.68C12.5 6.21 12.78 6.88 12.78 7.7C12.78 10.59 11.03 11.23 9.35 11.41C9.62 11.64 9.86 12.1 9.86 12.8V14.75C9.86 14.95 10 15.19 10.38 15.11C13.36 14.11 15.5 11.31 15.5 8C15.5 3.86 12.14 0.5 8 0.5Z" />
  </svg>
)
