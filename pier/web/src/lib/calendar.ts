// Local calendar days can be shorter or longer than 24 hours.
export function localDay(date: Date, offset = 0): Date {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate() + offset)
}
