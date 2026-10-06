import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import Content from "./Content";

afterEach(() => vi.unstubAllGlobals());

it("lists each spreadsheet problem by row and column when publication is rejected", async () => {
  vi.stubGlobal("fetch", vi.fn()
    .mockResolvedValueOnce(Response.json({ sources: [], publications: [] }))
    .mockResolvedValueOnce(Response.json({ detail: "The spreadsheet has problems.", errors: [
      { sheet: "Ledger", row: 3, column: "unit", reason: "Unknown unit DOLLARS" }, { row: 7, column: "source_id", reason: "No stored source" }] }, { status: 422 })));
  render(<Content />);
  // jsdom's File has no text(); browsers do.
  const csv = (name: string) => Object.assign(new File(["header\n"], name, { type: "text/csv" }), { text: async () => "header\n" });
  fireEvent.change(await screen.findByLabelText("Initiatives CSV"), { target: { files: [csv("initiatives.csv")] } });
  fireEvent.change(screen.getByLabelText("Ledger CSV"), { target: { files: [csv("ledger.csv")] } });
  fireEvent.change(screen.getByLabelText("Ledger as-of date"), { target: { value: "2026-09-30" } });
  fireEvent.change(screen.getByLabelText("Update cadence"), { target: { value: "monthly" } });
  fireEvent.click(screen.getByRole("checkbox"));
  // jsdom's required-file validation cannot see files set this way, so submit directly.
  const form = screen.getByRole("button", { name: "Confirm publication" }).closest("form");
  if (!form) throw new Error("Missing publication form");
  fireEvent.submit(form);
  const problems = await screen.findByRole("list", { name: "Spreadsheet problems" });
  expect(within(problems).getAllByRole("listitem").map((item) => item.textContent)).toEqual([
    "Ledger sheet, Row 3, column unitUnknown unit DOLLARS", "Row 7, column source_idNo stored source"]);
  expect(screen.getByRole("alert")).toHaveTextContent("The spreadsheet has problems.");
});
