import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import Pending from "./Pending";

afterEach(() => vi.unstubAllGlobals());

const application = {
  id: "app-1",
  organization: "Delta Inc",
  website: "https://delta.example",
  type: "Corporate",
  contact_name: "Dana",
  title: "Head of Giving",
  work_email: "dana@delta.example",
  phone: "+14155550123",
  workforce_size: 500,
  status: "PENDING",
  decision_reason: "",
  approved_partner_id: null,
};

it("lists a pending application and approves it", async () => {
  const fetch = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url.endsWith("/pending") && init?.method !== "POST") {
      return Promise.resolve(Response.json({ applications: [application] }));
    }
    return Promise.resolve(Response.json({ partner_id: "p1" }));
  });
  vi.stubGlobal("fetch", fetch);
  render(<Pending />);
  await waitFor(() => expect(screen.getByRole("heading", { name: "Delta Inc" })).toBeInTheDocument());
  fireEvent.click(screen.getByRole("button", { name: "Approve" }));
  await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Approved Delta Inc"));
  expect(fetch.mock.calls.some(([u, i]) => String(u).includes("/app-1/approve") && i?.method === "POST")).toBe(true);
});
