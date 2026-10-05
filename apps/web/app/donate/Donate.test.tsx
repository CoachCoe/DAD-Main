import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import Donate from "./Donate";

afterEach(() => vi.unstubAllGlobals());

async function expectAlert(text: string | RegExp) {
  await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent(text));
}

describe("sandbox giving", () => {
  it.each([
    "http://checkout.stripe.com/session",
    "https://checkout.stripe.com.attacker.example/session",
    "https://attacker@checkout.stripe.com/session",
    "https://checkout.stripe.com:444/session",
  ])("rejects an unsafe checkout destination: %s", async (url) => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ url }), { status: 200 })));
    render(<Donate />);
    fireEvent.change(screen.getByLabelText("Email"), { target: { value: "ben@example.org" } });
    fireEvent.click(screen.getByRole("button", { name: "Continue to test checkout" }));
    await expectAlert("unexpected destination");
    expect(screen.getByRole("button", { name: "Continue to test checkout" })).toBeEnabled();
  });

  it("defaults to monthly, discloses the actual charge, and removes daily framing for one-time", () => {
    render(<Donate />);
    expect(screen.getByRole("radio", { name: "Monthly" })).toBeChecked();
    expect(screen.getByRole("button", { name: "$30/month · about $1 a day" })).toBeVisible();
    fireEvent.click(screen.getByRole("radio", { name: "One time" }));
    expect(screen.queryByText(/about \$1 a day/)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "$30 once" })).toBeVisible();
  });

  it("marks exactly the chosen preset as pressed", () => {
    render(<Donate />);
    const ninety = screen.getByRole("button", { name: "$90/month · about $3 a day" });
    expect(screen.getByRole("button", { name: "$30/month · about $1 a day" })).toHaveAttribute("aria-pressed", "true");
    fireEvent.click(ninety);
    expect(ninety).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: "$30/month · about $1 a day" })).toHaveAttribute("aria-pressed", "false");
    expect(screen.getByLabelText("Monthly amount (USD)")).toHaveValue("90");
  });

  it("keeps the idempotency key on retry and sends exact integer cents", async () => {
    const fetch = vi.fn().mockRejectedValue(new TypeError("Failed to fetch"));
    vi.stubGlobal("fetch", fetch);
    render(<Donate />);
    fireEvent.change(screen.getByLabelText("Email"), { target: { value: "ben@example.org" } });
    fireEvent.change(screen.getByLabelText("Monthly amount (USD)"), { target: { value: "30.01" } });
    fireEvent.click(screen.getByRole("button", { name: "Continue to test checkout" }));
    await expectAlert("offline");
    expect(screen.getByRole("alert")).not.toHaveTextContent("Failed to fetch");
    fireEvent.click(screen.getByRole("button", { name: "Continue to test checkout" }));
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
    expect(fetch.mock.calls[0][1].headers["Idempotency-Key"]).toBe(fetch.mock.calls[1][1].headers["Idempotency-Key"]);
    expect(JSON.parse(fetch.mock.calls[0][1].body).amount_minor).toBe(3001);
  });

  it("does not send fractional cents or payment-card fields, and ties the error to the amount", async () => {
    const fetch = vi.fn();
    vi.stubGlobal("fetch", fetch);
    render(<Donate />);
    fireEvent.change(screen.getByLabelText("Email"), { target: { value: "ben@example.org" } });
    const amount = screen.getByLabelText("Monthly amount (USD)");
    fireEvent.change(amount, { target: { value: "1.001" } });
    fireEvent.click(screen.getByRole("button", { name: "Continue to test checkout" }));
    await expectAlert("two decimal places");
    expect(amount).toHaveAttribute("aria-invalid", "true");
    expect(amount).toHaveAccessibleDescription(/two decimal places/);
    expect(amount).toHaveFocus();
    expect(fetch).not.toHaveBeenCalled();
    expect(screen.queryByLabelText(/card number/i)).not.toBeInTheDocument();
    fireEvent.change(amount, { target: { value: "1.00" } });
    expect(amount).not.toHaveAttribute("aria-invalid");
  });
});
