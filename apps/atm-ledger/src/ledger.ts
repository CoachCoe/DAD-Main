import { createHash } from "node:crypto";

export class LedgerError extends Error {
	readonly status: number;
	constructor(message: string, status = 400) {
		super(message);
		this.status = status;
	}
}

const severities = ["unassessed", "low", "medium", "high", "critical"] as const;
// Triage chooses any disposition but "reopened", which only a publication sets.
const triaged = ["untriaged", "confirmed", "resolved", "dismissed"] as const;
const dispositions = [...triaged, "reopened"] as const;
const closed: readonly string[] = ["resolved", "dismissed"];
type Severity = (typeof severities)[number];
type Disposition = (typeof dispositions)[number];
interface Audit {
	at: string;
	actor: string;
	severity: Severity;
	disposition: (typeof triaged)[number];
	noteDigest: string;
}
export interface Finding {
	id: string;
	source: string;
	check: string;
	severity: Severity;
	disposition: Disposition;
	firstSeen: string;
	lastSeen: string;
	lastRunId: string;
	occurrences: number;
	updatedAt?: string;
	audit: Audit[];
}
interface Receipt {
	execution: string;
	runId: string;
	revision: string;
	bundleSha256: string;
	artifactId: string;
	artifactDigest: string;
	eligible: boolean;
}
export interface Ledger {
	schemaVersion: 1;
	source: string;
	sequence: number;
	findings: Finding[];
	receipts: Receipt[];
}
export interface Principal {
	role: "read" | "publish" | "admin";
	actor: string;
	actorId: string;
	execution: string;
	revision: string;
}
export interface Snapshot {
	version: number;
	digest: string;
	historyHash: string;
	ledger: Ledger;
}
export interface Delta {
	findings: Finding[];
	receipts: Receipt[];
}
export interface MigrationEntry {
	commit: string;
	parent: string | null;
	at: string;
	ledger: Ledger;
}

export function check(
	condition: unknown,
	message: string,
	status = 400,
): asserts condition {
	if (!condition) throw new LedgerError(message, status);
}
export function object(
	value: unknown,
	keys: readonly string[],
): asserts value is Record<string, unknown> {
	check(
		value !== null && typeof value === "object" && !Array.isArray(value),
		"Expected an object",
	);
	check(
		Object.keys(value).every((k) => keys.includes(k)),
		"Unexpected fields",
	);
}
export const hex = /^[a-f0-9]{64}$/;
export const sha = /^[a-f0-9]{40}$/;
export const actorName = /^[A-Za-z0-9_.@-]{1,100}$/;
const findingId = /^[a-f0-9]{24}$/;
const run = /^atm-[0-9]{14}-[a-f0-9]{8}$/;
const oneOf = <T extends string>(v: unknown, values: readonly T[]): v is T =>
	typeof v === "string" && (values as readonly string[]).includes(v);
const date = (v: unknown) =>
	typeof v === "string" && Number.isFinite(Date.parse(v));
const matches = (v: unknown, pattern: RegExp) =>
	typeof v === "string" && pattern.test(v);

export function canonical(value: unknown): string {
	if (Array.isArray(value)) return `[${value.map(canonical).join(",")}]`;
	if (value !== null && typeof value === "object") {
		return `{${Object.entries(value)
			.sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
			.map(([k, v]) => `${JSON.stringify(k)}:${canonical(v)}`)
			.join(",")}}`;
	}
	const encoded = JSON.stringify(value);
	check(encoded !== undefined, "Unsupported value");
	return encoded;
}
export const digest = (value: unknown) =>
	createHash("sha256").update(canonical(value)).digest("hex");
export const emptyLedger = (source: string): Ledger => ({
	schemaVersion: 1,
	source,
	sequence: 0,
	findings: [],
	receipts: [],
});

