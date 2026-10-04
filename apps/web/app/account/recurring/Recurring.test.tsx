import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import RecurringPage from "./page";

const subscription = { id: "owned-id", amount_minor: 3000, currency: "USD", status: "active",
  period_end: "2026-11-01T12:00:00Z", stops_at: null };
const response = (value: unknown) => ({ ok: true, json: async () => value });
afterEach(() => vi.unstubAllGlobals());

it("requires explicit confirmation and sends the displayed billing boundary", async () => {
  const fetch = vi.fn().mockResolvedValueOnce(response({ state: "ready", subscription }))
    .mockResolvedValueOnce(response({ state: "ready", subscription: { ...subscription, stops_at: subscription.period_end } }));
  vi.stubGlobal("fetch", fetch);
  render(<RecurringPage />);
  fireEvent.click(await screen.findByRole("button", { name: "Cancel monthly giving" }));
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(screen.getByText(/does not refund past gifts/)).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Confirm cancel renewals" }));
  expect(await screen.findByText(/Renewals cancelled/)).toBeVisible();
  expect(JSON.parse(fetch.mock.calls[1][1].body)).toEqual({ subscription_id: "owned-id", period_end: subscription.period_end });
  expect(fetch.mock.calls[1][1].headers["Idempotency-Key"]).toMatch(/^[a-f0-9-]{36}$/);
});

it("retries an uncertain cancellation with the same key and never claims success", async () => {
  const fetch = vi.fn().mockResolvedValueOnce(response({ state: "ready", subscription }))
    .mockRejectedValue(new Error("Connection lost"));
  vi.stubGlobal("fetch", fetch);
  render(<RecurringPage />);
  fireEvent.click(await screen.findByRole("button", { name: "Cancel monthly giving" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirm cancel renewals" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("Cancellation is not confirmed");
  expect(screen.queryByText(/Renewals cancelled/)).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Confirm cancel renewals" }));
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(3));
  expect(fetch.mock.calls[1][1].headers["Idempotency-Key"]).toBe(fetch.mock.calls[2][1].headers["Idempotency-Key"]);
});

it("offers a sign-in path when opened without a session", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: false, status: 401, json: async () => ({ detail: "Sign in required" }) }));
  render(<RecurringPage />);
  expect(await screen.findByRole("link", { name: "Sign in to manage your giving" })).toHaveAttribute("href", "/account/sign-in");
  expect(screen.queryByRole("button", { name: "Cancel monthly giving" })).not.toBeInTheDocument();
});

it("distinguishes unconfirmed checkout from having no monthly gift", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response({ state: "preparing", subscription: null })));
  render(<RecurringPage />);
  expect(await screen.findByText(/checkout is still being confirmed/)).toBeVisible();
  expect(screen.queryByText("You have no monthly gift to manage.")).not.toBeInTheDocument();
});
