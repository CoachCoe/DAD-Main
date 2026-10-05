import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import ResumePage from "./Resume";

const options = { state: "ready", pause_id: "owned-pause", next_renewal: "2026-11-01T12:00:00Z" };
const response = (value: unknown) => ({ ok: true, json: async () => value });
afterEach(() => vi.unstubAllGlobals());

it("requires explicit confirmation and sends the displayed renewal date", async () => {
  const fetch = vi.fn().mockResolvedValueOnce(response(options))
    .mockResolvedValueOnce(response({ ...options, request_id: "command", confirmed: true }));
  vi.stubGlobal("fetch", fetch);
  render(<ResumePage />);
  fireEvent.click(await screen.findByRole("button", { name: "Resume at next renewal" }));
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(screen.getByText(/does not charge you immediately/)).toBeVisible();
  expect(screen.getByRole("heading", { name: "Confirm resumption" })).toHaveFocus();
  fireEvent.click(screen.getByRole("button", { name: "Confirm resume giving" }));
  await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Resumption confirmed for"));
  expect(JSON.parse(fetch.mock.calls[1][1].body)).toEqual({ pause_id: options.pause_id, next_renewal: options.next_renewal });
});

it("retains the same request key when a response is lost", async () => {
  const fetch = vi.fn().mockResolvedValueOnce(response(options)).mockRejectedValue(new Error("Offline"));
  vi.stubGlobal("fetch", fetch);
  render(<ResumePage />);
  fireEvent.click(await screen.findByRole("button", { name: "Resume at next renewal" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirm resume giving" }));
  await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("Resumption is not confirmed"));
  fireEvent.click(screen.getByRole("button", { name: "Confirm resume giving" }));
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(3));
  expect(fetch.mock.calls[1][1].headers["Idempotency-Key"]).toBe(fetch.mock.calls[2][1].headers["Idempotency-Key"]);
  expect(screen.queryByText(/Resumption confirmed for/)).not.toBeInTheDocument();
});

it("can retry a saved request from another browser visit", async () => {
  const fetch = vi.fn().mockResolvedValueOnce(response({ ...options, state: "saved", request_id: "saved-command", confirmed: false }))
    .mockResolvedValueOnce(response({ ...options, request_id: "saved-command", confirmed: true }));
  vi.stubGlobal("fetch", fetch);
  render(<ResumePage />);
  fireEvent.click(await screen.findByRole("button", { name: "Confirm resume giving" }));
  await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Resumption confirmed for"));
  expect(fetch.mock.calls[1][1].headers["Idempotency-Key"]).toBe("saved-command");
});

it("does not offer resumption for an unavailable or cancelled pause", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response({ state: "unavailable" })));
  render(<ResumePage />);
  expect(await screen.findByText(/No pause is currently available to shorten/)).toBeVisible();
  expect(screen.queryByRole("button", { name: "Resume at next renewal" })).not.toBeInTheDocument();
});

it("offers sign-in when the donor session expired", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: false, status: 401, json: async () => ({ detail: "Sign in required" }) }));
  render(<ResumePage />);
  expect(await screen.findByRole("link", { name: "Sign in to manage your giving" })).toHaveAttribute("href", "/account/sign-in");
});

it("returns focus to the opening control when backing out", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response(options)));
  render(<ResumePage />);
  fireEvent.click(await screen.findByRole("button", { name: "Resume at next renewal" }));
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  expect(screen.getByRole("button", { name: "Resume at next renewal" })).toHaveFocus();
});
