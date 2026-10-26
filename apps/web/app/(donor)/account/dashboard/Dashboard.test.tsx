import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import DashboardPage from "./Dashboard";

const data = {
  mode: "test",
  state: "ready",
  computed_at: "2027-01-01T00:00:00Z",
  founding_donor: true,
  referrals: { link: 2, irl: 3, total_minor: 12345, link_minor: 10000, irl_minor: 2345, currency: "USD" },
  streak: {
    current: 2,
    at_risk: false,
    grace_ends_at: null,
    ladder: { days: 24, last_milestone: 7, next_milestone: 30, days_to_next: 6, bar: Array(30).fill(true) },
  },
  rank: { national_percentile: 2, city: "Reno", city_rank: 7, computed_at: "2027-01-01T00:00:00Z" },
  network: { active_count: 3, annualized_minor: 108000, currency: "USD" },
  impact: { given_minor: 4500, currency: "USD", initiatives_funded: 4, events_volunteered: 1 },
};
const today = {
  initiative_id: "i-1",
  slug: "reno-meals",
  title: "Reno meals",
  summary: "Hot meals.",
  region: "Northern Nevada",
  location: "Reno, NV",
  type: "Food",
  started_on: "2026-03-01",
  metrics: [{ label: "Meals served", value: 1200, unit: "PEOPLE", verified_on: "2026-09-30" }],
  distinct_shown: 4,
};
afterEach(() => vi.unstubAllGlobals());
/** Stubs fetch with `dashboard` for the dashboard and `initiative` (default: none today) for today's initiative. */
function stubFetch(dashboard: (input: RequestInfo | URL) => Promise<unknown>, initiative?: unknown) {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("today-initiative"))
        return Promise.resolve(
          initiative
            ? { ok: true, json: async () => initiative }
            : { ok: false, status: 404, json: async () => ({ detail: "Record not found" }) },
        );
      if (url.includes("/spend"))
        return Promise.resolve({
          ok: true,
          json: async () => ({
            initiative_id: "i-1",
            total_minor: 90000,
            categories: [{ category: "Food", amount_minor: 90000 }],
          }),
        });
      return dashboard(input);
    }),
  );
}
function respond(value: unknown, initiative?: unknown) {
  stubFetch(() => Promise.resolve({ ok: true, json: async () => value }), initiative);
}

it("reserves the metric layout without announcing unverified results", async () => {
  let resolve!: (response: { ok: boolean; json: () => Promise<typeof data> }) => void;
  stubFetch(
    () =>
      new Promise((done) => {
        resolve = done;
      }),
  );
  const { container } = render(<DashboardPage />);
  expect(container.querySelectorAll('[aria-hidden="true"] .metricSkeleton')).toHaveLength(8);
  expect(screen.getByRole("status")).toHaveTextContent("Loading your dashboard");
  expect(screen.queryByRole("heading", { name: "Link Referrals" })).not.toBeInTheDocument();
  expect(screen.queryByText("Earned")).not.toBeInTheDocument();
  resolve({ ok: true, json: async () => data });
  expect(await screen.findByText("$123.45")).toBeVisible();
  expect(container.querySelectorAll(".metricSkeleton")).toHaveLength(0);
});

it("shows the five elements with separate channels and collected-dollar breakdown", async () => {
  respond(data);
  render(<DashboardPage />);
  expect(await screen.findByRole("heading", { name: "Link Referrals" })).toBeVisible();
  expect(screen.getAllByRole("heading", { level: 2 })).toHaveLength(9);
  const link = screen.getByRole("heading", { name: "Link Referrals" }).closest("article");
  const irl = screen.getByRole("heading", { name: "IRL Referrals" }).closest("article");
  if (!link || !irl) throw new Error("Missing referral cards");
  expect(within(link).getByText("2")).toBeVisible();
  expect(within(irl).getByText("3")).toBeVisible();
  expect(screen.getByText("$123.45")).toBeVisible();
  expect(screen.getByText("24 days")).toBeVisible();
  expect(screen.getByText("6 days to your 30-day milestone.")).toBeVisible();
  expect(screen.getByText("Top 2% nationally · #7 in Reno")).toBeVisible();
  expect(screen.getByText("$1,080.00 a year")).toBeVisible();
  expect(screen.getByRole("img", { name: "30 of the last 30 days in your current streak" })).toBeVisible();
  fireEvent.click(screen.getByText("View Link and IRL breakdown"));
  expect(screen.getByText("Link: $100.00")).toBeVisible();
  expect(screen.getByText("IRL: $23.45")).toBeVisible();
  const badge = screen.getByRole("heading", { name: "Founding Donor" }).closest("article");
  if (!badge) throw new Error("Missing badge card");
  expect(within(badge).getByText("Earned")).toBeVisible();
  expect(screen.queryByText(/not yet earned/)).not.toBeInTheDocument();
  expect(screen.queryByText(/Combined referrals/i)).not.toBeInTheDocument();
});

it("counts today's initiative as funded once its card has loaded", async () => {
  respond(data, { ...today, distinct_shown: 6 });
  render(<DashboardPage />);
  const funded = (await screen.findByRole("heading", { name: "Initiatives funded" })).closest("article");
  if (!funded) throw new Error("Missing initiatives funded card");
  await waitFor(() => expect(within(funded).getByText("6")).toBeVisible());
});

