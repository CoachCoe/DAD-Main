import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import DashboardPage from "./Dashboard";

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
  const link = screen.getByRole("heading", { name: "Link Referrals" }).closest("article");
  const irl = screen.getByRole("heading", { name: "IRL Referrals" }).closest("article");
  if (!link || !irl) throw new Error("Missing referral cards");
  expect(within(link).getByText("2")).toBeVisible();
  expect(within(irl).getByText("3")).toBeVisible();
  expect(screen.getByText("$123.45")).toBeVisible();
  fireEvent.click(screen.getByText("View Link and IRL breakdown"));
  expect(screen.getByText("Link: $100.00")).toBeVisible();
  expect(screen.getByText("IRL: $23.45")).toBeVisible();
  const badge = screen.getByRole("heading", { name: "Founding Donor" }).closest("article");
  if (!badge) throw new Error("Missing badge card");
  expect(within(badge).getByText("Earned")).toBeVisible();
  expect(screen.queryByText(/not yet earned/)).not.toBeInTheDocument();
  expect(screen.queryByText(/Combined referrals/i)).not.toBeInTheDocument();
});

it("shows the badge only when earned, and otherwise explains how it is earned", async () => {
  respond({ ...data, founding_donor: false }); render(<DashboardPage />);
  expect(await screen.findByRole("heading", { name: "Link Referrals" })).toBeVisible();
  expect(screen.queryByRole("heading", { name: "Founding Donor" })).not.toBeInTheDocument();
  expect(screen.getAllByRole("heading", { level: 2 })).toHaveLength(4);
  expect(screen.getByText(/Founding Donor badge: not yet earned/)).toHaveTextContent("first gift completed before DAD launches on 22 April 2027");
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
  await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("Check your connection"));
  expect(screen.getByRole("alert")).not.toHaveTextContent("Offline");
  fireEvent.click(screen.getByRole("button", { name: "Refresh dashboard" }));
  expect(await screen.findByText("$123.45")).toBeVisible();
  expect(screen.getByRole("alert")).toBeEmptyDOMElement();
});

it("distinguishes preparing data from an empty account", async () => {
  respond({ mode: "test", state: "preparing" }); render(<DashboardPage />);
  expect(await screen.findByText(/preparing your dashboard/)).toBeVisible();
  expect(screen.queryByText("0 months")).not.toBeInTheDocument();
});
