import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import DonorDetail from "./DonorDetail";

const detail = { id: "donor-1", email: "donor@example.org", public_name: "Sam",
  streak: { current: 3, best: 5, at_risk: true, computed_at: "2027-01-01T00:00:00Z" },
  referrer: { id: "donor-0", email: "friend@example.org", type: "IRL" },
  gifts: [{ id: "gift-1", amount_minor: 3000, currency: "USD", frequency: "MONTHLY", paid_at: "2027-01-01T00:00:00Z" }],
  referred: [{ id: "donor-2", email: "invitee@example.org", type: "LINK", net_minor: 4500 }],
  next_gifts_offset: null, next_referrals_offset: null };
const actor = { id: "staff-1", email: "f@example.org", name: "Fin", role: "FINANCE", active: true, disabled: false,
  permissions: ["donor.read", "gift.refund", "gift.cancel"] };
const action = (id: string) => ({ id, actor_id: "staff-1", kind: "REFUND", target_id: "gift-1", amount_minor: 1000,
  period_end: null, reason: "Donor asked", status: "SUCCEEDED", completed_at: "2027-01-02T00:00:00Z" });
const reply = (status: number, body: unknown) => ({ ok: status < 400, status, json: async () => body });

afterEach(() => vi.unstubAllGlobals());

function serve(routes: Record<string, ReturnType<typeof reply>>) {
  const fetch = vi.fn(async (url: string) => {
    const path = url.replace(/^\/api/, "").split("?")[0];
    const query = url.split("?")[1] ?? "";
    return routes[`${path}?${query}`] ?? routes[path] ?? reply(500, {});
  });
  vi.stubGlobal("fetch", fetch);
  return fetch;
}

it("shows the donor record with links scoped to the staff member's permissions", async () => {
  serve({ "/donor/staff/donors/donor-1": reply(200, detail), "/donor/staff/me": reply(200, actor),
    "/donor/staff/donors/donor-1/actions": reply(200, { actions: [action("a1")], next_offset: null }) });
  render(<DonorDetail donor="donor-1" />);
  expect(await screen.findByRole("heading", { level: 1, name: "donor@example.org" })).toBeVisible();
  expect(screen.getByText("Streak: 3 · Best: 5 · At risk")).toBeVisible();
  expect(screen.getByText("Referrer: friend@example.org · IRL")).toBeVisible();
  expect(screen.getByText(/invitee@example.org · LINK · Net \$45.00/)).toBeVisible();
  expect(screen.getByRole("link", { name: "Delivery history and resends" })).toHaveAttribute("href", "/staff/messages?donor=donor-1");
  expect(screen.getByRole("link", { name: "Refund gift" })).toHaveAttribute("href", "/staff/finance?donor=donor-1&gift=gift-1");
  expect(screen.getByRole("link", { name: "Cancel monthly giving" })).toHaveAttribute("href", "/staff/finance?donor=donor-1");
  const history = screen.getByRole("region", { name: "Staff financial actions" });
  expect(await within(history).findByText(/REFUND · SUCCEEDED · \$10.00/)).toBeVisible();
  expect(within(history).queryByRole("button", { name: "Older actions" })).not.toBeInTheDocument();
});

it("hides financial links from a role without those permissions", async () => {
  serve({ "/donor/staff/donors/donor-1": reply(200, detail), "/donor/staff/me": reply(200, { ...actor, permissions: ["donor.read"] }),
    "/donor/staff/donors/donor-1/actions": reply(200, { actions: [], next_offset: null }) });
  render(<DonorDetail donor="donor-1" />);
  expect(await screen.findByText("No refunds or cancellations recorded.")).toBeVisible();
  expect(screen.queryByRole("link", { name: "Refund gift" })).not.toBeInTheDocument();
  expect(screen.queryByRole("link", { name: "Cancel monthly giving" })).not.toBeInTheDocument();
});

