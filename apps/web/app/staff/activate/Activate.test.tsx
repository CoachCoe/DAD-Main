import { StrictMode } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import StaffActivate from "./Activate";

afterEach(() => {
  vi.unstubAllGlobals();
  sessionStorage.clear();
  window.history.replaceState(null, "", "/");
});

it("retains the private invitation through StrictMode effect replay and clears the URL", async () => {
  window.history.replaceState(null, "", "/staff/activate#token=private-test-invitation");
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ email: "staff@example.org", secret: "TEST-SETUP", uri: "otpauth://totp/test" }), { status: 200 }));
  vi.stubGlobal("fetch", fetch);
  render(<StrictMode><StaffActivate /></StrictMode>);
  expect(window.location.hash).toBe("");
  const begin = screen.getByRole("button", { name: "Begin secure setup" });
  expect(begin).toBeEnabled();
  fireEvent.click(begin);
  expect(await screen.findByText("TEST-SETUP")).toBeVisible();
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({ token: "private-test-invitation" });
});

it("keeps the invitation through a reload until activation, and explains a missing link", async () => {
  window.history.replaceState(null, "", "/staff/activate#token=kept-invitation");
  const first = render(<StaffActivate />);
  expect(await screen.findByRole("button", { name: "Begin secure setup" })).toBeEnabled();
  first.unmount();
  window.history.replaceState(null, "", "/staff/activate");
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ email: "staff@example.org", secret: "S", uri: "otpauth://totp/x" }), { status: 200 }));
  vi.stubGlobal("fetch", fetch);
  const second = render(<StaffActivate />);
  fireEvent.click(await screen.findByRole("button", { name: "Begin secure setup" }));
  await screen.findByText("S");
  expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({ token: "kept-invitation" });
  second.unmount();
  sessionStorage.clear();
  render(<StaffActivate />);
  expect(await screen.findByText(/Open this page from the private invitation link/)).toBeVisible();
  expect(screen.queryByRole("button", { name: "Begin secure setup" })).not.toBeInTheDocument();
});
