import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import DonatePage from "./page";

afterEach(() => vi.unstubAllGlobals());

describe("sandbox giving", () => {
  it("defaults to monthly, discloses the actual charge, and removes daily framing for one-time", () => {
    render(<DonatePage />);
    expect(screen.getByRole("radio", { name: "Monthly" })).toBeChecked();
    expect(screen.getByRole("button", { name: "$30/month · about $1 a day" })).toBeVisible();
    fireEvent.click(screen.getByRole("radio", { name: "One time" }));
    expect(screen.queryByText(/about \$1 a day/)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "$30 once" })).toBeVisible();
  });

  it("keeps the idempotency key on retry and sends exact integer cents", async () => {
    const fetch = vi.fn().mockRejectedValue(new Error("Connection interrupted"));
    vi.stubGlobal("fetch", fetch);
    render(<DonatePage />);
    fireEvent.change(screen.getByLabelText("Email"), { target: { value: "ben@example.org" } });
    fireEvent.change(screen.getByLabelText("Monthly amount (USD)"), { target: { value: "30.01" } });
    fireEvent.click(screen.getByRole("button", { name: "Continue to test checkout" }));
    await screen.findByRole("alert");
    fireEvent.click(screen.getByRole("button", { name: "Continue to test checkout" }));
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
    expect(fetch.mock.calls[0][1].headers["Idempotency-Key"]).toBe(fetch.mock.calls[1][1].headers["Idempotency-Key"]);
    expect(JSON.parse(fetch.mock.calls[0][1].body).amount_minor).toBe(3001);
  });

  it("does not send fractional cents or payment-card fields", async () => {
    const fetch = vi.fn();
    vi.stubGlobal("fetch", fetch);
    render(<DonatePage />);
    fireEvent.change(screen.getByLabelText("Email"), { target: { value: "ben@example.org" } });
    fireEvent.change(screen.getByLabelText("Monthly amount (USD)"), { target: { value: "1.001" } });
    fireEvent.click(screen.getByRole("button", { name: "Continue to test checkout" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("two decimal places");
    expect(fetch).not.toHaveBeenCalled();
    expect(screen.queryByLabelText(/card number/i)).not.toBeInTheDocument();
  });
});
