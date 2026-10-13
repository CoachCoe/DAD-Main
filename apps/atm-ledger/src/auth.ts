import {
	createRemoteJWKSet,
	jwtVerify,
	type JWTVerifyGetKey,
	type JWTPayload,
} from "jose";
import { check, type Principal } from "./ledger";

const githubKeys = createRemoteJWKSet(
	new URL("https://token.actions.githubusercontent.com/.well-known/jwks"),
	{ timeoutDuration: 5000 },
);
type Trust = Pick<
	Env,
	"SOURCE" | "REPOSITORY_ID" | "OWNER_ID" | "ADMIN_ACTOR_IDS"
>;

export function authorize(claims: JWTPayload, env: Trust): Principal {
	const repo = env.SOURCE.replace(/^github:/, "");
	check(
		claims.repository === repo &&
			claims.repository_id === env.REPOSITORY_ID &&
			claims.repository_owner_id === env.OWNER_ID &&
			claims.runner_environment === "github-hosted",
		"Identity is not authorized",
		403,
	);
	check(
		typeof claims.workflow_ref === "string" &&
			typeof claims.actor === "string" &&
			/^[A-Za-z0-9_.@-]{1,100}$/.test(claims.actor) &&
			typeof claims.actor_id === "string" &&
			/^\d+$/.test(claims.actor_id),
		"Invalid workflow identity",
		403,
	);
	check(
		typeof claims.run_id === "string" &&
			/^\d+$/.test(claims.run_id) &&
			typeof claims.run_attempt === "string" &&
			/^\d+$/.test(claims.run_attempt) &&
			typeof claims.sha === "string" &&
			/^[a-f0-9]{40}$/.test(claims.sha),
		"Invalid execution identity",
		403,
	);
	const workflow = `${repo}/.github/workflows/atm.yml@`;
	const admin = `${repo}/.github/workflows/atm.yml@refs/heads/dev`;
	const supportedRef =
		claims.workflow_ref === `${workflow}refs/heads/dev` ||
		claims.workflow_ref === `${workflow}refs/heads/main` ||
		new RegExp(
			`^${workflow.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}refs/pull/[0-9]+/merge$`,
		).test(claims.workflow_ref);
	let role: Principal["role"];
	if (
		claims.workflow_ref === admin &&
		claims.event_name === "workflow_dispatch" &&
		claims.environment === "atm-ledger-admin" &&
		env.ADMIN_ACTOR_IDS.split(",").includes(claims.actor_id)
	)
		role = "admin";
	else if (
		supportedRef &&
		["push", "pull_request", "workflow_dispatch"].includes(
			String(claims.event_name),
		) &&
		claims.environment === "atm-ledger-reader"
	)
		role = "read";
	else if (
		supportedRef &&
		["push", "pull_request", "workflow_dispatch"].includes(
			String(claims.event_name),
		) &&
		claims.environment === "atm-ledger-publisher"
	)
		role = "publish";
	else throw new Error("Identity is not authorized");
	return {
		role,
		actor: claims.actor,
		actorId: claims.actor_id,
		execution: `${claims.run_id}:${claims.run_attempt}`,
		revision: claims.sha,
	};
}

export async function authenticate(
	token: string,
	env: Trust,
	keys: JWTVerifyGetKey = githubKeys,
): Promise<Principal> {
	check(token.length <= 16384, "Invalid credential", 401);
	try {
		const { payload } = await jwtVerify(token, keys, {
			issuer: "https://token.actions.githubusercontent.com",
			audience: `atm-ledger:${env.SOURCE}`,
			algorithms: ["RS256"],
			requiredClaims: ["exp", "iat", "nbf"],
			maxTokenAge: "10m",
			clockTolerance: 10,
		});
		return authorize(payload, env);
	} catch {
		throw new Error("Identity is not authorized");
	}
}
