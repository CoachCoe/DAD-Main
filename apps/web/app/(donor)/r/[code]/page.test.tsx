import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import ReferralPage, { generateMetadata } from "./page";

afterEach(() => { vi.unstubAllGlobals(); vi.unstubAllEnvs(); });

function lookUp(status: number, body: unknown) {
  const fetch = vi.fn(async (url: string) => url.startsWith("/api/")
    ? new Response(JSON.stringify({ captured: true, public_name: null }))
    : new Response(JSON.stringify(body), { status }));
  vi.stubGlobal("fetch", fetch);
  return fetch;
}
const props = (code: string, query: Record<string, string> = {}) => ({ params: Promise.resolve({ code }), searchParams: Promise.resolve(query) });

it("names the referrer on the server for the first render and the share preview", async () => {
  vi.stubEnv("DONOR_API_BASE_URL", "http://api.test:8002");
  const fetch = lookUp(200, { public_name: "Sam" });
  const metadata = await generateMetadata(props("abc"));
  expect(metadata.title).toBe("Sam invited you to DAD");
  expect(metadata.openGraph?.title).toBe("Sam invited you to DAD");
  expect(metadata.robots).toEqual({ index: false });
  render(await ReferralPage(props("abc", { via: "irl" })));
  expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Sam invited you to DAD");
  const [url, init] = fetch.mock.calls[0] as unknown as [string, RequestInit];
  expect(url).toBe("http://api.test:8002/donor/referrals/abc");
  expect(init.next).toEqual({ revalidate: 60 });
  expect([...new Headers(init.headers).keys()]).toEqual(["accept"]);
  const capture = fetch.mock.calls.find(([target]) => target.startsWith("/api/")) as unknown as [string, RequestInit];
  expect(JSON.parse(String(capture[1].body))).toEqual({ kind: "IRL" });
});

it("leads with an unavailable message when the code is unknown", async () => {
  lookUp(404, { detail: "This referral link is no longer available" });
  render(await ReferralPage(props("old")));
  expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("This referral link is no longer available");
  expect((await generateMetadata(props("old"))).title).toBe("You’re invited to DAD");
});

it.each([[429, { detail: "Too many requests" }], [503, {}], [200, { public_name: "" }]])(
  "renders a generic invitation when the lookup answers %s", async (status, body) => {
    lookUp(status, body);
    render(await ReferralPage(props("abc")));
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Join the beginning");
  });

it("encodes the code into the lookup path", async () => {
  const fetch = lookUp(404, {});
  await generateMetadata(props("a/b?c"));
  expect(fetch.mock.calls[0][0]).toMatch(/\/donor\/referrals\/a%2Fb%3Fc$/);
});

it("decodes the route parameter once, so an encoded code is not encoded twice", async () => {
  const fetch = lookUp(200, { public_name: "Rae" });
  render(await ReferralPage(props("r%C3%A6-1")));
  expect(fetch.mock.calls[0][0]).toMatch(/\/donor\/referrals\/r%C3%A6-1$/);
  await waitFor(() => expect(fetch.mock.calls.some(([target]) => target === "/api/donor/referrals/r%C3%A6-1")).toBe(true));
});
