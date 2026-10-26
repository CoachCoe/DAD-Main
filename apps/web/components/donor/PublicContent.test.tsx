import { render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import InitiativePage from "@/app/(donor)/initiatives/[slug]/page";
import InitiativesPage from "@/app/(donor)/initiatives/page";
import LedgerPage from "@/app/(donor)/ledger/page";

const refresh = vi.fn();
vi.mock("next/navigation", async (original) => ({
  ...(await original<typeof import("next/navigation")>()),
  useRouter: () => ({ refresh }),
  notFound: () => {
    throw new Error("NEXT_NOT_FOUND");
  },
}));

// Rendering outside a request: there is no request scope for connection() to wait on.
vi.mock("next/server", () => ({ connection: async () => undefined }));

const initiative = {
  id: "8f9e0d1c-2b3a-4c5d-8e6f-7a8b9c0d1e2f",
  slug: "water",
  title: "Clean water",
  summary: "Wells with a partner.",
  type: "Water",
  location: "Northern region",
  status: "ACTIVE",
  started_on: "2099-05-01",
  partner: "Partner Org",
};
const published = {
  state: "published",
  ledger_as_of: "2026-09-01",
  cadence: "monthly",
  initiatives: [],
  ledger: [
    { id: "snapshot:0", metric: "Collected", value: 3000, unit: "USD_CENTS", illustrative: false },
    { id: "snapshot:1", metric: "Example reach", value: 100, unit: "PEOPLE", illustrative: true },
  ],
};
afterEach(() => {
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
  refresh.mockReset();
});
const respond = (body: unknown, status = 200) =>
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify(body), { status })),
  );

it("labels illustrative figures alongside real figures and their as-of date", async () => {
  respond(published);
  render(await LedgerPage());
  expect(screen.getByRole("heading", { level: 1, name: "Impact ledger" })).toBeVisible();
  expect(screen.getByText("$30.00")).toBeInTheDocument();
  expect(screen.getByText("Illustrative figure — not a reported outcome")).toBeInTheDocument();
  expect(screen.getByText("Sep 1, 2026")).toHaveAttribute("dateTime", "2026-09-01");
  expect(screen.getByText("100 people").closest("p")).toHaveTextContent("Illustrative");
  expect(screen.getByText("Source document retained by DAD and available on request.")).toBeInTheDocument();
});

it("names a count of one in the singular", async () => {
  respond({
    ...published,
    ledger: [
      { id: "snapshot:0", metric: "Partners onboarded", value: 1, unit: "PARTNERS", illustrative: false },
      { id: "snapshot:1", metric: "People reached", value: 1, unit: "PEOPLE", illustrative: false },
    ],
  });
  render(await LedgerPage());
  expect(screen.getByText("1 partner")).toBeInTheDocument();
  expect(screen.getByText("1 person")).toBeInTheDocument();
});

it("fetches the configured API with a one-minute shared cache", async () => {
  vi.stubEnv("DONOR_API_BASE_URL", "http://api.internal:8002");
  respond(published);
  render(await LedgerPage());
  const fetch = vi.mocked(globalThis.fetch);
  expect(fetch.mock.calls[0][0]).toBe("http://api.internal:8002/donor/public/content");
  expect(fetch.mock.calls[0][1]).toMatchObject({ next: { revalidate: 60 } });
});

it.each([
  ["an unreachable API", () => vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("fetch failed")))],
  ["a server error", () => respond({ detail: "boom" }, 503)],
])("offers a retry for %s and never substitutes demonstration numbers", async (_label, fail) => {
  fail();
  render(await LedgerPage());
  expect(screen.getByRole("alert")).toHaveTextContent("Published records are unavailable");
  expect(screen.queryByText("$30.00")).not.toBeInTheDocument();
  expect(screen.queryByText(/No ledger figures have been published/)).not.toBeInTheDocument();
  screen.getByRole("button", { name: "Retry records" }).click();
  expect(refresh).toHaveBeenCalledTimes(1);
});

it("distinguishes an unpublished ledger from an unavailable one", async () => {
  respond({ ...published, ledger: [], state: "unpublished", ledger_as_of: null, cadence: null });
  render(await LedgerPage());
  expect(screen.getByText(/No ledger figures have been published/)).toBeInTheDocument();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

it("links each initiative to its detail page", async () => {
  respond([initiative]);
  render(await InitiativesPage());
  expect(screen.getByRole("link", { name: "Clean water" })).toHaveAttribute("href", "/initiatives/water");
  expect(screen.getByText(/Partner: Partner Org/)).toHaveTextContent("Planned start");
});

it("renders a published initiative and treats an unknown slug as not found", async () => {
  respond(initiative);
  render(await InitiativePage({ params: Promise.resolve({ slug: "water" }), searchParams: Promise.resolve({}) }));
  expect(screen.getByRole("heading", { level: 1, name: "Clean water" })).toBeVisible();
  expect(screen.getByText(/not designated to this initiative/)).toBeVisible();
  respond({ detail: "Record not found" }, 404);
  await expect(
    InitiativePage({ params: Promise.resolve({ slug: "missing" }), searchParams: Promise.resolve({}) }),
  ).rejects.toThrow("NEXT_NOT_FOUND");
});

it("shows a running initiative without its partner when none is linked", async () => {
  respond([{ ...initiative, started_on: "2026-03-01", partner: null }]);
  render(await InitiativesPage());
  expect(screen.queryByText(/Partner:/)).toBeNull();
  expect(screen.getByText(/Started/)).toBeInTheDocument();
  expect(screen.queryByText(/not a recipient enrolment form/)).toBeNull();
});
