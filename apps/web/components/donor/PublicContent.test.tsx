import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { PublicContent } from "./PublicContent";

const published = { state: "published", ledger_as_of: "2026-09-01", cadence: "monthly", initiatives: [],
  ledger: [{ metric: "Collected", value: 3000, unit: "USD_CENTS", illustrative: false },
    { metric: "Example reach", value: 100, unit: "PEOPLE", illustrative: true }] };
afterEach(() => vi.unstubAllGlobals());

it("labels illustrative figures alongside real figures and their as-of date", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true, json: async () => published }));
  render(<PublicContent view="ledger" />);
  expect(await screen.findByText("$30.00")).toBeInTheDocument();
  expect(screen.getByText("Illustrative figure — not a reported outcome")).toBeInTheDocument();
  expect(screen.getByText("2026-09-01")).toHaveAttribute("dateTime", "2026-09-01");
  expect(screen.getByText("Source document retained by DAD and available on request.")).toBeInTheDocument();
});

it("retries unavailable records and never substitutes demonstration numbers", async () => {
  const fetch = vi.fn().mockRejectedValueOnce(new Error("offline")).mockResolvedValue({ ok: true,
    json: async () => ({ ...published, ledger: [], state: "unpublished", ledger_as_of: null, cadence: null }) });
  vi.stubGlobal("fetch", fetch);
  render(<PublicContent view="ledger" />);
  expect(await screen.findByRole("alert")).toHaveTextContent("could not load");
  fireEvent.click(screen.getByRole("button", { name: "Retry records" }));
  expect(await screen.findByText(/No ledger figures have been published/)).toBeInTheDocument();
  expect(screen.queryByText("$30.00")).not.toBeInTheDocument();
});
