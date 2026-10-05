// @vitest-environment node
import { NextRequest } from "next/server";
import { unstable_doesMiddlewareMatch } from "next/experimental/testing/server";
import { afterEach, describe, expect, it, vi } from "vitest";
import nextConfig from "./next.config";
import { config, proxy } from "./proxy";

afterEach(() => vi.unstubAllEnvs());

function respond(path: string) {
  const response = proxy(new NextRequest(`http://127.0.0.1:3000${path}`));
  const policy = response.headers.get("Content-Security-Policy");
  if (!policy) throw new Error("Missing policy");
  return { response, policy };
}
const directive = (policy: string, name: string) =>
  policy.split(";").map((part) => part.trim()).find((part) => part.startsWith(`${name} `)) ?? "";
const nonceOf = (policy: string) => /'nonce-([^']+)'/.exec(directive(policy, "script-src"))?.[1];

describe("signed-in surface policy", () => {
  it("allows scripts only by nonce, and lets those scripts load the rest", () => {
    const scripts = directive(respond("/account/dashboard").policy, "script-src");
    expect(scripts).toMatch(/^script-src 'self' 'nonce-[A-Za-z0-9+/=]{24,}' 'strict-dynamic'$/);
    expect(scripts).not.toContain("'unsafe-inline'");
    expect(scripts).not.toContain("'unsafe-eval'");
  });

  it("issues a fresh nonce for every request", () => {
    const nonces = new Set(Array.from({ length: 20 }, () => nonceOf(respond("/staff/sign-in").policy)));
    expect(nonces.size).toBe(20);
  });

  it("hands the same policy to rendering, which is where Next reads the nonce", () => {
    const { response, policy } = respond("/staff");
    expect(response.headers.get("x-middleware-request-content-security-policy")).toBe(policy);
  });

  it.each(["default-src 'self'", "connect-src 'self'", "frame-ancestors 'none'", "object-src 'none'", "base-uri 'self'", "form-action 'self'"])(
    "keeps %s", (expected) => {
      expect(respond("/account/dashboard").policy.split("; ")).toContain(expected);
    });

  it("permits eval only for the development server", () => {
    vi.stubEnv("NODE_ENV", "development");
    expect(directive(respond("/account/dashboard").policy, "script-src")).toContain("'unsafe-eval'");
  });

  it("upgrades insecure requests only for an HTTPS origin", () => {
    vi.stubEnv("DONOR_PUBLIC_ORIGIN", "https://donors.example.org");
    expect(respond("/account/dashboard").policy).toContain("upgrade-insecure-requests");
    vi.stubEnv("DONOR_PUBLIC_ORIGIN", "http://127.0.0.1:3000");
    expect(respond("/account/dashboard").policy).not.toContain("upgrade-insecure-requests");
  });
});

describe("proxy scope", () => {
  const sets = (url: string, headers?: Record<string, string>) =>
    unstable_doesMiddlewareMatch({ config, nextConfig, url, headers })
    && proxy(new NextRequest(`http://127.0.0.1:3000${url}`)).headers.has("Content-Security-Policy");

  it.each(["/account", "/account/dashboard", "/account/sign-in", "/staff", "/staff/sign-in", "/staff/donors/abc", "/Staff/sign-in"])(
    "sets the nonce policy on %s", (url) => {
      expect(sets(url)).toBe(true);
    });

  it.each(["/", "/ledger", "/accounts", "/staffing", "/api/donor/me", "/_next/static/chunk.js"])("leaves %s to the static policy", (url) => {
    expect(sets(url)).toBe(false);
  });

  it("skips router prefetches, which are not documents", () => {
    expect(sets("/account/dashboard", { "next-router-prefetch": "1" })).toBe(false);
  });
});
