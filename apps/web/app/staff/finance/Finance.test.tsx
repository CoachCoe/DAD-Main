import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import StaffFinancePage from "./Finance";

const quote = { gift_id: "gift", donor_id: "donor", amount_minor: 3000, available_minor: 3000 };
const response = (value: unknown) => ({ ok: true, json: async () => value });
const actions = (items: unknown[], next_offset: number | null = null) => response({ actions: items, next_offset });
const saved = { id: "saved-request", actor_id: "staff", kind: "REFUND", target_id: "gift", amount_minor: 1000,
  reason: "Donor requested refund", status: "AUTHORIZED" };
afterEach(() => { vi.unstubAllGlobals(); });

it("requires confirmation of exact refund cents and the audit reason", async () => {
  const fetch = vi.fn().mockResolvedValueOnce(response(quote)).mockResolvedValueOnce(response({ id: "staff" }))
    .mockResolvedValueOnce(actions([])).mockResolvedValueOnce(response({ status: "PENDING" }))
    .mockResolvedValueOnce(response({ ...quote, available_minor: 1766 })).mockResolvedValueOnce(response({ id: "staff" }))
    .mockResolvedValueOnce(actions([]));
  vi.stubGlobal("fetch", fetch);
  render(<StaffFinancePage donor="donor" gift="gift" />);
  fireEvent.change(await screen.findByLabelText("Refund in USD"), { target: { value: "12.34" } });
  fireEvent.change(screen.getByLabelText("Audit reason"), { target: { value: "Donor requested partial refund" } });
  fireEvent.click(screen.getByRole("button", { name: "Review financial action" }));
  expect(fetch).toHaveBeenCalledTimes(3);
  expect(screen.getByText("Refund $12.34 USD to the original payment method.")).toBeVisible();
  expect(screen.getByRole("heading", { name: "Confirm financial action" })).toHaveFocus();
  fireEvent.click(screen.getByRole("button", { name: "Confirm financial action" }));
  await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Stripe refund status: PENDING"));
  expect(JSON.parse(fetch.mock.calls[3][1].body)).toEqual({ amount_minor: 1234, reason: "Donor requested partial refund" });
  expect(await screen.findByText(/Available to refund: \$17.66/)).toBeVisible();
});

it("reuses a persisted uncertain request rather than minting another refund", async () => {
  const fetch = vi.fn().mockResolvedValueOnce(response(quote)).mockResolvedValueOnce(response({ id: "staff" }))
    .mockResolvedValueOnce(actions([saved])).mockRejectedValue(new Error("Offline"));
  vi.stubGlobal("fetch", fetch);
  render(<StaffFinancePage donor="donor" gift="gift" />);
  fireEvent.click(await screen.findByRole("button", { name: "Retry original financial action" }));
  await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("not confirmed"));
  expect(fetch.mock.calls[3][1].headers["Idempotency-Key"]).toBe("saved-request");
  expect(JSON.parse(fetch.mock.calls[3][1].body).amount_minor).toBe(1000);
});

it("finds an uncertain request on a later page of the action history", async () => {
  const other = { ...saved, id: "someone-else", actor_id: "other-staff" };
  const fetch = vi.fn().mockResolvedValueOnce(response(quote)).mockResolvedValueOnce(response({ id: "staff" }))
    .mockResolvedValueOnce(actions([other], 50)).mockResolvedValueOnce(actions([saved]));
  vi.stubGlobal("fetch", fetch);
  render(<StaffFinancePage donor="donor" gift="gift" />);
  expect(await screen.findByRole("button", { name: "Retry original financial action" })).toBeVisible();
  expect(fetch.mock.calls.slice(2).map(([url]) => url)).toEqual([
    "/api/donor/staff/donors/donor/actions?offset=0", "/api/donor/staff/donors/donor/actions?offset=50"]);
});

it("stops paging once the action history is exhausted", async () => {
  const fetch = vi.fn().mockResolvedValueOnce(response(quote)).mockResolvedValueOnce(response({ id: "staff" }))
    .mockResolvedValueOnce(actions([{ ...saved, status: "SUCCEEDED" }]));
  vi.stubGlobal("fetch", fetch);
  render(<StaffFinancePage donor="donor" gift="gift" />);
  expect(await screen.findByRole("button", { name: "Review financial action" })).toBeVisible();
  expect(fetch).toHaveBeenCalledTimes(3);
});

it("ties refund validation errors to the field and keeps the request unsent", async () => {
  const fetch = vi.fn().mockResolvedValueOnce(response(quote)).mockResolvedValueOnce(response({ id: "staff" }))
    .mockResolvedValueOnce(actions([]));
  vi.stubGlobal("fetch", fetch);
  render(<StaffFinancePage donor="donor" gift="gift" />);
  const amount = await screen.findByLabelText("Refund in USD");
  fireEvent.change(amount, { target: { value: "45.00" } });
  fireEvent.change(screen.getByLabelText("Audit reason"), { target: { value: "Donor requested refund" } });
  fireEvent.click(screen.getByRole("button", { name: "Review financial action" }));
  expect(screen.getByRole("alert")).toHaveTextContent("within the available refund balance");
  expect(amount).toHaveAttribute("aria-invalid", "true");
  expect(amount).toHaveAccessibleDescription(/available refund balance/);
  expect(amount).toHaveFocus();
  expect(fetch).toHaveBeenCalledTimes(3);
});

it("distinguishes missing permission from a signed-out session", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: false, status: 403, json: async () => ({ detail: "Forbidden" }) }));
  const { unmount } = render(<StaffFinancePage donor="donor" gift="gift" />);
  await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("does not permit refunds"));
  expect(screen.queryByRole("link", { name: "Staff sign-in" })).not.toBeInTheDocument();
  unmount();
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: false, status: 401, json: async () => ({ detail: "Sign in" }) }));
  render(<StaffFinancePage donor="donor" gift="gift" />);
  expect(await screen.findByRole("link", { name: "Staff sign-in" })).toHaveAttribute("href", "/staff/sign-in");
  expect(screen.getByRole("alert")).toBeEmptyDOMElement();
});
