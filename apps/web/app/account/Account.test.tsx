import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import ClaimPage from "./claim/Claim";
import SignInPage from "./sign-in/SignIn";
import GivingPage from "./giving/Giving";

afterEach(() => { vi.unstubAllGlobals(); window.history.replaceState(null, "", "/"); });

it("removes the token from the address and requires a click before redeeming", async () => {
  window.history.replaceState(null, "", "/account/claim#token=secret-link");
  const fetch = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ claimed: true }) });
  vi.stubGlobal("fetch", fetch);
  render(<ClaimPage />);
  expect(window.location.hash).toBe("");
  expect(fetch).not.toHaveBeenCalled();
  fireEvent.click(await screen.findByRole("button", { name: "Confirm and sign in" }));
  expect(await screen.findByText("You’re signed in")).toBeVisible();
  expect(fetch.mock.calls[0][1].body).toBe(JSON.stringify({ token: "secret-link" }));
});

it("shows an expired link error and replacement path", async () => {
  window.history.replaceState(null, "", "/account/claim#token=expired-link");
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: false, status: 401,
    json: async () => ({ detail: "Link expired" }) }));
  render(<ClaimPage />);
  fireEvent.click(await screen.findByRole("button", { name: "Confirm and sign in" }));
  await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("Link expired"));
  expect(screen.getByRole("link", { name: "Request a new link" })).toHaveAttribute("href", "/account/sign-in");
});

it("requests email without granting access", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true,
    json: async () => ({ message: "If this email matches a donor account, a link will arrive." }) }));
  render(<SignInPage />);
  fireEvent.change(screen.getByLabelText("Email"), { target: { value: "donor@example.org" } });
  fireEvent.click(screen.getByRole("button", { name: "Email me a sign-in link" }));
  await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("If this email matches"));
  expect(screen.queryByText("My giving")).not.toBeInTheDocument();
});

it("renders protected empty history, and removes it after logout", async () => {
  const fetch = vi.fn().mockResolvedValue({ ok: true,
    json: async () => ({ email: "donor@example.org", gifts: [], next_offset: null }) });
  vi.stubGlobal("fetch", fetch);
  render(<GivingPage />);
  expect(await screen.findByText(/No confirmed test gifts/)).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Sign out" }));
  expect(await screen.findByRole("link", { name: "Sign in to view your giving" })).toBeVisible();
  expect(screen.queryByText("donor@example.org")).not.toBeInTheDocument();
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
});
