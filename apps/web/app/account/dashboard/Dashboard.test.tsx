import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import DashboardPage from "./page";

const data = { mode: "test", state: "ready", computed_at: "2027-01-01T00:00:00Z", founding_donor: true,
  referrals: { link: 2, irl: 3, total_minor: 12345, link_minor: 10000, irl_minor: 2345, currency: "USD" },
  streak: { current: 2, at_risk: false, grace_ends_at: null } };
afterEach(() => vi.unstubAllGlobals());
function respond(value: unknown) {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true, json: async () => value }));
}

it("shows the five elements with separate channels and collected-dollar breakdown", async () => {
  respond(data); render(<DashboardPage />);
  expect(await screen.findByRole("heading", { name: "Link Referrals" })).toBeVisible();
  expect(screen.getAllByRole("heading", { level: 2 })).toHaveLength(5);
  expect(within(screen.getByRole("heading", { name: "Link Referrals" }).closest("article")!).getByText("2")).toBeVisible();
  expect(within(screen.getByRole("heading", { name: "IRL Referrals" }).closest("article")!).getByText("3")).toBeVisible();
  expect(screen.getByText("$123.45")).toBeVisible();
  fireEvent.click(screen.getByText("View Link and IRL breakdown"));
  expect(screen.getByText("Link: $100.00")).toBeVisible();
  expect(screen.getByText("IRL: $23.45")).toBeVisible();
  expect(screen.getByText("Earned in this sandbox")).toBeVisible();
  expect(screen.queryByText(/Combined referrals/i)).not.toBeInTheDocument();
});

it("does not substitute zeros for unavailable metrics", async () => {
  respond({ ...data, referrals: null, streak: null }); render(<DashboardPage />);
  expect(await screen.findByText(/streak is temporarily unavailable/)).toBeVisible();
  expect(screen.getAllByText("Updating")).toHaveLength(4);
  expect(screen.queryByText("0 months")).not.toBeInTheDocument();
  expect(screen.queryByText("$0.00")).not.toBeInTheDocument();
});

it("explains the payment grace state", async () => {
  respond({ ...data, streak: { current: 2, at_risk: true, grace_ends_at: "2027-02-15T00:00:00Z" } });
  render(<DashboardPage />);
  expect(await screen.findByText(/Your streak is at risk/)).toHaveTextContent("Grace ends");
});

it("handles a logged-out response", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: false, status: 401, json: async () => ({ detail: "Sign in required" }) }));
  render(<DashboardPage />);
  expect(await screen.findByRole("link", { name: "Sign in to view your dashboard" })).toBeVisible();
  expect(screen.queryByText("$0.00")).not.toBeInTheDocument();
});

it("supports connection failure and retry", async () => {
  vi.stubGlobal("fetch", vi.fn().mockRejectedValueOnce(new Error("Offline"))
    .mockResolvedValueOnce({ ok: true, json: async () => data }));
  render(<DashboardPage />);
  expect(await screen.findByRole("alert")).toHaveTextContent("Check your connection");
  fireEvent.click(screen.getByRole("button", { name: "Refresh dashboard" }));
  expect(await screen.findByText("$123.45")).toBeVisible();
});

it("distinguishes preparing data from an empty account", async () => {
  respond({ mode: "test", state: "preparing" }); render(<DashboardPage />);
  expect(await screen.findByText(/preparing your dashboard/)).toBeVisible();
  expect(screen.queryByText("0 months")).not.toBeInTheDocument();
});
