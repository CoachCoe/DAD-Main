import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import Partners from "./Partners";

afterEach(() => vi.unstubAllGlobals());

function fill() {
  fireEvent.change(screen.getByLabelText("Organization"), { target: { value: "Lake Basin Water Trust" } });
  fireEvent.change(screen.getByLabelText("Your name"), { target: { value: "Jo" } });
  fireEvent.change(screen.getByLabelText("Email"), { target: { value: "jo@example.org" } });
  fireEvent.change(screen.getByLabelText("Organization website (optional)"), { target: { value: "example.org" } });
  fireEvent.click(screen.getByRole("checkbox"));
  fireEvent.click(screen.getByRole("button", { name: "Submit partner interest" }));
}

it("accepts a bare domain and sends it as an HTTPS address", async () => {
  const fetch = vi.fn().mockResolvedValue(Response.json({ ok: true }));
  vi.stubGlobal("fetch", fetch);
  render(<Partners />);
  fill();
  await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("inquiry was received"));
  await waitFor(() => expect(screen.getByRole("heading", { level: 1 })).toHaveFocus());
  expect(JSON.parse(fetch.mock.calls[0][1].body).website).toBe("https://example.org");
});

it("shows the API's own reason for a rejected form instead of blaming the website", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(Response.json({ detail: "Enter a valid email address." }, { status: 422 })));
  render(<Partners />);
  fill();
  await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("Enter a valid email address."));
  expect(screen.getByRole("alert")).not.toHaveTextContent(/website/i);
  expect(screen.getByLabelText("Organization")).toBeEnabled();
});
