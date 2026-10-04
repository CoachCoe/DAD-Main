import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, it } from "vitest";

const css = readFileSync(join(process.cwd(), "app/globals.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, " ");

it("keeps R1 numbers and controls free of unsolicited motion", () => {
  expect(css.match(/(?:transition|animation)\s*:[^;}]+/g) ?? []).toEqual([]);
  expect(css.match(/@keyframes\s+[a-zA-Z-]+/g) ?? []).toEqual([]);
});
