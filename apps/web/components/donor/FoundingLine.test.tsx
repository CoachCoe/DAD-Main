import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { FoundingLine } from "./FoundingLine";

// Noon UTC is the same New York calendar day year-round, so these dates never straddle midnight.
it("counts the days left and names the deadline", () => {
  render(<FoundingLine now={new Date("2026-12-01T17:00:00Z")} />);
  expect(screen.getByText(/Founding Donor window/)).toHaveTextContent(
    "Founding Donor window: 31 days left · first gift by December 31, 2026",
  );
});

it("says '1 day left' on the last day", () => {
  render(<FoundingLine now={new Date("2026-12-31T17:00:00Z")} />);
  expect(screen.getByText(/Founding Donor window/)).toHaveTextContent("1 day left");
  expect(screen.queryByText(/1 days/)).not.toBeInTheDocument();
});

it("hides the whole line once the window has closed", () => {
  const { container } = render(<FoundingLine now={new Date("2027-01-01T17:00:00Z")} />);
  expect(screen.queryByText(/Founding Donor/)).not.toBeInTheDocument();
  expect(container.querySelector("svg")).toBeNull();
});

it("keeps the star decorative", () => {
  const { container } = render(<FoundingLine now={new Date("2026-12-01T17:00:00Z")} />);
  expect(container.querySelector("svg")).toHaveAttribute("aria-hidden", "true");
});
