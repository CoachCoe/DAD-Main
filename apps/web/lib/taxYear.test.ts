import { expect, it } from "vitest";
import { defaultSummaryYear, taxYear } from "./taxYear";

it("uses the New York calendar year rather than UTC", () => {
  expect(taxYear(new Date("2028-01-01T03:00:00Z"))).toBe(2027);
  expect(taxYear(new Date("2028-01-01T05:00:00Z"))).toBe(2028);
});

it("defaults to the previous tax year, never before the first summary year", () => {
  expect(defaultSummaryYear(new Date("2026-10-04T12:00:00Z"))).toBe(2026);
  expect(defaultSummaryYear(new Date("2027-02-01T12:00:00Z"))).toBe(2026);
  expect(defaultSummaryYear(new Date("2029-02-01T12:00:00Z"))).toBe(2028);
  expect(defaultSummaryYear(new Date("2028-01-01T03:00:00Z"))).toBe(2026);
});
