import { expect, it } from "vitest";
import { dailyGiftMinor, formatDollars, formatMoney, isGiftAmount, monthlyFraming, parseDollars } from "./money";

it("formats integer cents without floating-point drift", () => {
  expect(formatMoney(12345)).toBe("$123.45");
  expect(formatMoney(1)).toBe("$0.01");
  expect(formatMoney(3000)).toBe("$30.00");
  expect(formatMoney(2000, "EUR")).toBe("€20.00");
});

it("drops cents only from whole-dollar amounts", () => {
  expect(formatDollars(3000)).toBe("$30");
  expect(formatDollars(4550)).toBe("$45.50");
  expect(formatDollars(4505)).toBe("$45.05");
});

it("pairs the monthly charge with its exact daily amount", () => {
  expect(monthlyFraming(3000)).toEqual({ monthly: "$30/month", daily: "$1 a day" });
  expect(monthlyFraming(90000)).toEqual({ monthly: "$900/month", daily: "$30 a day" });
});

it("marks the daily figure approximate for an amount set before the daily rule", () => {
  expect(monthlyFraming(4500)).toEqual({ monthly: "$45/month", daily: "about $1.50 a day" });
  expect(monthlyFraming(3001).daily).toBe("about $1 a day");
});

it.each([
  ["1", 3000],
  ["$3", 9000],
  [" 30 ", 90000],
])("charges a %s-a-day gift as the daily amount × 30", (input, minor) => {
  expect(dailyGiftMinor(input)).toBe(minor);
  expect(isGiftAmount(minor)).toBe(true);
});

it.each(["0", "31", "1.50", "-1", "", "100"])("refuses %s dollars a day", (input) => {
  expect(dailyGiftMinor(input)).toBeNull();
});

it("accepts only whole-dollar daily gifts from $1 to $30 a day", () => {
  for (const minor of [2999, 3001, 4500, 93000, 0]) expect(isGiftAmount(minor)).toBe(false);
});

it.each([
  ["30", 3000],
  ["30.0", 3000],
  ["30.01", 3001],
  ["90.25", 9025],
  ["0.5", 50],
  [" 12 ", 1200],
  ["1234567.89", 123456789],
  ["$30", 3000],
  ["30.", 3000],
  ["1,000", 100000],
  ["$1,234,567.89", 123456789],
])("parses %s as exact cents", (input, minor) => {
  expect(parseDollars(input)).toBe(minor);
});

it.each([
  "",
  "$",
  "1.001",
  "-1",
  "$-1",
  "1e3",
  ".5",
  "30..",
  "1,00",
  "10,0000",
  "1,000,00",
  "$ 30",
  "99999999999999999999",
])("rejects %s", (input) => {
  expect(parseDollars(input)).toBeNull();
});
