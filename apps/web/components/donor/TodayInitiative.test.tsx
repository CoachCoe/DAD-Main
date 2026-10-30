import { render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { TodayInitiative } from "./TodayInitiative";

afterEach(() => vi.unstubAllGlobals());

it("says the initiative is display attribution, not a designated gift (A-5)", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      Promise.resolve(
        Response.json({
          initiative_id: "i-1",
          title: "Reno meals",
          type: "Food",
          location: "Reno, NV",
          started_on: "2026-09-01",
          distinct_shown: 1,
          metrics: [],
        }),
      ),
    ),
  );
  render(<TodayInitiative />);
  expect(await screen.findByRole("heading", { name: "Reno meals" })).toBeInTheDocument();
  expect(screen.getByText("Why this initiative?")).toBeInTheDocument();
  expect(screen.getByText(/none is designated to this initiative/)).toBeInTheDocument();
});
