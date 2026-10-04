import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import StaffFinancePage from "./page";

const quote = { gift_id: "gift", donor_id: "donor", amount_minor: 3000, available_minor: 3000 };
const response = (value: unknown) => ({ ok: true, json: async () => value });
afterEach(() => { vi.unstubAllGlobals(); window.history.replaceState(null, "", "/"); });

it("requires confirmation of exact refund cents and the audit reason", async () => {
  window.history.replaceState(null, "", "/staff/finance?donor=donor&gift=gift");
  const fetch = vi.fn().mockResolvedValueOnce(response(quote)).mockResolvedValueOnce(response([]))
    .mockResolvedValueOnce(response({ id: "staff" })).mockResolvedValueOnce(response({ status: "PENDING" }))
    .mockResolvedValueOnce(response({ ...quote, available_minor: 1766 })).mockResolvedValueOnce(response([]))
    .mockResolvedValueOnce(response({ id: "staff" }));
  vi.stubGlobal("fetch", fetch);
  render(<StaffFinancePage />);
  fireEvent.change(await screen.findByLabelText("Refund in USD"), { target: { value: "12.34" } });
  fireEvent.change(screen.getByLabelText("Audit reason"), { target: { value: "Donor requested partial refund" } });
  fireEvent.click(screen.getByRole("button", { name: "Review financial action" }));
  expect(fetch).toHaveBeenCalledTimes(3);
  expect(screen.getByText("Refund $12.34 USD to the original payment method.")).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Confirm financial action" }));
  await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Stripe refund status: PENDING"));
  expect(JSON.parse(fetch.mock.calls[3][1].body)).toEqual({ amount_minor: 1234, reason: "Donor requested partial refund" });
  expect(await screen.findByText(/Available to refund: \$17.66/)).toBeVisible();
});

it("reuses a persisted uncertain request rather than minting another refund", async () => {
  window.history.replaceState(null, "", "/staff/finance?donor=donor&gift=gift");
  const fetch = vi.fn().mockResolvedValueOnce(response(quote)).mockResolvedValueOnce(response([
    { id: "saved-request", actor_id: "staff", kind: "REFUND", target_id: "gift", amount_minor: 1000,
      reason: "Donor requested refund", status: "AUTHORIZED" },
  ])).mockResolvedValueOnce(response({ id: "staff" })).mockRejectedValue(new Error("Offline"));
  vi.stubGlobal("fetch", fetch);
  render(<StaffFinancePage />);
  fireEvent.click(await screen.findByRole("button", { name: "Retry original financial action" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("not confirmed");
  expect(fetch.mock.calls[3][1].headers["Idempotency-Key"]).toBe("saved-request");
  expect(JSON.parse(fetch.mock.calls[3][1].body).amount_minor).toBe(1000);
});
