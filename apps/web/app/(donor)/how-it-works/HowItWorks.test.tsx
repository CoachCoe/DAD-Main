import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import About from "../about/page";
import HowItWorks from "./page";

it("explains the donor release, monthly billing, direct referral credit, and launch timing", () => {
  render(<HowItWorks />);
  expect(screen.getByText(/22 April 2027/)).toHaveTextContent("Founding Donor badge");
  expect(screen.getByText(/about a dollar a day/)).toHaveTextContent(
    "$30/month, and $90, $150, or an amount you choose",
  );
  expect(screen.getByText(/only people you personally refer/)).toHaveTextContent("less successful refunds");
  expect(screen.getByText(/Successful monthly gifts build your streak/)).toHaveTextContent("7 days to recover");
  expect(screen.getByText(/Successful monthly gifts build your streak/)).toHaveTextContent(
    "Cancellation ends it at the first skipped renewal",
  );
  expect(screen.getByText(/does not enrol recipients/)).toHaveTextContent("Aid programs begin at launch");
  expect(screen.getByText(/does not enrol recipients/)).toHaveTextContent("donor app arrives for iOS and Android");
  expect(screen.queryByText(/R2/)).not.toBeInTheDocument();
  expect(screen.getByText(/approved by the board/)).toHaveTextContent("From launch, programs invite");
  expect(screen.getByText(/No real donation is collected/)).toBeInTheDocument();
});

it("links the explanation to giving, source-cited figures, and partner interest", () => {
  render(<HowItWorks />);
  expect(screen.getByRole("link", { name: "Try giving" })).toHaveAttribute("href", "/donate");
  expect(screen.getByRole("link", { name: "Read the impact ledger" })).toHaveAttribute("href", "/ledger");
  expect(screen.getByRole("link", { name: "Partner application" })).toHaveAttribute("href", "/apply");
  expect(screen.getByText(/reported figures cite a source document/)).toHaveTextContent(
    "illustrative figures are labeled",
  );
});

it("keeps /about as a permanent alias of the single About / how it works screen", () => {
  expect(() => About()).toThrow("NEXT_REDIRECT");
});
