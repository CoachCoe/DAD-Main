import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import Employer from "./Employer";

afterEach(() => vi.unstubAllGlobals());

it("enrolls an unlinked donor with a company code", async () => {
  const fetch = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url.endsWith("/donor/me/team") && init?.method !== "POST") {
      return Promise.resolve(Response.json({ partner_id: "", organization: "" }));
    }
    return Promise.resolve(Response.json({ organization: "Delta Inc" }));
  });
  vi.stubGlobal("fetch", fetch);
  render(<Employer />);
  await waitFor(() => expect(screen.getByLabelText("Company code")).toBeInTheDocument());
  fireEvent.change(screen.getByLabelText("Company code"), { target: { value: "DELTA" } });
  fireEvent.click(screen.getByRole("button", { name: "Link my employer" }));
  await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("linked to Delta Inc"));
  expect(fetch.mock.calls.some(([u, i]) => String(u).endsWith("/donor/me/employer") && i?.method === "POST")).toBe(
    true,
  );
});

it("shows the linked employer and team", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      Promise.resolve(Response.json({ partner_id: "p1", organization: "Delta Inc", team_name: "Engineering" })),
    ),
  );
  render(<Employer />);
  await waitFor(() => expect(screen.getByText(/Engineering/)).toBeInTheDocument());
  expect(screen.getByRole("button", { name: "Change team" })).toBeInTheDocument();
});
