import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import SharePage from "./page";
import CodePage from "../code/page";
import ReferralLanding from "../../r/[code]/ReferralLanding";

const sharing = { public_name: "Sam", link: "https://dad.example/r/code", irl_link: "https://dad.example/r/code?via=irl" };
afterEach(() => { vi.unstubAllGlobals(); window.history.replaceState(null, "", "/"); });
function response(data: unknown) { return { ok: true, json: async () => data }; }

it("requires a chosen public nickname and keeps QR off the share page", async () => {
  const fetch = vi.fn().mockResolvedValueOnce(response({ public_name: null, link: null, irl_link: null }))
    .mockResolvedValueOnce(response(sharing));
  vi.stubGlobal("fetch", fetch);
  render(<SharePage />);
  fireEvent.change(await screen.findByLabelText("Public first name or nickname"), { target: { value: "Sam" } });
  expect(screen.getByText(/anyone who opens your referral link/)).toBeVisible();
  expect(screen.queryByRole("img")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Save public name" }));
  expect(await screen.findByLabelText("Your referral link")).toHaveValue(sharing.link);
  expect(JSON.parse(fetch.mock.calls[1][1].body)).toEqual({ public_name: "Sam", replace_code: false });
});

it("opens native sharing with the Link URL and accurate billing disclosure", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response(sharing)));
  const share = vi.fn().mockResolvedValue(undefined);
  vi.stubGlobal("navigator", { share });
  render(<SharePage />);
  fireEvent.click(await screen.findByRole("button", { name: "Share invitation" }));
  expect(share).toHaveBeenCalledWith(expect.objectContaining({ url: sharing.link,
    text: expect.stringContaining("$30/month") }));
});

it("requires a confirmation step before replacing the link", async () => {
  const fetch = vi.fn().mockResolvedValue(response(sharing));
  vi.stubGlobal("fetch", fetch);
  render(<SharePage />);
  fireEvent.click(await screen.findByRole("button", { name: "Replace my link and code" }));
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("alert")).toHaveTextContent("stop working for new visitors");
  fireEvent.click(screen.getByRole("button", { name: "Confirm replacement" }));
  await screen.findByText("Your sharing details are saved.");
  expect(JSON.parse(fetch.mock.calls[1][1].body).replace_code).toBe(true);
});

it("shows an on-screen code with brightness instructions and no download control", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response(sharing)));
  render(<CodePage />);
  expect(await screen.findByRole("img")).toHaveAttribute("src", "/api/donor/me/referral-code.svg?view=0");
  expect(screen.getByText(/Increase your screen brightness/)).toBeVisible();
  expect(screen.queryByRole("link", { name: /download/i })).not.toBeInTheDocument();
  fireEvent.error(screen.getByRole("img"));
  expect(screen.getByRole("alert")).toHaveTextContent("code image could not be loaded");
});

it("captures the IRL marker before enabling giving and uses the returned public name", async () => {
  window.history.replaceState(null, "", "/r/code?via=irl");
  const fetch = vi.fn().mockResolvedValue(response({ captured: true, public_name: "Sam" }));
  vi.stubGlobal("fetch", fetch);
  render(<ReferralLanding code="code" />);
  expect(screen.queryByRole("link", { name: "Continue to giving" })).not.toBeInTheDocument();
  expect(await screen.findByRole("heading", { name: "Sam invited you to DAD" })).toBeVisible();
  expect(fetch.mock.calls[0][0]).toBe("/api/donor/referrals/code");
  expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({ kind: "IRL" });
  expect(screen.getByRole("link", { name: "Continue to giving" })).toHaveAttribute("href", "/donate");
});

it("defaults shared links to Link attribution and handles unavailable codes", async () => {
  const fetch = vi.fn().mockResolvedValue(response({ captured: false, public_name: null }));
  vi.stubGlobal("fetch", fetch);
  render(<ReferralLanding code="old" />);
  expect(await screen.findByText("This referral link is no longer available.")).toBeVisible();
  expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({ kind: "LINK" });
  expect(screen.getByRole("link", { name: "Continue without a referral" })).toHaveAttribute("href", "/donate");
});

it("keeps referral failure distinct from successful capture", async () => {
  vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("Offline")));
  render(<ReferralLanding code="code" />);
  expect(await screen.findByRole("alert")).toHaveTextContent("couldn’t confirm your referral");
  expect(screen.queryByRole("link", { name: "Continue to giving" })).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Retry referral" })).toBeVisible();
});
