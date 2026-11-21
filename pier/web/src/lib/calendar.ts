// Local calendar days can be shorter or longer than 24 hours.
export function localDay(date: Date, offset = 0): Date {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate() + offset)
}

// timeZone is the viewer's time zone, such as Europe/London, or '' if the
// browser won't say. pierd needs it to cut a chart into the days the viewer
// sees: it knows the moments, not where they're read, and with the zone it
// knows the midnights across a clock change too.
export function timeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone ?? ''
  } catch {
    return ''
  }
}

// utcOffset is how many minutes the viewer's clock is ahead of UTC at a
// moment (minus in the west). pierd falls back on it for a zone it doesn't
// know, though a fixed offset can't follow a clock change inside a range.
export function utcOffset(at: number): number {
  return -Math.round(new Date(at).getTimezoneOffset())
}
