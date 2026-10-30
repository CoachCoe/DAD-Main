import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import Deletions from "./Deletions";

afterEach(() => vi.unstubAllGlobals());

const deletion = (id: string, kept = 2) => ({ deleted_at: "2026-10-20T12:00:00Z", donor_id: id, retained_gifts: kept });

it("lists erased accounts with the gift records kept and links to each", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => Response.json({ deletions: [deletion("donor-1", 12)], next_cursor: null })),
  );
  render(<Deletions />);
  const table = await screen.findByRole("table");
  expect(within(table).getByRole("link", { name: "donor-1" })).toHaveAttribute("href", "/staff/donors/donor-1");
  expect(within(table).getByText("12")).toBeVisible();
  expect(screen.getByText(/There is no request to approve/)).toBeVisible();
});

it("pages with the server's cursors", async () => {
  const fetch = vi.fn(async (url: string) =>
    Response.json(
      url.includes("cursor=c1")
        ? { deletions: [deletion("older")], next_cursor: null }
        : { deletions: [deletion("newer")], next_cursor: "c1" },
    ),
  );
  vi.stubGlobal("fetch", fetch);
  render(<Deletions />);
  fireEvent.click(await screen.findByRole("button", { name: "Older deletions" }));
  expect(await screen.findByRole("link", { name: "older" })).toBeVisible();
  expect(fetch).toHaveBeenLastCalledWith("/api/donor/staff/deletions?cursor=c1", expect.anything());
  fireEvent.click(screen.getByRole("button", { name: "Newer deletions" }));
  expect(await screen.findByRole("link", { name: "newer" })).toBeVisible();
});

it("says when none have been deleted and when the session is missing", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => Response.json({ deletions: [], next_cursor: null })),
  );
  const { unmount } = render(<Deletions />);
  expect(await screen.findByText("No accounts have been deleted.")).toBeVisible();
  unmount();
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => Response.json({ detail: "Sign in" }, { status: 401 })),
  );
  render(<Deletions />);
  expect(await screen.findByRole("link", { name: "Staff sign-in" })).toBeVisible();
});
