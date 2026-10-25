import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import Donate from "./Donate";

afterEach(() => {
  vi.unstubAllGlobals();
  sessionStorage.clear();
});

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

  it("frames the gift in dollars a day beside the monthly charge", () => {
    render(<Donate />);
    expect(screen.getByRole("button", { name: "$1 a day · $30/month" })).toBeVisible();
    expect(screen.getByLabelText("Dollars a day")).toHaveValue("1");
  });

  it("charges a custom daily gift as the daily amount × 30", () => {
    render(<Donate />);
    const amount = screen.getByLabelText("Dollars a day");
    fireEvent.change(amount, { target: { value: "7" } });
    expect(screen.getByText(/^You’ll give/)).toHaveTextContent("You’ll give $210/month · $7 a day");
    expect(amount).toHaveAccessibleDescription(/\$210\/month/);
    fireEvent.change(amount, { target: { value: "31" } });
    expect(screen.queryByText(/^You’ll give/)).not.toBeInTheDocument();
  });

  it("marks exactly the chosen preset as pressed", () => {
    render(<Donate />);
    const three = screen.getByRole("button", { name: "$3 a day · $90/month" });
    expect(screen.getByRole("button", { name: "$1 a day · $30/month" })).toHaveAttribute("aria-pressed", "true");
    fireEvent.click(three);
    expect(three).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: "$1 a day · $30/month" })).toHaveAttribute("aria-pressed", "false");
    expect(screen.getByLabelText("Dollars a day")).toHaveValue("3");
  });

  it("keeps the idempotency key on retry and sends the monthly charge in integer cents", async () => {
    const fetch = vi.fn().mockRejectedValue(new TypeError("Failed to fetch"));
    vi.stubGlobal("fetch", fetch);
    render(<Donate />);
    fireEvent.change(screen.getByLabelText("Email"), { target: { value: "ben@example.org" } });
    fireEvent.change(screen.getByLabelText("Dollars a day"), { target: { value: "30" } });
    fireEvent.click(screen.getByRole("button", { name: "Continue to test checkout" }));
    await expectAlert("offline");
    expect(screen.getByRole("alert")).not.toHaveTextContent("Failed to fetch");
    fireEvent.click(screen.getByRole("button", { name: "Continue to test checkout" }));
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
    expect(fetch.mock.calls[0][1].headers["Idempotency-Key"]).toBe(fetch.mock.calls[1][1].headers["Idempotency-Key"]);
    expect(JSON.parse(fetch.mock.calls[0][1].body).amount_minor).toBe(90000);
  });

  it.each(["1.50", "31", "0"])(
    "refuses %s dollars a day without sending it, and ties the error to the amount",
    async (value) => {
      const fetch = vi.fn();
      vi.stubGlobal("fetch", fetch);
      render(<Donate />);
      fireEvent.change(screen.getByLabelText("Email"), { target: { value: "ben@example.org" } });
      const amount = screen.getByLabelText("Dollars a day");
      fireEvent.change(amount, { target: { value } });
      fireEvent.click(screen.getByRole("button", { name: "Continue to test checkout" }));
      await expectAlert("whole number of dollars a day from $1 to $30");
      expect(amount).toHaveAttribute("aria-invalid", "true");
      expect(amount).toHaveAccessibleDescription(/Larger gifts are arranged with DAD directly/);
      expect(amount).toHaveFocus();
      expect(fetch).not.toHaveBeenCalled();
      expect(screen.queryByLabelText(/card number/i)).not.toBeInTheDocument();
      fireEvent.change(amount, { target: { value: "2" } });
      expect(amount).not.toHaveAttribute("aria-invalid");
    },
  );

  it("returns from a cancelled checkout with the same choice, and submits what is shown", async () => {
    const fetch = vi.fn().mockRejectedValueOnce(new TypeError("Failed to fetch"));
    vi.stubGlobal("fetch", fetch);
    const { unmount } = render(<Donate />);
    fireEvent.change(screen.getByLabelText("Dollars a day"), { target: { value: "$10" } });
    fireEvent.change(screen.getByLabelText("Email"), { target: { value: "ben@example.org" } });
    fireEvent.click(screen.getByRole("button", { name: "Continue to test checkout" }));
    await expectAlert("offline");
    unmount();
    const retry = vi
      .fn()
      .mockResolvedValue(new Response(JSON.stringify({ url: "https://evil.example/" }), { status: 200 }));
    vi.stubGlobal("fetch", retry);
    render(<Donate />);
    expect(await screen.findByLabelText("Dollars a day")).toHaveValue("$10");
    expect(screen.getByLabelText("Email")).toHaveValue("ben@example.org");
    fireEvent.click(screen.getByRole("button", { name: "Continue to test checkout" }));
    await waitFor(() => expect(retry).toHaveBeenCalled());
    expect(JSON.parse(retry.mock.calls[0][1].body)).toEqual({
      email: "ben@example.org",
      amount_minor: 30000,
      frequency: "MONTHLY",
    });
  });

  it("clears a stale error as soon as the donor edits the form", async () => {
    vi.stubGlobal("fetch", vi.fn());
    render(<Donate />);
    fireEvent.change(screen.getByLabelText("Email"), { target: { value: "ben@example.org" } });
    fireEvent.change(screen.getByLabelText("Dollars a day"), { target: { value: "0.50" } });
    fireEvent.click(screen.getByRole("button", { name: "Continue to test checkout" }));
    await expectAlert("from $1 to $30");
    fireEvent.click(screen.getByRole("button", { name: "$3 a day · $90/month" }));
    expect(screen.getByRole("alert")).toBeEmptyDOMElement();
  });

  it("promises the Founding Donor badge only for a first gift before January 2027", () => {
    render(<Donate />);
    expect(screen.getByText(/before January 2027 earns a permanent Founding Donor badge/)).toBeVisible();
  });

  it("is usable again when the browser restores it from the back/forward cache mid-redirect", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise(() => {})),
    );
    render(<Donate />);
    fireEvent.change(screen.getByLabelText("Email"), { target: { value: "ben@example.org" } });
    fireEvent.click(screen.getByRole("button", { name: "Continue to test checkout" }));
    expect(await screen.findByRole("button", { name: "Opening secure checkout…" })).toBeDisabled();
    const restored = Object.assign(new Event("pageshow"), { persisted: true });
    act(() => {
      window.dispatchEvent(restored);
    });
    expect(screen.getByRole("button", { name: "Continue to test checkout" })).toBeEnabled();
  });
});
