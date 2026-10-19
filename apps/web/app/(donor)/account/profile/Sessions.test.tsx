import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { Sessions } from "./Sessions";

afterEach(() => vi.unstubAllGlobals());

const browser = {
  id: "b1",
  client: "BROWSER",
  platform: null,
  label: "Web browser",
  created_at: "2026-10-01T12:00:00Z",
  last_used_at: "2026-10-08T12:00:00Z",
  current: true,
};
const phone = {
  id: "p1",
  client: "APP",
  platform: "IOS",
  label: "Alex’s iPhone",
  created_at: "2026-09-20T12:00:00Z",
  last_used_at: "2026-10-07T09:30:00Z",
  current: false,
};
const tablet = {
  id: "t1",
  client: "APP",
  platform: null,
  label: "Mobile device",
  created_at: "2026-09-02T12:00:00Z",
  last_used_at: "2026-10-02T09:30:00Z",
  current: false,
};

const json = (body: unknown, status = 200) => Promise.resolve({ ok: status < 400, status, json: async () => body });

function serve(routes: Record<string, () => Promise<unknown>>) {
  const fetch = vi.fn((url: string, init?: RequestInit) => routes[`${init?.method ?? "GET"} ${url}`]());
  vi.stubGlobal("fetch", fetch);
  return fetch;
}

it("lists each session with its kind and last use, marking this device", async () => {
  serve({ "GET /api/donor/me/sessions": () => json({ sessions: [browser, phone, tablet] }) });
  render(<Sessions onSignedOut={vi.fn()} />);
  expect(screen.getByRole("status")).toHaveTextContent("Loading where you’re signed in…");
  const rows = await screen.findAllByRole("listitem");
  expect(rows[0]).toHaveTextContent(/^Web browserThis deviceSigned in Oct 1, 2026 · Active now$/);
  expect(within(rows[0]).queryByRole("button")).not.toBeInTheDocument();
  expect(rows[1]).toHaveTextContent("Alex’s iPhone");
  expect(rows[1]).toHaveTextContent(/iOS app · Signed in Sep 20, 2026 · Last active Oct 7, 2026/);
  expect(rows[2]).toHaveTextContent(/DAD app · Signed in Sep 2, 2026/);
  expect(within(rows[1]).getByRole("button")).toHaveAccessibleName(/^Sign out Alex’s iPhone, last active Oct 7, 2026/);
  expect(screen.getByRole("status")).toBeEmptyDOMElement();
});

it("signs out one other session and keeps keyboard focus in the list", async () => {
  const fetch = serve({
    "GET /api/donor/me/sessions": () => json({ sessions: [browser, phone, tablet] }),
    "POST /api/donor/me/sessions/p1/revoke": () => json({ sessions: [browser, tablet] }),
    "POST /api/donor/me/sessions/t1/revoke": () => json({ sessions: [browser] }),
  });
  render(<Sessions onSignedOut={vi.fn()} />);
  fireEvent.click(await screen.findByRole("button", { name: /^Sign out Alex’s iPhone/ }));
  await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Signed out of Alex’s iPhone."));
  expect(screen.queryByText("Alex’s iPhone")).not.toBeInTheDocument();
  await waitFor(() => expect(screen.getByRole("button", { name: /^Sign out Mobile device/ })).toHaveFocus());
  fireEvent.click(screen.getByRole("button", { name: /^Sign out Mobile device/ }));
  await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Signed out of Mobile device."));
  await waitFor(() => expect(screen.getByRole("heading", { name: "Where you’re signed in" })).toHaveFocus());
  expect(screen.getByText("You’re not signed in anywhere else.")).toBeVisible();
  expect(fetch).toHaveBeenCalledWith(
    "/api/donor/me/sessions/p1/revoke",
    expect.objectContaining({ method: "POST", credentials: "include" }),
  );
});

it("signs out every other device at once", async () => {
  serve({
    "GET /api/donor/me/sessions": () => json({ sessions: [browser, phone, tablet] }),
    "POST /api/donor/me/sessions/revoke-others": () => json({ sessions: [browser] }),
  });
  render(<Sessions onSignedOut={vi.fn()} />);
  fireEvent.click(await screen.findByRole("button", { name: "Sign out of all other devices" }));
  await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Signed out of every other device."));
  expect(screen.getAllByRole("listitem")).toHaveLength(1);
  expect(screen.queryByRole("button", { name: "Sign out of all other devices" })).not.toBeInTheDocument();
  await waitFor(() => expect(screen.getByRole("heading", { name: "Where you’re signed in" })).toHaveFocus());
});

it("keeps the list and offers a refresh when a sign-out is not confirmed", async () => {
  let listed = 0;
  serve({
    "GET /api/donor/me/sessions": () => {
      listed += 1;
      return json({ sessions: [browser, phone] });
    },
    "POST /api/donor/me/sessions/p1/revoke": () => json({ detail: "Donor account is temporarily unavailable" }, 503),
  });
  render(<Sessions onSignedOut={vi.fn()} />);
  fireEvent.click(await screen.findByRole("button", { name: /^Sign out Alex’s iPhone/ }));
  await waitFor(() =>
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Sign-out was not confirmed. Refresh the list to check before retrying.",
    ),
  );
  expect(screen.getByText("Alex’s iPhone")).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Refresh signed-in devices" }));
  await waitFor(() => expect(listed).toBe(2));
  await waitFor(() => expect(screen.getByRole("alert")).toBeEmptyDOMElement());
});

it("explains a list that could not load, and hands an ended session back to the page", async () => {
  serve({ "GET /api/donor/me/sessions": () => json({ detail: "Unavailable" }, 503) });
  const { unmount } = render(<Sessions onSignedOut={vi.fn()} />);
  await waitFor(() =>
    expect(screen.getByRole("alert")).toHaveTextContent("Your signed-in devices could not be loaded right now."),
  );
  expect(screen.getByRole("button", { name: "Refresh signed-in devices" })).toBeVisible();
  unmount();

  serve({ "GET /api/donor/me/sessions": () => json({ detail: "Sign in required" }, 401) });
  const onSignedOut = vi.fn();
  render(<Sessions onSignedOut={onSignedOut} />);
  await waitFor(() => expect(onSignedOut).toHaveBeenCalled());
});
