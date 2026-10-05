import { describe, expect, it } from "vitest";
import { webTokenVariables } from "@/design/web";

function token(name: string): string {
  const value = (webTokenVariables as Record<string, unknown>)[`--${name}`];
  if (typeof value !== "string" || !/^#[0-9a-fA-F]{3,6}$/.test(value)) throw new Error(`--${name} is not defined as a hex colour`);
  return value;
}

function luminance(hex: string): number {
  let h = hex.replace("#", "");
  if (h.length === 3) h = [...h].map((c) => c + c).join("");
  const channel = (pair: string) => {
    const v = parseInt(pair, 16) / 255;
    return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
  };
  return (
    0.2126 * channel(h.slice(0, 2)) +
    0.7152 * channel(h.slice(2, 4)) +
    0.0722 * channel(h.slice(4, 6))
  );
}

function contrast(foreground: string, background: string): number {
  const a = luminance(foreground);
  const b = luminance(background);
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
}

describe("the palette meets accessible text contrast thresholds", () => {
  const paper = token("paper");

  it.each([
    ["--ink, body text", "ink", 4.5],
    ["--ink-soft, the 9-11px labels that carry the detail", "ink-soft", 4.5],
    ["--brand-ink, the brand hue taken to AA for small text", "brand-ink", 4.5],
  ])("%s reaches %s:1 on paper", (_label, name, minimum) => {
    expect(contrast(token(name), paper)).toBeGreaterThanOrEqual(minimum as number);
  });

  it("--brand reaches large-text AA on paper, which is what it is reserved for", () => {
    // Fills, underlines and display sizes only; never small text, and never inside a
    // status chip where it would read as a verdict.
    expect(contrast(token("brand"), paper)).toBeGreaterThanOrEqual(3);
  });

  it("--warn is legible on its own wash", () => {
    expect(contrast(token("warn"), token("warn-wash"))).toBeGreaterThanOrEqual(4.5);
  });

  it("a verdict is never carried by the house colour", () => {
    // Orange is identity. Confirmation and failure have to be distinguishable from it, or
    // the palette says "verified" and "look at this" in the same voice.
    expect(token("ok")).not.toBe(token("brand"));
    expect(token("fail")).not.toBe(token("brand"));
  });

  it.each([
    ["--ok-ink, a confirmation", "ok-ink", "ok-wash"],
    ["--fail-ink, a failure", "fail-ink", "fail-wash"],
  ])("%s is legible on its own wash", (_label, ink, wash) => {
    expect(contrast(token(ink as string), token(wash as string))).toBeGreaterThanOrEqual(4.5);
  });
});
