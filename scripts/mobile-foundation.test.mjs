import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { nativeContract, tokenSources } from "./mobile-foundation.mjs";

const original = JSON.parse(await readFile(new URL("../apps/donor-api/api/openapi.json", import.meta.url), "utf8"));

test("mobile contract preserves native operations and excludes privileged and browser-only writes", () => {
  const mobile = nativeContract(original);
  for (const path of [
    "/donor/claim",
    "/donor/refresh",
    "/donor/me/profile",
    "/donor/me/dashboard",
    "/donor/me/push-device",
  ]) {
    assert.deepEqual(mobile.paths[path], original.paths[path]);
  }
  for (const path of Object.keys(mobile.paths)) {
    assert.ok(
      !path.startsWith("/donor/staff/") && !path.startsWith("/donor/checkouts") && !path.includes("stripe/webhook"),
      path,
    );
  }
  assert.equal(mobile.components.schemas.StaffView, undefined);
  assert.deepEqual(mobile.components.schemas.Profile, original.components.schemas.Profile);
  assert.deepEqual(mobile.components.schemas.NativeSession, original.components.schemas.NativeSession);
});

test("every mobile reference resolves, including transitive response schemas", () => {
  const mobile = nativeContract(original);
  let count = 0;
  const visit = (value) => {
    if (!value || typeof value !== "object") return;
    if (value.$ref) {
      const parts = value.$ref.slice(2).split("/");
      assert.ok(
        parts.reduce((node, part) => node?.[part], mobile),
        value.$ref,
      );
      count++;
    }
    for (const child of Object.values(value)) visit(child);
  };
  visit(mobile);
  assert.ok(count > 50);
  assert.deepEqual(
    mobile.paths["/donor/claim"].post.responses["200"],
    original.paths["/donor/claim"].post.responses["200"],
  );
});

test("token generators preserve semantic names and reject invalid numeric values", () => {
  const input = {
    schema_version: 1,
    color: { "brand-ink": "#c8340f" },
    radius: { card: 18 },
    space: { 1: 4.5 },
    text: { "2xl": 28 },
  };
  const output = tokenSources(input);
  assert.ok(output["DADTokens.swift"].includes('"brand-ink": "#c8340f"'));
  assert.ok(output["DADTokens.kt"].includes('"2xl" to 28.0'));
  assert.ok(output["tokens.ts"].includes('"1": 4.5'));
  assert.ok(output["DADTokens.kt"].includes('"1" to 4.5,'));
  assert.throws(() => tokenSources({ ...input, text: { md: -1 } }), /Invalid design token/);
});
