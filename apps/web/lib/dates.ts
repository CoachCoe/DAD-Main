const calendarDay = new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric", year: "numeric", timeZone: "UTC" });

/** A calendar date with no time of day ("2026-09-30"); it must not shift with the reader's zone. */
export function formatDay(isoDate: string): string {
  return calendarDay.format(new Date(`${isoDate.slice(0, 10)}T00:00:00Z`));
}

/** The reader's local date of an instant, e.g. "Oct 1, 2026". */
export function formatDate(instant: string): string {
  return new Date(instant).toLocaleDateString("en-US", { month: "short", day: "numeric", year: "numeric" });
}

/** An instant with its time and zone, so a deadline is unambiguous, e.g. "Oct 15, 2026, 8:00 AM EDT". */
export function formatDateTime(instant: string): string {
  return new Date(instant).toLocaleString("en-US", {
    month: "short", day: "numeric", year: "numeric", hour: "numeric", minute: "2-digit", timeZoneName: "short",
  });
}
