import { act, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import DeliveryHistory from "./Messages";

afterEach(() => vi.unstubAllGlobals());
const reply = (status: number, body: unknown) => ({ ok: status < 400, status, json: async () => body });

it.each([
  [false, true],
  [true, false],
])("offers a claim email only to an unclaimed donor (claimed: %s)", async (claimed, offered) => {
  let detailsRead = () => {};
  const details = new Promise<void>((resolve) => {
    detailsRead = resolve;
  });
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) =>
      url.includes("/messages")
        ? reply(200, { messages: [], next_offset: null })
        : {
            ok: true,
            status: 200,
            json: async () => {
              detailsRead();
              return { id: "donor-1", claimed };
            },
          },
    ),
  );
  render(<DeliveryHistory donor="donor-1" />);
  expect(await screen.findByText("No transactional messages recorded.")).toBeVisible();
  await details;
  await act(async () => {});
  expect(screen.queryByText("Request account claim email") !== null).toBe(offered);
});
