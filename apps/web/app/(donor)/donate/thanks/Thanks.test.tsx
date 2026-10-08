import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import ThankYouPage from "./page";

async function renderWith(params: Record<string, string | string[] | undefined>) {
  render(await ThankYouPage({ params: Promise.resolve({}), searchParams: Promise.resolve(params) }));
}

it("restates a monthly gift with the charge and daily framing at equal weight", async () => {
  await renderWith({ session_id: "cs_test", amount_minor: "3000", frequency: "MONTHLY" });
  const summary = screen.getByText(/Your gift:/);
  expect(summary).toHaveTextContent("Your gift: $30/month · about $1 a day");
  const parts = [...summary.querySelectorAll("strong")].map((part) => part.textContent);
  expect(parts).toEqual(["$30/month", "about $1 a day"]);
});

it("restates a one-time gift without daily framing", async () => {
  await renderWith({ session_id: "cs_test", amount_minor: "4550", frequency: "ONE_TIME" });
  expect(screen.getByText(/Your gift:/)).toHaveTextContent("Your gift: $45.50 once");
  expect(screen.queryByText(/a day/)).not.toBeInTheDocument();
});

it("carries the Founding Donor timing, receipt notice, and claim call to action", async () => {
  await renderWith({ session_id: "cs_test", amount_minor: "9000", frequency: "MONTHLY" });
  expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Thank you");
  expect(screen.getByText(/launches in April 2027/)).toHaveTextContent("Founding Donor badge");
  expect(screen.getByText(/receipt email follows/)).toBeVisible();
  expect(screen.getByText(/giving streak and your personal referral link/)).toBeVisible();
  expect(screen.getByRole("link", { name: "Claim your account" })).toHaveAttribute("href", "/account/sign-in");
});

it.each([
  {},
  { session_id: "cs_test" },
  { amount_minor: "3000" },
  { amount_minor: "3000", frequency: "WEEKLY" },
  { amount_minor: "30.00", frequency: "MONTHLY" },
  { amount_minor: "-3000", frequency: "MONTHLY" },
  { amount_minor: "99", frequency: "MONTHLY" },
  { amount_minor: "100000000", frequency: "MONTHLY" },
  { amount_minor: ["3000", "9000"], frequency: "MONTHLY" },
  { amount_minor: "3000", frequency: ["MONTHLY", "ONE_TIME"] },
])("falls back to a generic thank-you for missing or invalid parameters: %o", async (params) => {
  await renderWith(params);
  expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Thank you");
  expect(screen.queryByText(/Your gift:/)).not.toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Claim your account" })).toBeVisible();
});
