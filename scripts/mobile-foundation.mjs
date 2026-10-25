import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const json = (value) => `${JSON.stringify(value, null, 2)}\n`;
export const digest = (value) => createHash("sha256").update(value).digest("hex");

const bearer = (operation) =>
  operation.security?.some((requirement) => "bearerAuth" in requirement || "partnerBearerAuth" in requirement);

export function nativeContract(source) {
  const paths = {};
  for (const [path, item] of Object.entries(source.paths)) {
    const operations = {};
    for (const [method, operation] of Object.entries(item)) {
      if (!["get", "post", "put", "patch", "delete"].includes(method)) continue;
      if (
        operation["x-clients"]?.includes("native") ||
        (method === "get" && (bearer(operation) || path === "/health/live"))
      ) {
        operations[method] = structuredClone(operation);
      }
    }
    if (Object.keys(operations).length) {
      if (item.parameters) operations.parameters = structuredClone(item.parameters);
      paths[path] = operations;
    }
  }
  const components = { securitySchemes: structuredClone(source.components.securitySchemes) };
  const visit = (value) => {
    if (!value || typeof value !== "object") return;
    if (value.$ref) {
      const match = value.$ref.match(/^#\/components\/([^/]+)\/([^/]+)$/);
      if (!match) throw new Error(`Unsupported contract reference: ${value.$ref}`);
      const [, group, name] = match;
      if (!components[group]?.[name]) {
        const target = source.components[group]?.[name];
        if (!target) throw new Error(`Missing contract reference: ${value.$ref}`);
        components[group] ??= {};
        components[group][name] = structuredClone(target);
        visit(target);
      }
    }
    for (const child of Object.values(value)) visit(child);
  };
  visit(paths);
  return {
    openapi: source.openapi,
    info: { ...source.info, title: "DAD donor mobile API" },
    servers: [source.servers[0]],
    paths,
    components,
  };
}

const header = "// Generated from DAD-Main design/tokens.json; do not edit.\n";

const validToken = (group, value) =>
  group === "color"
    ? typeof value === "string" && /^(#[0-9a-f]{3,8}|rgba\([\d.,]+\))$/i.test(value)
    : typeof value === "number" && Number.isFinite(value) && value >= 0;

// Each platform declares one map per token group, string-valued for colors and numeric otherwise.
function tokenMaps(tokens, declare, entry, close) {
  const groups = Object.entries(tokens).map(([group, values]) =>
    [declare(group, group === "color"), ...Object.entries(values).map(([key, value]) => entry(key, value)), close].join(
      "\n",
    ),
  );
  return groups.join("\n");
}

const swiftTokens = (tokens) =>
  `${header}public enum DADTokens {\n${tokenMaps(
    tokens,
    (group, color) => `    public static let ${group}: [String: ${color ? "String" : "Double"}] = [`,
    (key, value) => `        ${JSON.stringify(key)}: ${JSON.stringify(value)},`,
    "    ]",
  )}\n}\n`;

const kotlinNumber = (value) => (Number.isInteger(value) ? `${value}.0` : String(value));

const kotlinTokens = (tokens) =>
  `${header}package org.dad.foundation.generated\n\nobject DADTokens {\n${tokenMaps(
    tokens,
    (group, color) => `    val ${group}: Map<String, ${color ? "String" : "Double"}> = mapOf(`,
    (key, value) =>
      `        ${JSON.stringify(key)} to ${typeof value === "string" ? JSON.stringify(value) : kotlinNumber(value)},`,
    "    )",
  )}\n}\n`;

export function tokenSources(document) {
  if (document.schema_version !== 1) throw new Error("Unsupported design token schema");
  const { schema_version: _, ...tokens } = document;
  for (const group of ["color", "radius", "space", "text"]) {
    if (!tokens[group] || typeof tokens[group] !== "object") throw new Error(`Missing token group: ${group}`);
    if (!Object.values(tokens[group]).every((value) => validToken(group, value))) {
      throw new Error(`Invalid design token in ${group}`);
    }
  }
  return {
    "tokens.ts": `${header}export const tokens = ${JSON.stringify(tokens, null, 2)} as const;\n`,
    "DADTokens.swift": swiftTokens(tokens),
    "DADTokens.kt": kotlinTokens(tokens),
  };
}

export async function generateWeb(check = false) {
  const tokens = tokenSources(JSON.parse(await readFile(resolve(root, "design/tokens.json"), "utf8")));
  const target = resolve(root, "apps/web/design/tokens.ts");
  if (check) {
    if ((await readFile(target, "utf8")) !== tokens["tokens.ts"])
      throw new Error("Web design tokens differ; run make generate-mobile");
  } else await writeFile(target, tokens["tokens.ts"]);
}

export async function bundle(output) {
  const version = JSON.parse(await readFile(resolve(root, "contracts/mobile/version.json"), "utf8")).version;
  if (!/^\d+\.\d+\.\d+$/.test(version)) throw new Error("Invalid mobile foundation version");
  const openapi = await readFile(resolve(root, "apps/donor-api/api/openapi.json"), "utf8");
  const tokens = await readFile(resolve(root, "design/tokens.json"), "utf8");
  const links = await readFile(resolve(root, "contracts/mobile/links.json"), "utf8");
  const files = {
    "openapi.json": openapi,
    "mobile.openapi.json": json(nativeContract(JSON.parse(openapi))),
    "tokens.json": tokens,
    "links.json": links,
    ...tokenSources(JSON.parse(tokens)),
  };
  const revision = execFileSync("git", ["rev-parse", "HEAD"], { cwd: root, encoding: "utf8" }).trim();
  const manifest = {
    version,
    source_repository: "CoachCoe/DAD-Main",
    source_revision: revision,
    files: Object.fromEntries(Object.entries(files).map(([name, contents]) => [name, digest(contents)])),
  };
  await mkdir(output, { recursive: true });
  for (const [name, contents] of Object.entries({ ...files, "manifest.json": json(manifest) }))
    await writeFile(resolve(output, name), contents);
  return manifest;
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  try {
    const [command, output] = process.argv.slice(2);
    if (command === "generate") await generateWeb();
    else if (command === "check") await generateWeb(true);
    else if (command === "bundle" && output) console.log(json(await bundle(resolve(output))));
    else throw new Error("Usage: node scripts/mobile-foundation.mjs {generate|check|bundle OUTPUT}");
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
