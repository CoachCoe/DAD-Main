import { render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import DeliveryHistory from "./Messages";

afterEach(() => vi.unstubAllGlobals());
const reply = (status: number, body: unknown) => ({ ok: status < 400, status, json: async () => body });

it.each([[false, true], [true, false]])("offers a claim email only to an unclaimed donor (claimed: %s)", async (claimed, offered) => {
  vi.stubGlobal("fetch", vi.fn(async (url: string) => url.includes("/messages")
    ? reply(200, { messages: [], next_offset: null }) : reply(200, { id: "donor-1", claimed })));
  render(<DeliveryHistory donor="donor-1" />);
  expect(await screen.findByText("No transactional messages recorded.")).toBeVisible();
  await new Promise((resolve) => setTimeout(resolve, 0));
  expect(screen.queryByText("Request account claim email") !== null).toBe(offered);
});
