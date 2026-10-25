import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import Dashboard from "./Dashboard";

afterEach(() => vi.unstubAllGlobals());

const overview = {
  organization: "Delta Inc",
  role: "ADMIN",
  company_code: "DELTA-7KQ4MX",
  committed_minor: 150000,
  deployed_minor: 50000,
  deployed_percent: 33,
  people_reached: 200,
  employees_enrolled: null,
  workforce_size: 400,
  enrolled_percent: null,
  updated_at: "2026-10-20T15:00:00Z",
};

const initiative = {
  id: "i1",
  title: "Reno Meals",
  summary: "Hot meals delivered daily.",
  status: "ACTIVE",
  region: "Northern Nevada",
  county_count: 3,
  committed_minor: 100000,
  deployed_minor: 40000,
  metrics: [
    { label: "Households served", value: 150, unit: "PEOPLE", verified_on: "2026-10-18" },
    { label: "Cost per delivery", value: 425, unit: "USD_CENTS", verified_on: "2026-10-18" },
  ],
  spend: [{ category: "Direct program delivery", amount_minor: 40000 }],
  updated_at: "2026-10-20T15:00:00Z",
};

function stub(overrides: Record<string, unknown> = {}, calls: { url: string; init?: RequestInit }[] = []) {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push({ url, init });
      if (url.endsWith("/overview")) return Promise.resolve(Response.json({ ...overview, ...overrides }));
      if (url.endsWith("/initiatives")) return Promise.resolve(Response.json({ initiatives: [initiative] }));
      if (url.endsWith("/spend"))
        return Promise.resolve(Response.json({ categories: [{ category: "Meals", amount_minor: 50000 }] }));
      if (url.endsWith("/users") && init?.method === "POST") return Promise.resolve(Response.json({ invited: true }));
      if (url.endsWith("/users"))
        return Promise.resolve(
          Response.json({ users: [{ id: "u1", email: "a@delta.test", name: "Ana", role: "ADMIN" }] }),
        );
      if (url.endsWith("/exports")) return Promise.resolve(Response.json({ exports: [] }));
      if (url.endsWith("/our-people"))
        return Promise.resolve(
          Response.json({
            enrolled: 0,
            workforce_size: 400,
            given_minor: 0,
            matched_minor: 0,
            avg_streak_days: 0,
            volunteer_minutes: 0,
            teams: 0,
            suppressed: true,
          }),
        );
      if (url.endsWith("/teams")) return Promise.resolve(Response.json({ teams: [] }));
      return Promise.resolve(Response.json({}));
    }),
  );
}

it("shows the four headline tiles, suppressing enrollment under five employees", async () => {
  stub();
  render(<Dashboard />);
  const tiles = within(await screen.findByRole("region", { name: "Overview" }));
  expect(tiles.getByText("$1,500.00")).toBeInTheDocument();
  expect(tiles.getByText("33% of committed")).toBeInTheDocument();
  expect(tiles.getByText("200")).toBeInTheDocument();
  expect(tiles.getByText("Fewer than 5")).toBeInTheDocument();
  expect(screen.getByText(/Figures last updated/)).toBeInTheDocument();
});

it("shows each initiative's status, progress, headline metrics and detail", async () => {
  stub();
  render(<Dashboard />);
  const card = within(await screen.findByRole("article", { name: "Reno Meals" }));
  expect(card.getByText("Active")).toBeInTheDocument();
  expect(card.getByText("Northern Nevada · 3 counties")).toBeInTheDocument();
  expect(card.getByText("$400.00 deployed of $1,000.00 committed (40%)")).toBeInTheDocument();
  expect(card.getAllByText("$4.25").length).toBeGreaterThan(0);
  expect(card.getByText("Direct program delivery")).toBeInTheDocument();
});

it("gives a partner admin export and user management, and emails invitations", async () => {
  const calls: { url: string; init?: RequestInit }[] = [];
  stub({}, calls);
  render(<Dashboard />);
  expect(await screen.findByRole("button", { name: "Export verified report (PDF)" })).toBeInTheDocument();
  fireEvent.change(await screen.findByLabelText("Work email"), { target: { value: "val@delta.test" } });
  fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Val" } });
  fireEvent.click(screen.getByRole("button", { name: "Email invitation" }));
  expect(await screen.findByText("We emailed val@delta.test an invitation to sign in.")).toBeInTheDocument();
  const sent = calls.find((c) => c.url.endsWith("/users") && c.init?.method === "POST");
  expect(JSON.parse(String(sent?.init?.body))).toEqual({ email: "val@delta.test", name: "Val", role: "VIEWER" });
  expect(screen.getByText("DELTA-7KQ4MX")).toBeInTheDocument();
});

it("keeps a partner viewer read-only", async () => {
  stub({ role: "VIEWER" });
  render(<Dashboard />);
  expect(
    await screen.findByText("Your organization’s partner administrators can export the report."),
  ).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /Export verified report/ })).not.toBeInTheDocument();
  expect(screen.queryByRole("region", { name: "Users" })).not.toBeInTheDocument();
  expect(screen.getByText("DELTA-7KQ4MX")).toBeInTheDocument();
});

it("sends a signed-out partner to request a sign-in link", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(() => Promise.resolve(Response.json({ detail: "Partner sign-in required" }, { status: 401 }))),
  );
  render(<Dashboard />);
  await waitFor(() =>
    expect(screen.getByRole("link", { name: "Email me a sign-in link" })).toHaveAttribute("href", "/partner/sign-in"),
  );
});
