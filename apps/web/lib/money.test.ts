import { expect, it } from "vitest";
import { formatDollars, formatMoney, monthlyFraming, parseDollars } from "./money";

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

it("pairs the monthly charge with an approximate daily framing", () => {
  expect(monthlyFraming(3000)).toEqual({ monthly: "$30/month", daily: "about $1 a day" });
  expect(monthlyFraming(15000)).toEqual({ monthly: "$150/month", daily: "about $5 a day" });
  expect(monthlyFraming(4500)).toEqual({ monthly: "$45/month", daily: "about $1.50 a day" });
  expect(monthlyFraming(3001).daily).toBe("about $1 a day");
});

it.each([
  ["30", 3000], ["30.0", 3000], ["30.01", 3001], ["90.25", 9025], ["0.5", 50], [" 12 ", 1200], ["1234567.89", 123456789],
  ["$30", 3000], ["30.", 3000], ["1,000", 100000], ["$1,234,567.89", 123456789],
])("parses %s as exact cents", (input, minor) => {
  expect(parseDollars(input)).toBe(minor);
});

it.each(["", "$", "1.001", "-1", "$-1", "1e3", ".5", "30..", "1,00", "10,0000", "1,000,00", "$ 30", "99999999999999999999"])("rejects %s", (input) => {
  expect(parseDollars(input)).toBeNull();
});
