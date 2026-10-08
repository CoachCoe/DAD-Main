import type { NextConfig } from "next";
import { loadEnvConfig } from "@next/env";
import path from "node:path";
import { contentSecurityPolicy } from "./lib/csp";

loadEnvConfig(path.resolve(process.cwd(), "../.."));

const securityHeaders = [
  { key: "X-Content-Type-Options", value: "nosniff" },
  { key: "X-Frame-Options", value: "DENY" },
  { key: "Referrer-Policy", value: "no-referrer" },
  { key: "Permissions-Policy", value: "camera=(), geolocation=(), microphone=()" },
  // Only meaningful over TLS; harmless in plain-HTTP local development, and forgetting it
  // at deploy time is how a first request stays downgradeable.
  { key: "Strict-Transport-Security", value: "max-age=63072000; includeSubDomains" },
];

const nextConfig: NextConfig = {
  output: "standalone",
  poweredByHeader: false,
  // The static headers below are fixed at build time, so proxy.ts gets the same origins
  // compiled in rather than reading a server environment that could disagree.
  env: {
    DONOR_PUBLIC_ORIGIN: process.env.DONOR_PUBLIC_ORIGIN ?? "",
    DONOR_STAFF_ORIGIN: process.env.DONOR_STAFF_ORIGIN ?? "",
  },
  // The API is proxied by app/api/[...path]/route.ts rather than a rewrite: with
  // output: standalone a rewrite destination is baked in at build time, so a runtime
  // API_BASE_URL was silently ignored.
  async headers() {
    return [
      { source: "/:path*", headers: securityHeaders },
      // proxy.ts sets a nonce policy on these instead. Browsers enforce every CSP header they
      // receive, so this one must not reach those responses as well.
      {
        source: "/((?!(?:account|staff)(?:/|$)).*)",
        headers: [{ key: "Content-Security-Policy", value: contentSecurityPolicy() }],
      },
    ];
  },
};

export default nextConfig;
