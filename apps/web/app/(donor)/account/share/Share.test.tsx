import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import CodePage from "../code/Code";
import SharePage from "./Share";

const sharing = {
  public_name: "Sam",
  link: "https://dad.example/r/code",
  irl_link: "https://dad.example/r/code?via=irl",
};
afterEach(() => {
  vi.unstubAllGlobals();
});
function response(data: unknown) {
  return { ok: true, json: async () => data };
}

it("requires a chosen public nickname and keeps QR off the share page", async () => {
  const fetch = vi
    .fn()
    .mockResolvedValueOnce(response({ public_name: null, link: null, irl_link: null }))
    .mockResolvedValueOnce(response(sharing));
  vi.stubGlobal("fetch", fetch);
  render(<SharePage />);
  fireEvent.change(await screen.findByLabelText("Public first name or nickname"), { target: { value: "Sam" } });
  expect(screen.getByText(/anyone who opens your referral link/)).toBeVisible();
  expect(screen.queryByRole("img")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Save public name" }));
  expect(await screen.findByLabelText("Your referral link")).toHaveValue(sharing.link);
  expect(JSON.parse(fetch.mock.calls[1][1].body)).toEqual({ public_name: "Sam", replace_code: false });
});

it("opens native sharing with the Link URL and accurate billing disclosure", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response(sharing)));
  const share = vi.fn().mockResolvedValue(undefined);
  vi.stubGlobal("navigator", { share, onLine: true });
  render(<SharePage />);
  fireEvent.click(await screen.findByRole("button", { name: "Share invitation" }));
  expect(share).toHaveBeenCalledWith(
    expect.objectContaining({ url: sharing.link, text: expect.stringContaining("$30/month") }),
  );
});

it("requires a focused confirmation step before replacing the link, and restores focus on return", async () => {
  const fetch = vi.fn().mockResolvedValue(response(sharing));
  vi.stubGlobal("fetch", fetch);
  render(<SharePage />);
  fireEvent.click(await screen.findByRole("button", { name: "Replace my link and code" }));
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("heading", { name: "Replace your link and code?" })).toHaveFocus();
  expect(screen.getByText(/stop working for new visitors/).closest("[role=alert]")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Keep current link" }));
  expect(screen.getByRole("button", { name: "Replace my link and code" })).toHaveFocus();
  fireEvent.click(screen.getByRole("button", { name: "Replace my link and code" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirm replacement" }));
  await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Your sharing details are saved."));
  expect(JSON.parse(fetch.mock.calls[1][1].body).replace_code).toBe(true);
});

it("shows an on-screen code with brightness instructions and no download control", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response(sharing)));
  render(<CodePage />);
  const image = await screen.findByRole("img");
  expect(image).toHaveAttribute("src", "/api/donor/me/referral-code.svg?view=0");
  expect(screen.getByText(/Increase your screen brightness/)).toBeVisible();
  expect(screen.queryByRole("link", { name: /download/i })).not.toBeInTheDocument();
  fireEvent.error(image);
  const alert = screen.getByText(/code image could not be loaded/);
  expect(alert).toHaveAttribute("role", "alert");
  fireEvent.click(screen.getByRole("button", { name: "Retry code" }));
  expect(await screen.findByRole("img")).toHaveAttribute("src", "/api/donor/me/referral-code.svg?view=1");
});

it("keeps the screen awake while the code is shown and releases it on leaving", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response(sharing)));
  const release = vi.fn().mockResolvedValue(undefined);
  const request = vi.fn().mockResolvedValue({ release });
  Object.defineProperty(navigator, "wakeLock", { value: { request }, configurable: true });
  try {
    const { unmount } = render(<CodePage />);
    await screen.findByRole("img");
    await waitFor(() => expect(request).toHaveBeenCalledWith("screen"));
    unmount();
    await waitFor(() => expect(release).toHaveBeenCalled());
  } finally {
    Reflect.deleteProperty(navigator, "wakeLock");
  }
});

it("sends a signed-out visitor to sign-in instead of showing a code", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue({ ok: false, status: 401, json: async () => ({ detail: "Sign in required" }) }),
  );
  render(<CodePage />);
  expect(await screen.findByRole("link", { name: "Sign in to show your code" })).toHaveAttribute(
    "href",
    "/account/sign-in",
  );
  expect(within(document.body).queryByRole("img")).not.toBeInTheDocument();
});
