// Local calendar days can be shorter or longer than 24 hours.
export function localDay(date: Date, offset = 0): Date {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate() + offset)
}

// utcOffset is how many minutes the viewer's clock is ahead of UTC at a
// moment (minus in the west). pierd needs it to cut a chart into the days
// the viewer sees: it knows the moments, not where they're read.
export function utcOffset(at: number): number {
  return -Math.round(new Date(at).getTimezoneOffset())
}
