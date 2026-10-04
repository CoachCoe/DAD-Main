import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import About from "./page";

it("explains the donor release, monthly billing, direct referral credit, and launch timing", () => {
  render(<About />);
  expect(screen.getByText(/22 April 2027/)).toHaveTextContent("$30/month");
  expect(screen.getByText(/only people you personally refer/)).toHaveTextContent("less successful refunds");
  expect(screen.getByText(/does not enrol recipients/)).toHaveTextContent("Aid programs begin at launch");
  expect(screen.getByText(/No real donation is collected/)).toBeInTheDocument();
});

it("links the donor explanation to giving, source-backed figures, and partner interest", () => {
  render(<About />);
  expect(screen.getByRole("link", { name: "Try giving" })).toHaveAttribute("href", "/donate");
  expect(screen.getByRole("link", { name: "Read the impact ledger" })).toHaveAttribute("href", "/ledger");
  expect(screen.getByRole("link", { name: "Partner interest form" })).toHaveAttribute("href", "/partners");
  expect(screen.getByText(/source documents support reported numbers/)).toHaveTextContent("illustrative figures are labeled");
});
