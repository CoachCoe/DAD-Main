import { expect, it } from "vitest";
import nextConfig from "./next.config";

it("sends the retired /ledger address permanently to /spend", async () => {
  expect(await nextConfig.redirects?.()).toEqual([{ source: "/ledger", destination: "/spend", permanent: true }]);
});