export function validateLedger(
	value: unknown,
	source: string,
): asserts value is Ledger {
	object(value, [
		"schemaVersion",
		"source",
		"sequence",
		"findings",
		"receipts",
	]);
	check(
		value.schemaVersion === 1 &&
			value.source === source &&
			/^github:[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(source),
		"Invalid ledger identity",
	);
	check(
		typeof value.sequence === "number" &&
			Number.isSafeInteger(value.sequence) &&
			value.sequence >= 0 &&
			Array.isArray(value.receipts) &&
			value.sequence === value.receipts.length &&
			Array.isArray(value.findings),
		"Invalid ledger sequence",
	);
	const ids = new Set();
	for (const f of value.findings) {
		object(f, [
			"id",
			"source",
			"check",
			"severity",
			"disposition",
			"firstSeen",
			"lastSeen",
			"lastRunId",
			"occurrences",
			"updatedAt",
			"audit",
		]);
		check(
			matches(f.id, findingId) &&
				!ids.has(f.id) &&
				f.source === source &&
				matches(f.check, /^[a-z-]+$/),
			"Invalid finding identity",
		);
		check(
			oneOf(f.severity, severities) && oneOf(f.disposition, dispositions),
			"Invalid finding classification",
		);
		check(
			date(f.firstSeen) &&
				date(f.lastSeen) &&
				matches(f.lastRunId, run) &&
				typeof f.occurrences === "number" &&
				Number.isSafeInteger(f.occurrences) &&
				f.occurrences > 0 &&
				Array.isArray(f.audit),
			"Invalid finding observation",
		);
		check(
			f.updatedAt === undefined || date(f.updatedAt),
			"Invalid triage time",
		);
		check(
			!closed.includes(f.disposition) || (f.updatedAt && f.audit.length),
			"Closure requires an audit record",
		);
		for (const a of f.audit) {
			object(a, ["at", "actor", "disposition", "severity", "noteDigest"]);
			check(
				date(a.at) &&
					matches(a.actor, actorName) &&
					oneOf(a.disposition, triaged) &&
					oneOf(a.severity, severities) &&
					matches(a.noteDigest, hex),
				"Invalid triage audit",
			);
		}
		ids.add(f.id);
	}
	const executions = new Set();
	for (const r of value.receipts) {
		object(r, [
			"execution",
			"runId",
			"revision",
			"bundleSha256",
			"artifactId",
			"artifactDigest",
			"eligible",
		]);
		check(
			matches(r.execution, /^\d+:\d+$/) &&
				!executions.has(r.execution) &&
				matches(r.runId, run) &&
				matches(r.revision, sha) &&
				matches(r.bundleSha256, hex) &&
				matches(r.artifactId, /^\d+$/) &&
				matches(r.artifactDigest, hex) &&
				typeof r.eligible === "boolean",
			"Invalid receipt",
		);
		executions.add(r.execution);
	}
}

export function publication(
	prior: Ledger,
	next: Ledger,
	execution?: string,
): Delta {
	validateLedger(next, prior.source);
	check(
		next.sequence === prior.sequence + 1 &&
			digest(next.receipts.slice(0, -1)) === digest(prior.receipts),
		"Publication cannot replace receipts",
	);
	const receipt = next.receipts.at(-1);
	check(
		receipt && (!execution || receipt.execution === execution),
		"Execution does not match identity",
	);
	const changes: Finding[] = [];
	for (const f of prior.findings) {
		const n = next.findings.find((item) => item.id === f.id);
		check(
			n &&
				n.source === f.source &&
				n.check === f.check &&
				n.firstSeen === f.firstSeen &&
				n.severity === f.severity &&
				digest(n.audit) === digest(f.audit) &&
				n.updatedAt === f.updatedAt,
			"Publisher cannot change triage or discard findings",
		);
		check(
			n.lastSeen >= f.lastSeen &&
				n.occurrences >= f.occurrences &&
				(n.disposition === f.disposition ||
					(closed.includes(f.disposition) && n.disposition === "reopened")),
			"Invalid finding transition",
		);
		if (digest(n) !== digest(f)) changes.push(n);
	}
	for (const n of next.findings.filter(
		(f) => !prior.findings.some((p) => p.id === f.id),
	)) {
		check(
			n.severity === "unassessed" &&
				n.disposition === "untriaged" &&
				n.audit.length === 0 &&
				n.updatedAt === undefined,
			"New findings must be unassessed",
		);
		changes.push(n);
	}
	check(
		!receipt.eligible ||
			next.findings.every(
				(f) =>
					closed.includes(f.disposition) ||
					!["unassessed", "high", "critical"].includes(f.severity),
			),
		"Open findings block acceptance",
	);
	const delta = { findings: changes, receipts: [receipt] };
	check(
		digest(applyDelta(prior, delta)) === digest(next),
		"Publication changes finding order",
	);
	return delta;
}

export function triage(
	prior: Ledger,
	body: unknown,
	actor: string,
	at: string,
): { ledger: Ledger; delta: Delta } {
	object(body, ["finding", "severity", "disposition", "noteDigest"]);
	check(
		matches(body.finding, findingId) &&
			oneOf(body.severity, severities) &&
			oneOf(body.disposition, triaged) &&
			matches(body.noteDigest, hex),
		"Invalid triage request",
	);
	const ledger = structuredClone(prior);
	const finding = ledger.findings.find((f) => f.id === body.finding);
	check(finding, "Finding not found", 404);
	const audit = {
		at,
		actor,
		severity: body.severity,
		disposition: body.disposition,
		noteDigest: body.noteDigest,
	};
	const proposed: unknown = {
		...finding,
		severity: body.severity,
		disposition: body.disposition,
		updatedAt: at,
		audit: [...finding.audit, audit],
	};
	const candidate: unknown = {
		...ledger,
		findings: ledger.findings.map((f) => (f.id === finding.id ? proposed : f)),
	};
	validateLedger(candidate, prior.source);
	return {
		ledger: candidate,
		delta: {
			findings: candidate.findings.filter((f) => f.id === finding.id),
			receipts: [],
		},
	};
}

export function applyDelta(prior: Ledger, delta: Delta): Ledger {
	const next = structuredClone(prior);
	for (const finding of delta.findings) {
		const i = next.findings.findIndex((f) => f.id === finding.id);
		if (i === -1) next.findings.push(finding);
		else next.findings[i] = finding;
	}
	next.receipts.push(...delta.receipts);
	next.sequence = next.receipts.length;
	validateLedger(next, prior.source);
	return next;
}
