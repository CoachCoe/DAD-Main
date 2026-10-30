import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import Content from "./Content";

afterEach(() => vi.unstubAllGlobals());

const source = { id: "8f9e0d1c-2b3a-4c5d-8e6f-7a8b9c0d1e2f", name: "approval.pdf", sha256: "a".repeat(64) };

// jsdom's required-file validation cannot see files set this way, so submit the form directly.
function submitUpload() {
  const form = screen.getByRole("button", { name: "Upload private source" }).closest("form");
  if (!form) throw new Error("Missing upload form");
  fireEvent.submit(form);
}

it("lists stored source documents and offers no spreadsheet publication", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce(Response.json({ sources: [source] })));
  render(<Content />);
  expect(await screen.findByRole("link", { name: "approval.pdf" })).toHaveAttribute(
    "href",
    `/api/donor/staff/content/sources/${source.id}`,
  );
  expect(screen.queryByLabelText("Ledger CSV")).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Confirm publication" })).not.toBeInTheDocument();
});

it("stores an uploaded source and reports its ID for citing as a board approval", async () => {
  const fetch = vi
    .fn()
    .mockResolvedValueOnce(Response.json({ sources: [] }))
    .mockResolvedValueOnce(Response.json(source))
    .mockResolvedValueOnce(Response.json({ sources: [source] }));
  vi.stubGlobal("fetch", fetch);
  render(<Content />);
  fireEvent.change(await screen.findByLabelText("Source file"), {
    target: { files: [new File(["signed"], "approval.pdf")] },
  });
  submitUpload();
  expect(
    await screen.findByText(`Source stored: ${source.id}. Cite this ID as an initiative's board approval.`),
  ).toBeVisible();
  expect(fetch.mock.calls[1][0]).toContain("/donor/staff/content/sources?name=approval.pdf");
});

it("explains a rejected upload for a role without permission", async () => {
  vi.stubGlobal(
    "fetch",
    vi
      .fn()
      .mockResolvedValueOnce(Response.json({ sources: [] }))
      .mockResolvedValueOnce(Response.json({ detail: "Forbidden" }, { status: 403 })),
  );
  render(<Content />);
  fireEvent.change(await screen.findByLabelText("Source file"), {
    target: { files: [new File(["signed"], "approval.pdf")] },
  });
  submitUpload();
  expect(await screen.findByRole("alert")).toHaveTextContent("does not permit storing source documents");
});
