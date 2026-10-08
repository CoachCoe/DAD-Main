import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import SignIn from "./SignIn";

const options = { challenge: "AQID", rpId: "staff.example.org", userVerification: "required" };

function browser(answer: () => Promise<unknown>) {
  class PublicKeyCredential {
    static parseRequestOptionsFromJSON = (json: unknown) => ({ parsed: json });
    toJSON = () => ({ id: "passkey", type: "public-key" });
  }
  const get = vi.fn(async () => { await answer(); return new PublicKeyCredential(); });
  vi.stubGlobal("PublicKeyCredential", PublicKeyCredential);
  Object.defineProperty(navigator, "credentials", { value: { create: vi.fn(), get }, configurable: true });
  return get;
}

function api(finish: Response) {
  const fetch = vi.fn(async (url: string, _init?: RequestInit) => url === "/api/donor/staff/sign-in/options"
    ? new Response(JSON.stringify({ ceremony: "ceremony-1", options }), { status: 200 }) : finish);
  vi.stubGlobal("fetch", fetch);
  return fetch;
}

afterEach(() => {
  vi.unstubAllGlobals();
  Reflect.deleteProperty(navigator, "credentials");
});

it("signs in with a passkey, without asking for an email or password", async () => {
  const get = browser(async () => {});
  const fetch = api(new Response(JSON.stringify({ signed_in: true }), { status: 200 }));
  render(<SignIn />);
  expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Sign in with a passkey" }));
  await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
  expect(get).toHaveBeenCalledWith({ publicKey: { parsed: options } });
  expect(fetch.mock.calls[1][0]).toBe("/api/donor/staff/sign-in");
  expect(JSON.parse(fetch.mock.calls[1][1]?.body as string)).toEqual({ ceremony: "ceremony-1", credential: { id: "passkey", type: "public-key" } });
  expect(screen.getByRole("alert")).toBeEmptyDOMElement();
});

it("treats a cancelled prompt as a choice, not a failure, and lets the person retry", async () => {
  browser(async () => { throw new DOMException("The operation either timed out or was not allowed.", "NotAllowedError"); });
  const fetch = api(new Response("{}", { status: 500 }));
  render(<SignIn />);
  fireEvent.click(screen.getByRole("button", { name: "Sign in with a passkey" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("Sign-in was cancelled or timed out.");
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("button", { name: "Sign in with a passkey" })).toBeEnabled();
});

it("explains a passkey the server refuses", async () => {
  browser(async () => {});
  api(new Response(JSON.stringify({ detail: "Passkey, invitation, or session invalid or expired" }), { status: 401 }));
  render(<SignIn />);
  fireEvent.click(screen.getByRole("button", { name: "Sign in with a passkey" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("That passkey wasn’t accepted.");
});

it("says when the browser cannot use passkeys", () => {
  render(<SignIn />);
  expect(screen.getByText(/This browser can’t use passkeys/)).toBeVisible();
  expect(screen.queryByRole("button", { name: "Sign in with a passkey" })).not.toBeInTheDocument();
});
