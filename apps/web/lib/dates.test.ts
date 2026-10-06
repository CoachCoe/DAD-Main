import { expect, it } from "vitest";
import { formatDateTime, formatDay } from "./dates";

it("keeps a calendar date on its own day in every time zone", () => {
  expect(formatDay("2026-09-30")).toBe("Sep 30, 2026");
  expect(formatDay("2027-01-01")).toBe("Jan 1, 2027");
});

it("states an instant to the minute with its zone, and no seconds", () => {
  const text = formatDateTime("2026-10-15T12:00:30Z");
  expect(text).toMatch(/^Oct 1[45], 2026, \d{1,2}:\d{2} [AP]M \S+$/);
  expect(text).not.toMatch(/:\d{2}:\d{2}/);
});
