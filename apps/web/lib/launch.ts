/**
 * Launch facts shown on donor pages. Keep every launch or Founding date on a page derived
 * from here so the copy cannot drift.
 */
type CalendarDate = { year: number; month: number; day: number };
const MONTHS = [
  "January",
  "February",
  "March",
  "April",
  "May",
  "June",
  "July",
  "August",
  "September",
  "October",
  "November",
  "December",
] as const;
export const LAUNCH_DATE: CalendarDate = { year: 2027, month: 4, day: 22 };
/** The last New York day on which a first gift still earns Founding Donor. */
export const FOUNDING_DEADLINE: CalendarDate = { year: 2026, month: 12, day: 31 };
function longDate({ year, month, day }: CalendarDate): string {
  return `${MONTHS[month - 1]} ${day}, ${year}`;
}
/** "April 22, 2027" */
export const launchDateLabel = (): string => longDate(LAUNCH_DATE);
/** "December 31, 2026" */
export const foundingDeadlineLabel = (): string => longDate(FOUNDING_DEADLINE);
/** "April 2027", for places that name the launch month only. */
export const launchMonthLabel = (): string => `${MONTHS[LAUNCH_DATE.month - 1]} ${LAUNCH_DATE.year}`;
function newYorkDate(now: Date): CalendarDate {
  const parts = new Intl.DateTimeFormat("en-US", {
    timeZone: "America/New_York",
    year: "numeric",
    month: "numeric",
    day: "numeric",
  }).formatToParts(now);
  const part = (type: string) => Number(parts.find((p) => p.type === type)?.value);
  return { year: part("year"), month: part("month"), day: part("day") };
}
/**
 * New York calendar days left in the Founding window, counting today and Dec 31 (1 on the
 * last day). Null once the window has closed.
 *
 * The authoritative rule is Go's recognition.FoundingCutoff
 * (apps/donor-api/internal/recognition/recognition.go): an exclusive cutoff at
 * Jan 1 2027 00:00 America/New_York. This only mirrors it for display; the server decides.
 */
export function foundingDaysLeft(now: Date): number | null {
  const today = newYorkDate(now);
  // Date.UTC here only counts whole calendar days between two civil dates, which has no
  // DST gap; the wall-clock instant is never converted through UTC.
  const days =
    (Date.UTC(FOUNDING_DEADLINE.year, FOUNDING_DEADLINE.month - 1, FOUNDING_DEADLINE.day) -
      Date.UTC(today.year, today.month - 1, today.day)) /
    86_400_000;
  return days < 0 ? null : days + 1;
}
