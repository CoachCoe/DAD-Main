// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { databaseAs } from "./database";

afterEach(() => vi.unstubAllEnvs());

it.each([undefined, "", "invalid", "postgresql://test@localhost/impactgraph", "https://localhost/donor_go"])(
  "refuses unsafe browser test database configuration %s", (url) => {
    vi.stubEnv("DONOR_GO_DATABASE_URL", url);
    expect(() => databaseAs("dad_staff_cli")).toThrow(/Browser tests/);
  });

it.each(["dad_api", "dad_staff_cli"])("runs browser operations with only the %s role", (role) => {
  vi.stubEnv("DONOR_GO_DATABASE_URL", "postgresql://test@localhost:55440/donor_go?sslmode=disable&role=other");
  const env = databaseAs(role);
  const url = new URL(env[`DONOR_${role.replace(/^dad_/, "").toUpperCase()}_DATABASE_URL`]);
  expect(url.pathname).toBe("/donor_go");
  expect(url.searchParams.getAll("role")).toEqual([role]);
  expect(url.searchParams.get("sslmode")).toBe("disable");
  expect(env.DONOR_ALLOW_OWNER_CONNECTION).toBe("true");
});
