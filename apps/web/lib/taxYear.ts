/** Annual summaries start with DAD's first year of giving; the API rejects earlier years. */
export const FIRST_SUMMARY_YEAR = 2026;

/** Receipt tax years follow America/New_York (docs/analytics.md). */
export function taxYear(now: Date): number {
  return Number(new Intl.DateTimeFormat("en-US", { timeZone: "America/New_York", year: "numeric" }).format(now));
}

/** The prior-year summary is the one donors need, but never a year before the first. */
export function defaultSummaryYear(now: Date): number {
  return Math.max(FIRST_SUMMARY_YEAR, taxYear(now) - 1);
}
