import { env } from "cloudflare:workers";
import { createLocalJWKSet, exportJWK, generateKeyPair, SignJWT } from "jose";
import { describe, expect, it } from "vitest";
import { authenticate, authorize } from "../src/auth";

const claims = {
	repository: "CoachCoe/DAD-Main",
	repository_id: "1378692308",
	repository_owner_id: "17258192",
	runner_environment: "github-hosted",
	actor: "CoachCoe",
	actor_id: "17258192",
	run_id: "123",
	run_attempt: "1",
	sha: "a".repeat(40),
	event_name: "push",
	environment: "atm-ledger-reader",
	workflow_ref: "CoachCoe/DAD-Main/.github/workflows/atm.yml@refs/heads/dev",
};

describe("GitHub OIDC authorization", () => {
	it.each([
		{ repository_id: "1" },
		{ repository_owner_id: "1" },
		{ repository: "attacker/DAD-Main" },
		{ runner_environment: "self-hosted" },
		{ environment: "production" },
		{ event_name: "pull_request_target" },
		{
			workflow_ref:
				"CoachCoe/DAD-Main/.github/workflows/other.yml@refs/heads/dev",
		},
		{
			workflow_ref:
				"CoachCoe/DAD-Main/.github/workflows/atm.yml@refs/heads/untrusted",
		},
	])(
		"rejects identities outside the configured trust boundary: %j",
		(override) => {
			expect(() => authorize({ ...claims, ...override }, env)).toThrow();
		},
	);

	it("separates readers, publishers and owner-only triage", () => {
		expect(authorize(claims, env).role).toBe("read");
		expect(
			authorize({ ...claims, environment: "atm-ledger-publisher" }, env).role,
		).toBe("publish");
		const admin = {
			...claims,
			environment: "atm-ledger-admin",
			event_name: "workflow_dispatch",
		};
		expect(authorize(admin, env).role).toBe("admin");
		expect(() =>
			authorize({ ...admin, actor_id: "9", actor: "attacker" }, env),
		).toThrow();
		expect(() => authorize({ ...admin, event_name: "push" }, env)).toThrow();
	});

	it("requires valid signatures, issuer, audience and expiry", async () => {
		const key = await generateKeyPair("RS256", {
			modulusLength: 2048,
			extractable: true,
		});
		const jwk = {
			...(await exportJWK(key.publicKey)),
			kid: "test",
			alg: "RS256",
		};
		const verify = createLocalJWKSet({ keys: [jwk] });
		const sign = (
			issuer = "https://token.actions.githubusercontent.com",
			audience = "atm-ledger:github:CoachCoe/DAD-Main",
			expires = "2m",
		) =>
			new SignJWT(claims)
				.setProtectedHeader({ alg: "RS256", kid: "test" })
				.setIssuer(issuer)
				.setAudience(audience)
				.setIssuedAt()
				.setNotBefore("0s")
				.setExpirationTime(expires)
				.sign(key.privateKey);
		expect((await authenticate(await sign(), env, verify)).execution).toBe(
			"123:1",
		);
		await expect(
			authenticate(await sign("https://attacker.example"), env, verify),
		).rejects.toThrow();
		await expect(
			authenticate(await sign(undefined, "other-service"), env, verify),
		).rejects.toThrow();
		await expect(
			authenticate(await sign(undefined, undefined, "-1h"), env, verify),
		).rejects.toThrow();
		const valid = await sign();
		const parts = valid.split(".");
		parts[1] = btoa(JSON.stringify({ ...claims, actor_id: "1" }));
		await expect(authenticate(parts.join("."), env, verify)).rejects.toThrow();
	});
});
