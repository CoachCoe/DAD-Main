// @vitest-environment node
import { NextRequest } from "next/server";
import { afterEach, expect, it, vi } from "vitest";
import { GET } from "./route";

afterEach(() => { vi.unstubAllGlobals(); vi.unstubAllEnvs(); });

it("strips untrusted client address headers at the browser boundary", async () => {
  vi.stubEnv("DONOR_CLIENT_IP_HEADER", "");
  const fetch = vi.fn().mockResolvedValue(Response.json({ ok: true }));
  vi.stubGlobal("fetch", fetch);
  await GET(new NextRequest("http://localhost:3000/api/donor/public/content", {
    headers: { "X-Real-IP": "203.0.113.1", "X-Forwarded-For": "203.0.113.2" },
  }), { params: Promise.resolve({ path: ["donor", "public", "content"] }) });
  const headers = new Headers(fetch.mock.calls[0][1].headers);
  expect(headers.has("x-real-ip")).toBe(false);
  expect(headers.has("x-forwarded-for")).toBe(false);
});

it.each(["203.0.113.1", "2001:db8::1", "203.0.113.1, 203.0.113.2", "unknown"])(
  "forwards only a single IP from the configured ingress header: %s", async (address) => {
    vi.stubEnv("DONOR_CLIENT_IP_HEADER", "x-ingress-client");
    const fetch = vi.fn().mockResolvedValue(Response.json({ ok: true }));
    vi.stubGlobal("fetch", fetch);
    await GET(new NextRequest("http://localhost:3000/api/donor/public/content", {
      headers: { "X-Real-IP": "192.0.2.99", "x-ingress-client": address },
    }), { params: Promise.resolve({ path: ["donor", "public", "content"] }) });
    expect(new Headers(fetch.mock.calls[0][1].headers).get("x-real-ip")).toBe(
      ["203.0.113.1", "2001:db8::1"].includes(address) ? address : null,
    );
  },
);
