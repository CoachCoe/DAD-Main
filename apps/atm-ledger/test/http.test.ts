import { env } from "cloudflare:workers";
import { expect, it } from "vitest";
import worker from "../src/index";

it.each([
	"/v1/ledger",
	"/v1/publications",
	"/v1/triage",
	"/v1/migration",
	"/v1/history",
	"/v1/recovery",
])("requires authentication on %s", async (route) => {
	const response = await worker.fetch(
		new Request(`https://ledger.example.org${route}`, {
			method:
				route.includes("ledger") || route.includes("history") ? "GET" : "POST",
		}),
		env,
	);
	expect(response.status).toBe(401);
	expect(response.headers.get("cache-control")).toBe("no-store");
	expect(await response.json()).toEqual({ detail: "Authentication required" });
});

it("refuses malformed bearer credentials without exposing token contents", async () => {
	const response = await worker.fetch(
		new Request("https://ledger.example.org/v1/ledger", {
			headers: { Authorization: "Bearer private-token-fixture" },
		}),
		env,
	);
	expect(response.status).toBe(403);
	expect(JSON.stringify(await response.json())).not.toContain(
		"private-token-fixture",
	);
});

it("rate limits ingress before authentication or durable storage access", async () => {
	const response = await worker.fetch(
		new Request("https://ledger.example.org/v1/ledger"),
		{ ...env, INGRESS: { limit: async () => ({ success: false }) } },
	);
	expect(response.status).toBe(429);
	expect(await response.json()).toEqual({
		detail: "Ledger ingress rate limit reached",
	});
});
