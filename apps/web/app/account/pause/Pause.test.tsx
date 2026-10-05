import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import PausePage from "./Pause";

const dates = { months: 2, starts_at: "2026-11-01T12:00:00Z", resumes_at: "2027-01-01T12:00:00Z" };
const options = { state: "ready", subscription_id: "owned", pause: null, options: [dates] };
const response = (value: unknown) => ({ ok: true, json: async () => value });
afterEach(() => vi.unstubAllGlobals());

it("requires confirmation of the exact skipped and resumed renewal dates", async () => {
  const fetch = vi.fn().mockResolvedValueOnce(response(options))
    .mockResolvedValueOnce(response({ pause: { ...dates, request_id: "command", confirmed: true } }));
  vi.stubGlobal("fetch", fetch);
  render(<PausePage />);
  fireEvent.click(await screen.findByRole("button", { name: "Skip 2 renewals" }));
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("heading", { name: "Confirm your pause" })).toHaveFocus();
  expect(screen.getByText(/Giving automatically resumes/)).toBeVisible();
  expect(screen.getByText(/streak ends at the first skipped renewal/)).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Confirm pause" }));
  await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Pause confirmed."));
  expect(JSON.parse(fetch.mock.calls[1][1].body)).toEqual({ subscription_id: "owned", ...dates });
});

it("retries a lost response with the same key and never claims success", async () => {
  const fetch = vi.fn().mockResolvedValueOnce(response(options)).mockRejectedValue(new Error("Offline"));
  vi.stubGlobal("fetch", fetch);
  render(<PausePage />);
  fireEvent.click(await screen.findByRole("button", { name: "Skip 2 renewals" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirm pause" }));
  await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("Pause is not confirmed"));
  expect(screen.queryByRole("button", { name: "Back" })).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "Confirm pause" }));
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(3));
  expect(fetch.mock.calls[1][1].headers["Idempotency-Key"]).toBe(fetch.mock.calls[2][1].headers["Idempotency-Key"]);
  expect(screen.queryByText("Pause confirmed.")).not.toBeInTheDocument();
});

it("can retry a saved pending pause after a browser refresh", async () => {
  const fetch = vi.fn().mockResolvedValueOnce(response({ ...options, options: [],
    pause: { ...dates, request_id: "saved-command", confirmed: false } }))
    .mockResolvedValueOnce(response({ pause: { ...dates, request_id: "saved-command", confirmed: true } }));
  vi.stubGlobal("fetch", fetch);
  render(<PausePage />);
  fireEvent.click(await screen.findByRole("button", { name: "Retry saved pause" }));
  await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Pause confirmed."));
  expect(fetch.mock.calls[1][1].headers["Idempotency-Key"]).toBe("saved-command");
});

it("requires sign-in and does not offer controls to an anonymous visitor", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: false, status: 401, json: async () => ({ detail: "Sign in required" }) }));
  render(<PausePage />);
  expect(await screen.findByRole("link", { name: "Sign in to manage your giving" })).toHaveAttribute("href", "/account/sign-in");
  expect(screen.queryByRole("button", { name: "Confirm pause" })).not.toBeInTheDocument();
});

it("returns focus to the chosen option when backing out", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response(options)));
  render(<PausePage />);
  fireEvent.click(await screen.findByRole("button", { name: "Skip 2 renewals" }));
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  expect(screen.getByRole("button", { name: "Skip 2 renewals" })).toHaveFocus();
});
