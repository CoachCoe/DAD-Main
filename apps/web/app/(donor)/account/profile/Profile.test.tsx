import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import Profile from "./Profile";

afterEach(() => vi.unstubAllGlobals());

const profile = { email: "alex@example.org", public_name: "Alex", consent_version: "r2-notifications-v1", version: 3,
  referral_emails: false, marketing_emails: false, push_referral_converted: true, push_streak_at_risk: true, push_streak_milestone: true };

const json = (body: unknown, status = 200) => Promise.resolve({ ok: status < 400, status, json: async () => body });

it("saves app notification choices alongside the email ones", async () => {
  const fetch = vi.fn((url: string, init?: RequestInit) => {
    if (url === "/api/donor/me/profile") return json(profile);
    if (url === "/api/donor/me/sessions") return json({ sessions: [] });
    if (url === "/api/donor/me/preferences" && init?.method === "POST") return json({ ...profile, ...JSON.parse(String(init.body)), version: 4 });
    throw new Error(`unexpected ${url}`);
  });
  vi.stubGlobal("fetch", fetch);
  render(<Profile />);
  const app = within(await screen.findByRole("group", { name: "DAD app notifications" }));
  const milestone = app.getByRole("checkbox", { name: "When my giving streak reaches a milestone" });
  expect(app.getByRole("checkbox", { name: "When someone I invited gives for the first time" })).toBeChecked();
  expect(app.getByRole("checkbox", { name: "When a declined payment puts my giving streak at risk" })).toBeChecked();
  expect(milestone).toBeChecked();
  fireEvent.click(milestone);
  fireEvent.click(screen.getByRole("button", { name: "Save preferences" }));
  expect(await screen.findByText("Notification preferences saved.")).toHaveAttribute("role", "status");
  const saved = fetch.mock.calls.find(([url]) => url === "/api/donor/me/preferences");
  expect(JSON.parse(String(saved?.[1]?.body))).toEqual({ version: 3, referral_emails: false, marketing_emails: false,
    push_referral_converted: true, push_streak_at_risk: true, push_streak_milestone: false });
  expect(milestone).not.toBeChecked();
});
