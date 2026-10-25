import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import Dashboard from "./Dashboard";

afterEach(() => vi.unstubAllGlobals());

function route(url: string) {
  if (url.endsWith("/overview")) {
    return Response.json({
      organization: "Delta Inc",
      committed_minor: 150000,
      deployed_minor: 50000,
      deployed_percent: 33,
      people_reached: 200,
      our_people_available: false,
    });
  }
  if (url.endsWith("/initiatives")) return Response.json({ initiatives: [] });
  if (url.endsWith("/spend")) return Response.json({ categories: [{ category: "Meals", amount_minor: 50000 }] });
  if (url.endsWith("/users"))
    return Response.json({ users: [{ id: "u1", email: "a@delta.test", name: "Ana", role: "ADMIN" }] });
  if (url.endsWith("/exports")) return Response.json({ exports: [] });
  if (url.endsWith("/our-people"))
    return Response.json({ enrolled: 8, given_minor: 80000, avg_streak: 12, suppressed: false });
  if (url.endsWith("/teams")) return Response.json({ teams: [] });
  return Response.json({});
}

it("renders the overview tiles with committed, deployed %, and verified people", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => Promise.resolve(route(String(input)))),
  );
  render(<Dashboard />);
  await waitFor(() => expect(screen.getByText("$1,500.00")).toBeInTheDocument());
  expect(screen.getByText("33% of committed")).toBeInTheDocument();
  expect(screen.getByText("200")).toBeInTheDocument();
  expect(screen.getByText("Our People enrolled")).toBeInTheDocument();
  expect(screen.getByText("Meals")).toBeInTheDocument();
});
