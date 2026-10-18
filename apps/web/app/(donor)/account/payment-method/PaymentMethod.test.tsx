import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import PaymentMethodPage from "./PaymentMethod";

const response = (value: unknown) => ({ ok: true, json: async () => value });
afterEach(() => vi.unstubAllGlobals());

it("does not treat returning from Stripe as payment-update confirmation", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response({ status: "OPEN", completed_at: null })));
  render(<PaymentMethodPage />);
  expect(await screen.findByText(/Your update is not confirmed yet/)).toBeVisible();
  expect(screen.queryByText(/Your last payment-method update was confirmed/)).not.toBeInTheDocument();
  expect(screen.getByText(/does not confirm payment of an overdue gift/)).toBeVisible();
});

it("requests a sign-in link in place for a cold recovery visit, without exposing billing controls", async () => {
  const fetch = vi
    .fn()
    .mockResolvedValueOnce({ ok: false, status: 401, json: async () => ({ detail: "Sign in required" }) })
    .mockResolvedValueOnce(response({ message: "If this email matches a donor account, a link will arrive." }));
  vi.stubGlobal("fetch", fetch);
  render(<PaymentMethodPage />);
  fireEvent.change(await screen.findByLabelText("Email"), { target: { value: "donor@example.org" } });
  expect(screen.queryByRole("button", { name: "Continue to Stripe" })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Email me a sign-in link" }));
  await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("If this email matches"));
  expect(fetch.mock.calls[1][0]).toBe("/api/donor/login");
  expect(JSON.parse(fetch.mock.calls[1][1].body)).toEqual({
    email: "donor@example.org",
    next: "/account/payment-method",
  });
});

it("retries a failed setup with the same request key", async () => {
  const fetch = vi
    .fn()
    .mockResolvedValueOnce(response({ status: "NONE", completed_at: null }))
    .mockRejectedValue(new Error("Connection lost"));
  vi.stubGlobal("fetch", fetch);
  render(<PaymentMethodPage />);
  fireEvent.click(await screen.findByRole("button", { name: "Continue to Stripe" }));
  await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("We could not open Stripe"));
  fireEvent.click(screen.getByRole("button", { name: "Continue to Stripe" }));
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(3));
  expect(fetch.mock.calls[1][1].headers["Idempotency-Key"]).toBe(fetch.mock.calls[2][1].headers["Idempotency-Key"]);
});

it("shows a verified update discovered during a retry without opening another setup", async () => {
  const fetch = vi
    .fn()
    .mockResolvedValueOnce(response({ status: "OPEN", completed_at: null }))
    .mockResolvedValueOnce(response({ url: null, status: "APPLIED", completed_at: "2026-10-03T12:00:00Z" }));
  vi.stubGlobal("fetch", fetch);
  render(<PaymentMethodPage />);
  fireEvent.click(await screen.findByRole("button", { name: "Continue to Stripe" }));
  expect(await screen.findByText(/Your last payment-method update was confirmed/)).toBeVisible();
});

it("rejects an unexpected redirect address", async () => {
  vi.stubGlobal(
    "fetch",
    vi
      .fn()
      .mockResolvedValueOnce(response({ status: "NONE", completed_at: null }))
      .mockResolvedValueOnce(response({ url: "https://checkout.stripe.com.attacker.example/" })),
  );
  render(<PaymentMethodPage />);
  fireEvent.click(await screen.findByRole("button", { name: "Continue to Stripe" }));
  await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("We could not open Stripe"));
});
