import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import Wording from "./Wording";

afterEach(() => vi.unstubAllGlobals());

const wording = {
  badges: [
    {
      key: "WT",
      title: "Winning Team",
      text: "Your team finishes a month at #1",
      default_title: "Winning Team",
      default_text: "Your team finishes a month at #1",
      custom: false,
    },
  ],
  milestones: [
    {
      key: "30",
      title: "A month of giving",
      text: "Thirty days of showing up.",
      default_title: "Your 30-day giving streak",
      default_text: "You have kept your giving streak going for 30 days.",
      custom: true,
    },
  ],
};

it("saves a badge's new wording and restores a milestone's default", async () => {
  const fetch = vi
    .fn()
    .mockImplementation(async (_url: string, init?: RequestInit) =>
      init?.method === "POST"
        ? Response.json({ saved: true })
        : Response.json(_url.endsWith("/rules") ? { badges: [], milestones: [7, 30] } : wording),
    );
  vi.stubGlobal("fetch", fetch);
  render(<Wording />);
  const title = (await screen.findAllByLabelText("Title"))[0];
  fireEvent.change(title, { target: { value: " Top Team " } });
  fireEvent.click(screen.getByRole("button", { name: "Save badge WT" }));
  expect(await screen.findByText("Saved the wording for badge WT.")).toBeTruthy();
  const saves = () =>
    fetch.mock.calls.filter(([, init]) => init?.method === "POST").map(([, init]) => JSON.parse(init.body));
  expect(saves()).toEqual([{ kind: "BADGE", key: "WT", title: "Top Team", text: "Your team finishes a month at #1" }]);

  expect(screen.getByText(/Default: Your 30-day giving streak/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Restore default for 30-day milestone" }));
  expect(await screen.findByText("Restored the default wording for 30-day milestone.")).toBeTruthy();
  expect(saves()[1]).toEqual({ kind: "MILESTONE", key: "30", title: "", text: "" });
});
