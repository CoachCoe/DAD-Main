import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import Receipts from "./Receipts";

afterEach(() => vi.unstubAllGlobals());
const response = (value: unknown) => ({ ok: true, json: async () => value });
const gift = { id: "g1", amount_minor: 3000, refunded_minor: 0, net_minor: 3000, paid_at: "2026-10-01T12:00:00Z", frequency: "MONTHLY", refunds: [] };

it("opens the gift named in the address without reading window.location", async () => {
  const fetch = vi.fn().mockResolvedValue(response({ as_of: "2026-10-04T12:00:00Z", receipt_id: "R-1", gift }));
  vi.stubGlobal("fetch", fetch);
  render(<Receipts gift="g/1" defaultYear={2026} latestYear={2026} />);
  expect(await screen.findByText("Reference: R-1")).toBeVisible();
  expect(fetch.mock.calls[0][0]).toBe("/api/donor/me/receipts/g%2F1");
});

it("defaults the summary year from the server and requests that year", async () => {
  const fetch = vi.fn().mockResolvedValue(response({ as_of: "2028-01-04T12:00:00Z", year: 2027, timezone: "America/New_York",
    gifts: [gift], amount_minor: 3000, refunded_minor: 0, net_minor: 3000 }));
  vi.stubGlobal("fetch", fetch);
  render(<Receipts defaultYear={2027} latestYear={2028} />);
  const year = screen.getByLabelText("Calendar year");
  expect(year).toHaveValue(2027);
  expect(year).toHaveAttribute("max", "2028");
  expect(fetch).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "View annual summary" }));
  expect(await screen.findByRole("heading", { name: "2027 annual test summary" })).toBeVisible();
  expect(fetch.mock.calls[0][0]).toBe("/api/donor/me/annual-summary/2027");
  fireEvent.click(screen.getByRole("button", { name: "View annual summary" }));
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
});
