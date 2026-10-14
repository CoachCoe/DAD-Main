import { authenticate } from "./auth";
import { LedgerError } from "./ledger";
export { LedgerStore } from "./store";

const LIMIT = 16 * 1024 * 1024;
async function body(request: Request): Promise<unknown> {
	if (!request.headers.get("content-type")?.startsWith("application/json"))
		throw new LedgerError("JSON required", 415);
	if (!request.body) throw new LedgerError("Body required");
	const reader = request.body.getReader();
	const chunks: Uint8Array[] = [];
	let size = 0;
	for (;;) {
		const next = await reader.read();
		if (next.done) break;
		size += next.value.byteLength;
		if (size > LIMIT) {
			await reader.cancel();
			throw new LedgerError("Body too large", 413);
		}
		chunks.push(next.value);
	}
	const bytes = new Uint8Array(size);
	let offset = 0;
	for (const chunk of chunks) {
		bytes.set(chunk, offset);
		offset += chunk.byteLength;
	}
	try {
		return JSON.parse(
			new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(bytes),
		);
	} catch {
		throw new LedgerError("Invalid JSON");
	}
}

export default {
	async fetch(request: Request, env: Env): Promise<Response> {
		const headers = {
			"Cache-Control": "no-store",
			"X-Content-Type-Options": "nosniff",
			"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
		};
		try {
			if (!(await env.INGRESS.limit({ key: "atm-ledger" })).success)
				throw new LedgerError("Ledger ingress rate limit reached", 429);
			const authorization = request.headers.get("authorization");
			if (!authorization?.startsWith("Bearer "))
				throw new LedgerError("Authentication required", 401);
			const principal = await authenticate(authorization.slice(7), env).catch(
				() => {
					throw new LedgerError("Identity is not authorized", 403);
				},
			);
			const url = new URL(request.url);
			const store = env.LEDGERS.getByName(env.SOURCE);
			const call = async (
				operation: Parameters<typeof store.request>[0],
				input: unknown,
			) => {
				const result = await store.request(operation, input, principal);
				if (!result.ok) throw new LedgerError(result.detail, result.status);
				return Response.json(result.value, { headers });
			};
			if (request.method === "GET" && url.pathname === "/v1/ledger")
				return await call("snapshot", null);
			if (
				request.method === "GET" &&
				url.pathname === "/v1/history" &&
				principal.role === "admin"
			)
				return await call(
					"history",
					Number(url.searchParams.get("after") ?? "0"),
				);
			if (request.method === "POST") {
				if (url.pathname === "/v1/publications" && principal.role === "publish")
					return await call("publish", await body(request));
				if (url.pathname === "/v1/triage" && principal.role === "admin")
					return await call("triage", await body(request));
				if (url.pathname === "/v1/migration" && principal.role === "admin")
					return await call("migrate", await body(request));
				if (url.pathname === "/v1/recovery" && principal.role === "admin")
					return await call("recover", await body(request));
			}
			throw new LedgerError("Operation is not authorized", 403);
		} catch (error) {
			if (error instanceof LedgerError)
				return Response.json(
					{ detail: error.message },
					{ status: error.status, headers },
				);
			console.error(
				JSON.stringify({
					event: "ledger_request_failed",
					type: error instanceof Error ? error.name : "Unknown",
				}),
			);
			return Response.json(
				{ detail: "Ledger unavailable" },
				{ status: 503, headers },
			);
		}
	},
} satisfies ExportedHandler<Env>;
