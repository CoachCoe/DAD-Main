import { describe, expect, it } from "vitest";
import { foundingDaysLeft, foundingDeadlineLabel, launchDateLabel, launchMonthLabel } from "./launch";

describe("launch labels", () => {
  it("uses one long-month US format", () => {
    expect(launchDateLabel()).toBe("April 22, 2027");
    expect(foundingDeadlineLabel()).toBe("December 31, 2026");
    expect(launchMonthLabel()).toBe("April 2027");
  });
});
describe("foundingDaysLeft", () => {
  it("is 1 on the last evening in New York", () => {
    expect(foundingDaysLeft(new Date("2027-01-01T04:59:59Z"))).toBe(1); // Dec 31 23:59:59 EST
  });
  it("is null from Jan 1 00:00 New York", () => {
    expect(foundingDaysLeft(new Date("2027-01-01T05:00:00Z"))).toBeNull();
    expect(foundingDaysLeft(new Date("2027-06-01T12:00:00Z"))).toBeNull();
  });
  it("follows the New York date, not UTC, late on Dec 30", () => {
    // 02:00 UTC Dec 31 is still Dec 30 in New York.
    expect(foundingDaysLeft(new Date("2026-12-31T02:00:00Z"))).toBe(2);
  });
  it("counts a mid-November day inclusively", () => {
    expect(foundingDaysLeft(new Date("2026-11-15T17:00:00Z"))).toBe(47);
  });
  it("counts whole calendar days across the November DST change", () => {
    expect(foundingDaysLeft(new Date("2026-11-01T03:30:00Z"))).toBe(62); // Oct 31 23:30 EDT
    expect(foundingDaysLeft(new Date("2026-11-01T06:30:00Z"))).toBe(61); // Nov 1 01:30 EST
    expect(foundingDaysLeft(new Date("2026-11-02T05:00:00Z"))).toBe(60); // Nov 2 00:00 EST
  });
});
