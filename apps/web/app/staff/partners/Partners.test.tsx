import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import Partners, { matchTerms } from "./Partners";

afterEach(() => vi.unstubAllGlobals());

it("lists approved partners with their terms and links to each", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      Promise.resolve(
        Response.json({
          partners: [
            {
              id: "p-1",
              organization: "Delta Inc",
              type: "Corporate",
              company_code: "DELTAX7K2QP",
              workforce_size: 1200,
              match_rate_bp: 10000,
              match_cap_minor: 5_000_000,
              approved_at: "2026-09-01T12:00:00Z",
              user_count: 2,
              initiative_count: 1,
            },
          ],
          next_offset: null,
        }),
      ),
    ),
  );
  render(<Partners />);
  const link = await screen.findByRole("link", { name: "Delta Inc" });
  expect(link).toHaveAttribute("href", "/staff/partners/p-1");
  expect(screen.getByText(/100% match up to \$50,000 a year · 1 initiative · 2 portal users/)).toBeInTheDocument();
  await waitFor(() => expect(screen.queryByRole("button", { name: "More partners" })).not.toBeInTheDocument());
});

it("describes match terms", () => {
  expect(matchTerms(0, 0)).toBe("No match");
  expect(matchTerms(5000, 0)).toBe("50% match, uncapped");
  expect(matchTerms(12550, 100_000)).toBe("125.5% match up to $1,000 a year");
});