it("shows the badge only when earned, and otherwise explains how it is earned", async () => {
  respond({ ...data, founding_donor: false });
  render(<DashboardPage />);
  expect(await screen.findByRole("heading", { name: "Link Referrals" })).toBeVisible();
  expect(screen.queryByRole("heading", { name: "Founding Donor" })).not.toBeInTheDocument();
  expect(screen.getAllByRole("heading", { level: 2 })).toHaveLength(8);
  expect(screen.getByText(/Founding Donor badge: not yet earned/)).toHaveTextContent(
    "first gift completed before 1 January 2027",
  );
});

it("does not substitute zeros for unavailable metrics", async () => {
  respond({ ...data, referrals: null, streak: null });
  render(<DashboardPage />);
  expect(await screen.findByText(/streak is temporarily unavailable/)).toBeVisible();
  expect(screen.getAllByText("Updating")).toHaveLength(4);
  expect(screen.queryByText("0 days")).not.toBeInTheDocument();
  expect(screen.queryByText("$0.00")).not.toBeInTheDocument();
});

it("can refresh an unavailable streak while keeping verified referral totals", async () => {
  stubFetch(
    vi
      .fn()
      .mockResolvedValueOnce({ ok: true, json: async () => ({ ...data, streak: null }) })
      .mockResolvedValueOnce({ ok: true, json: async () => data }),
  );
  render(<DashboardPage />);
  expect(await screen.findByText("$123.45")).toBeVisible();
  expect(screen.getByRole("status")).toHaveTextContent("Your streak is updating");
  expect(screen.queryByText("0 days")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Refresh dashboard" }));
  expect(await screen.findByText("24 days")).toBeVisible();
  expect(screen.getByText("$123.45")).toBeVisible();
  expect(screen.queryByRole("button", { name: "Refresh dashboard" })).not.toBeInTheDocument();
});

it("explains the payment grace state", async () => {
  respond({
    ...data,
    streak: { ...data.streak, at_risk: true, grace_ends_at: "2027-02-15T00:00:00Z" },
  });
  render(<DashboardPage />);
  expect(await screen.findByText(/Your streak is at risk/)).toHaveTextContent(
    "grace period ends, to keep your 24-day streak",
  );
  expect(screen.getByRole("link", { name: "Update payment method" })).toHaveAttribute(
    "href",
    "/account/payment-method",
  );
});

it("handles a logged-out response", async () => {
  stubFetch(vi.fn().mockResolvedValue({ ok: false, status: 401, json: async () => ({ detail: "Sign in required" }) }));
  render(<DashboardPage />);
  expect(await screen.findByRole("link", { name: "Sign in to view your dashboard" })).toBeVisible();
  expect(screen.queryByText("$0.00")).not.toBeInTheDocument();
});

it("supports connection failure and retry", async () => {
  stubFetch(
    vi
      .fn()
      .mockRejectedValueOnce(new Error("Offline"))
      .mockResolvedValueOnce({ ok: true, json: async () => data }),
  );
  render(<DashboardPage />);
  await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("Check your connection"));
  expect(screen.getByRole("alert")).not.toHaveTextContent("Offline");
  fireEvent.click(screen.getByRole("button", { name: "Refresh dashboard" }));
  expect(await screen.findByText("$123.45")).toBeVisible();
  expect(screen.getByRole("alert")).toBeEmptyDOMElement();
});

it("distinguishes preparing data from an empty account", async () => {
  respond({ mode: "test", state: "preparing" });
  render(<DashboardPage />);
  expect(await screen.findByText(/preparing your dashboard/)).toBeVisible();
  expect(screen.queryByText("0 days")).not.toBeInTheDocument();
});

it("shows the donor's own impact and today's initiative with its spending", async () => {
  respond(data, today);
  render(<DashboardPage />);
  const given = (await screen.findByRole("heading", { name: "Your giving" })).closest("article");
  if (!given) throw new Error("Missing giving card");
  expect(within(given).getByText("$45.00")).toBeVisible();
  const funded = screen.getByRole("heading", { name: "Initiatives funded" }).closest("article");
  const volunteered = screen.getByRole("heading", { name: "Events volunteered" }).closest("article");
  if (!funded || !volunteered) throw new Error("Missing impact cards");
  expect(within(funded).getByText("4")).toBeVisible();
  expect(within(volunteered).getByText("1")).toBeVisible();
  expect(await screen.findByRole("heading", { name: "Reno meals" })).toBeVisible();
  expect(screen.getByText("Food · Reno, NV · Since Mar 1, 2026")).toBeVisible();
  expect(screen.getByText("Meals served: 1,200 people (verified Sep 30, 2026)")).toBeVisible();
  fireEvent.click(screen.getByText("See every dollar this initiative has spent"));
  expect(await screen.findByText("Food: $900.00")).toBeVisible();
});

it("shows giving as updating rather than zero while it is recomputed", async () => {
  respond({ ...data, impact: { ...data.impact, given_minor: null } });
  render(<DashboardPage />);
  const given = (await screen.findByRole("heading", { name: "Your giving" })).closest("article");
  if (!given) throw new Error("Missing giving card");
  expect(within(given).getByText("Updating")).toBeVisible();
});
