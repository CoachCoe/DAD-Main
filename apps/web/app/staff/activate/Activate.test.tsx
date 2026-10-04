import { StrictMode } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import StaffActivate from "./page";

afterEach(() => {
  vi.unstubAllGlobals();
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