it("pages through the action history by the offset the API returns", async () => {
  const fetch = serve({ "/donor/staff/donors/donor-1": reply(200, detail), "/donor/staff/me": reply(200, actor),
    "/donor/staff/donors/donor-1/actions?offset=0": reply(200, { actions: [action("a1")], next_offset: 50 }),
    "/donor/staff/donors/donor-1/actions?offset=50": reply(200, { actions: [action("a51")], next_offset: null }) });
  render(<DonorDetail donor="donor-1" />);
  fireEvent.click(await screen.findByRole("button", { name: "Older actions" }));
  expect(await screen.findByRole("button", { name: "Newest actions" })).toBeVisible();
  expect(screen.queryByRole("button", { name: "Older actions" })).not.toBeInTheDocument();
  expect(fetch).toHaveBeenCalledWith("/api/donor/staff/donors/donor-1/actions?offset=50", expect.anything());
  fireEvent.click(screen.getByRole("button", { name: "Newest actions" }));
  expect(await screen.findByRole("button", { name: "Older actions" })).toBeVisible();
});

it("requests the next page of gifts from the detail endpoint", async () => {
  const fetch = serve({ "/donor/staff/donors/donor-1?gifts_offset=0&referrals_offset=0": reply(200, { ...detail, next_gifts_offset: 50 }),
    "/donor/staff/donors/donor-1?gifts_offset=50&referrals_offset=0": reply(200, detail), "/donor/staff/me": reply(200, actor),
    "/donor/staff/donors/donor-1/actions": reply(200, { actions: [], next_offset: null }) });
  render(<DonorDetail donor="donor-1" />);
  fireEvent.click(await screen.findByRole("button", { name: "Older gifts" }));
  expect(await screen.findByRole("button", { name: "Newest gifts" })).toBeVisible();
  expect(fetch).toHaveBeenCalledWith("/api/donor/staff/donors/donor-1?gifts_offset=50&referrals_offset=0", expect.anything());
});

it("announces loading until the record arrives", async () => {
  serve({ "/donor/staff/donors/donor-1": reply(200, detail), "/donor/staff/me": reply(200, actor),
    "/donor/staff/donors/donor-1/actions": reply(200, { actions: [], next_offset: null }) });
  render(<DonorDetail donor="donor-1" />);
  expect(screen.getAllByRole("status")[0]).toHaveTextContent("Loading donor record…");
  expect(await screen.findByRole("heading", { level: 1, name: "donor@example.org" })).toBeVisible();
});

it("says when no donor matches the link", async () => {
  serve({ "/donor/staff/donors/missing": reply(404, { detail: "Not found" }), "/donor/staff/me": reply(200, actor) });
  render(<DonorDetail donor="missing" />);
  expect(await screen.findByText(/No donor record matches this link/)).toBeVisible();
  expect(screen.getByRole("heading", { level: 1, name: "Donor record" })).toBeVisible();
  expect(screen.queryByRole("link", { name: "Staff sign-in" })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Retry loading" })).not.toBeInTheDocument();
});

it("offers sign-in to a signed-out visitor without an error", async () => {
  serve({ "/donor/staff/donors/donor-1": reply(401, { detail: "Sign in" }), "/donor/staff/me": reply(401, { detail: "Sign in" }) });
  render(<DonorDetail donor="donor-1" />);
  expect(await screen.findByRole("link", { name: "Staff sign-in" })).toHaveAttribute("href", "/staff/sign-in");
  expect(screen.getByRole("alert")).toBeEmptyDOMElement();
});

it("distinguishes a missing permission from a missing session", async () => {
  serve({ "/donor/staff/donors/donor-1": reply(403, { detail: "Forbidden" }), "/donor/staff/me": reply(200, actor) });
  render(<DonorDetail donor="donor-1" />);
  expect(await screen.findByText("Your staff role does not permit viewing donor records.")).toBeVisible();
  expect(screen.queryByRole("link", { name: "Staff sign-in" })).not.toBeInTheDocument();
});

it("offers a retry when the service is unavailable", async () => {
  const fetch = serve({ "/donor/staff/donors/donor-1": reply(503, {}), "/donor/staff/me": reply(200, actor) });
  render(<DonorDetail donor="donor-1" />);
  fireEvent.click(await screen.findByRole("button", { name: "Retry loading" }));
  expect(fetch.mock.calls.filter(([url]) => url.startsWith("/api/donor/staff/donors/donor-1?"))).toHaveLength(2);
});
