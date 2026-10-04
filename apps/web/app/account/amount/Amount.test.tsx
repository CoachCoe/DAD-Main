import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import AmountPage from "./page";

const options = { state: "ready", subscription_id: "owned-subscription", amount_minor: 3000, effective_at: "2026-11-01T12:00:00Z", change: null };
const response = (value: unknown) => ({ ok: true, json: async () => value });
afterEach(() => vi.unstubAllGlobals());

it("confirms integer minor units and the displayed renewal without an immediate charge", async () => {
  const fetch = vi.fn().mockResolvedValueOnce(response(options)).mockResolvedValueOnce(response({
    request_id: "command", amount_minor: 9025, previous_minor: 3000, effective_at: options.effective_at, confirmed: true,
  }));
  vi.stubGlobal("fetch", fetch);
  render(<AmountPage />);
  fireEvent.change(await screen.findByLabelText("Monthly amount in USD"), { target: { value: "90.25" } });
  fireEvent.click(screen.getByRole("button", { name: "Review amount change" }));
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(screen.getByText(/Past gifts and outstanding invoices keep/)).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Confirm amount change" }));
  await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Last amount change confirmed: $90.25/month"));
  expect(JSON.parse(fetch.mock.calls[1][1].body)).toEqual({ subscription_id: options.subscription_id,
    amount_minor: 9025, previous_minor: 3000, effective_at: options.effective_at });
});

it("retries a lost response with an unchanged command", async () => {
  const fetch = vi.fn().mockResolvedValueOnce(response(options)).mockRejectedValue(new Error("Offline"));
  vi.stubGlobal("fetch", fetch);
  render(<AmountPage />);
  fireEvent.change(await screen.findByLabelText("Monthly amount in USD"), { target: { value: "90" } });
  fireEvent.click(screen.getByRole("button", { name: "Review amount change" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirm amount change" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("not confirmed");
  fireEvent.click(screen.getByRole("button", { name: "Confirm amount change" }));
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(3));
  expect(fetch.mock.calls[1][1].headers["Idempotency-Key"]).toBe(fetch.mock.calls[2][1].headers["Idempotency-Key"]);
  expect(fetch.mock.calls[1][1].body).toBe(fetch.mock.calls[2][1].body);
});

it("rejects fractional cents before confirmation", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response(options)));
  render(<AmountPage />);
  fireEvent.change(await screen.findByLabelText("Monthly amount in USD"), { target: { value: "30.001" } });
  fireEvent.click(screen.getByRole("button", { name: "Review amount change" }));
  expect(screen.getByRole("alert")).toHaveTextContent("two decimal places");
  expect(screen.queryByRole("button", { name: "Confirm amount change" })).not.toBeInTheDocument();
});
