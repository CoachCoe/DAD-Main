import { afterEach, expect, it, vi } from "vitest";
import { api } from "./api";

afterEach(() => vi.unstubAllGlobals());
const response = () => ({ ok: true, json: async () => ({ ok: true }) });

it("marks JSON request bodies so browsers do not send them as text/plain", async () => {
  const fetch = vi.fn().mockResolvedValue(response());
  vi.stubGlobal("fetch", fetch);
  await api("/donor/example", { method: "POST", body: JSON.stringify({ amount_minor: 3000 }), headers: { "Idempotency-Key": "stable" } });
  const request = fetch.mock.calls[0][1];
  expect(new Headers(request.headers).get("Content-Type")).toBe("application/json");
  expect(new Headers(request.headers).get("Idempotency-Key")).toBe("stable");
  expect(request.credentials).toBe("include");
});

it("preserves explicit content types and Headers instances", async () => {
  const fetch = vi.fn().mockResolvedValue(response());
  vi.stubGlobal("fetch", fetch);
  await api("/example", { method: "POST", body: "some,csv", headers: new Headers({ "Content-Type": "text/csv", "X-Request": "value" }) });
  const headers = new Headers(fetch.mock.calls[0][1].headers);
  expect(headers.get("Content-Type")).toBe("text/csv");
  expect(headers.get("X-Request")).toBe("value");
});

it("lets the browser provide FormData boundaries and Blob content types", async () => {
  const fetch = vi.fn().mockResolvedValue(response());
  vi.stubGlobal("fetch", fetch);
  for (const body of [new FormData(), new Blob(["source document"])]) {
    await api("/example", { method: "POST", body });
  }
  expect(fetch.mock.calls.every(([, request]) => !new Headers(request.headers).has("Content-Type"))).toBe(true);
});
