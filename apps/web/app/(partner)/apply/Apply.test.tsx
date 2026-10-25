import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import Apply from "./Apply";

afterEach(() => vi.unstubAllGlobals());

function fill() {
  fireEvent.change(screen.getByLabelText("Organization name"), { target: { value: "Delta Inc" } });
  fireEvent.change(screen.getByLabelText("Website"), { target: { value: "delta.example" } });
  fireEvent.change(screen.getByLabelText("Approximate workforce size"), { target: { value: "500" } });
  fireEvent.change(screen.getByLabelText("Your name"), { target: { value: "Dana" } });
  fireEvent.change(screen.getByLabelText("Your title"), { target: { value: "Head of Giving" } });
  fireEvent.change(screen.getByLabelText("Work email"), { target: { value: "dana@delta.example" } });
  fireEvent.change(screen.getByLabelText("Phone"), { target: { value: "+14155550123" } });
  fireEvent.click(screen.getByRole("button", { name: "Submit application" }));
}

it("submits a normalized application and shows the pending-review state", async () => {
  const fetch = vi.fn().mockResolvedValue(Response.json({ received: true }));
  vi.stubGlobal("fetch", fetch);
  render(<Apply />);
  fill();
  await waitFor(() => expect(screen.getByRole("heading", { name: /pending review/i })).toBeInTheDocument());
  const body = JSON.parse(fetch.mock.calls[0][1].body);
  expect(body.website).toBe("https://delta.example");
  expect(body.workforce_size).toBe(500);
  expect(fetch.mock.calls[0][1].headers["Idempotency-Key"]).toBeTruthy();
});

it("surfaces a 422 and re-enables the form for correction", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(Response.json({ detail: "A pending application already exists." }, { status: 422 })),
  );
  render(<Apply />);
  fill();
  await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("pending application already exists"));
  expect(screen.getByLabelText("Organization name")).toBeEnabled();
});
