import { render, screen, within } from "@testing-library/react";
import { expect, it } from "vitest";
import { PreviewBanner } from "./PreviewBanner";

it("is a labelled landmark that says test payments only and links to the legal page", () => {
  render(<PreviewBanner />);
  const banner = screen.getByRole("complementary", { name: "Preview notice" });
  expect(banner).toHaveTextContent("Test payments only. No real money moves.");
  expect(within(banner).getByRole("link", { name: "Details" })).toHaveAttribute("href", "/legal");
});
it("keeps the Details link outside the sentence so it is never clipped", () => {
  render(<PreviewBanner />);
  const banner = screen.getByRole("complementary", { name: "Preview notice" });
  expect(banner.querySelector("p")).not.toContainElement(within(banner).getByRole("link", { name: "Details" }));
});
it("does not promise future behaviour", () => {
  render(<PreviewBanner />);
  expect(screen.getByRole("complementary", { name: "Preview notice" })).not.toHaveTextContent(/launch/i);
});
