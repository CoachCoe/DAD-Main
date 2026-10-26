"use client";
import Link from "next/link";
import { type FormEvent, useState } from "react";
import { Announcer } from "@/components/donor/Announcer";
import { Page } from "@/components/donor/Page";
import { api, failureMessage, type ResponseBody, type Schema } from "@/lib/api";
import { formatDollars, formatMoney, parseDollars } from "@/lib/money";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";
import { matchTerms } from "../Partners";

type MatchStatus = ResponseBody<"/donor/staff/partners/{partnerID}/match-status", "get">;

export default function PartnerDetail({ partner }: { partner: string }) {
  const id = encodeURIComponent(partner);
  const year = new Date().getFullYear();
  const { resource, data, reload } = useApiResource(`/donor/staff/partners/${id}`, {
    load: async (signal) => {
      const [detail, match] = await Promise.all([
        api<Schema<"StaffPartnerDetail">>(`/donor/staff/partners/${id}`, { signal }),
        api<MatchStatus>(`/donor/staff/partners/${id}/match-status?year=${year}`, { signal }),
      ]);
      return { detail, match };
    },
  });
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState("");
  const [error, setError] = useState("");
  const messages = resourceMessages(resource, {
    loading: "Loading partner…",
    failure: "This partner could not be loaded right now. Please retry in a moment.",
    forbidden: "Partner administration requires partnerships access.",
    notFound: "No approved partner matches this link.",
  });

  async function run(event: FormEvent<HTMLFormElement> | null, done: string, action: () => Promise<unknown>) {
    event?.preventDefault();
    const form = event?.currentTarget;
    if (busy) return;
    setBusy(true);
    setStatus("");
    setError("");
    try {
      await action();
      setStatus(done);
      form?.reset();
      reload();
    } catch (cause) {
      setError(failureMessage(cause, "That change could not be saved. Nothing was changed."));
    } finally {
      setBusy(false);
    }
  }

  const post = (path: string, body: unknown) =>
    api(`/donor/staff/partners/${id}/${path}`, { method: "POST", body: JSON.stringify(body) });

  function saveMatch(event: FormEvent<HTMLFormElement>) {
    const form = new FormData(event.currentTarget);
    const rate = Number(form.get("rate"));
    const cap = parseDollars(String(form.get("cap") ?? ""));
    const workforce = Number(form.get("workforce"));
    if (!Number.isFinite(rate) || rate < 0 || cap === null || !Number.isInteger(workforce) || workforce < 0) {
      event.preventDefault();
      setError("Enter a match rate, a yearly cap in dollars, and a whole-number workforce size.");
      return;
    }
    return run(event, "Match terms saved.", () =>
      post("match", { match_rate_bp: Math.round(rate * 100), match_cap_minor: cap, workforce_size: workforce }),
    );
  }

  function recordPayment(event: FormEvent<HTMLFormElement>) {
    const amount = parseDollars(String(new FormData(event.currentTarget).get("amount") ?? ""));
    if (!amount) {
      event.preventDefault();
      setError("Enter the payment received in dollars.");
      return;
    }
    return run(event, `Recorded a ${formatMoney(amount)} match payment for ${year}.`, () =>
      post("match-payment", { year, amount_minor: amount }),
    );
  }

  function link(event: FormEvent<HTMLFormElement>) {
    const initiative = String(new FormData(event.currentTarget).get("initiative") ?? "").trim();
    return run(event, "Initiative linked.", () => post("link", { initiative_id: initiative }));
  }

  function invite(event: FormEvent<HTMLFormElement>) {
    const form = new FormData(event.currentTarget);
    const email = String(form.get("email") ?? "");
    return run(event, `Invited ${email}. They have been emailed a sign-in link.`, () =>
      post("users", { email, name: String(form.get("name") ?? ""), role: String(form.get("role") ?? "VIEWER") }),
    );
  }

  const detail = data?.detail;
  const match = data?.match;
  return (
    <Page
      wide
      eyebrow="Staff"
      back={{ href: "/staff/partners", label: "Partners" }}
      title={detail?.organization ?? "Partner"}
    >
      {resource.state === "signedOut" && (
        <p>
          <Link className="button" href="/staff/sign-in">
            Staff sign-in
          </Link>
        </p>
      )}
      {detail && match && (
        <>
          <p className="note">
            {detail.type || "Partner"}
            {detail.website && (
              <>
                {" "}
                · <a href={detail.website}>{detail.website}</a>
              </>
            )}
            {detail.company_code && (
              <>
                {" "}
                · Company code <code>{detail.company_code}</code>
              </>
            )}
          </p>

          <h2>Matching</h2>
          <p>
            {detail.workforce_size.toLocaleString()} people · {matchTerms(detail.match_rate_bp, detail.match_cap_minor)}
          </p>
          <p>
            {year}: employees gave {formatDollars(match.gifts_minor)} · match owed {formatDollars(match.owed_minor)} ·
            received {formatDollars(match.paid_minor)} · outstanding {formatDollars(match.outstanding_minor)}
          </p>
          <form onSubmit={saveMatch}>
            <label>
              Match rate (%)
              <input
                name="rate"
                inputMode="decimal"
                defaultValue={detail.match_rate_bp / 100}
                key={`rate-${detail.match_rate_bp}`}
                required
              />
            </label>
            <label>
              Annual match cap in USD (0 for no cap)
              <input
                name="cap"
                inputMode="decimal"
                defaultValue={(detail.match_cap_minor / 100).toFixed(2)}
                key={`cap-${detail.match_cap_minor}`}
                required
              />
            </label>
            <label>
              Workforce size
              <input
                name="workforce"
                inputMode="numeric"
                defaultValue={detail.workforce_size}
                key={`workforce-${detail.workforce_size}`}
                required
              />
            </label>
            <button className="button" type="submit" disabled={busy}>
              Save match terms
            </button>
          </form>
          <form onSubmit={recordPayment}>
            <label>
              Match payment received for {year} in USD
              <input name="amount" inputMode="decimal" required />
            </label>
            <button className="button" type="submit" disabled={busy}>
              Record payment
            </button>
          </form>

          <h2>Initiatives</h2>
          {detail.initiatives.length ? (
            <ul>
              {detail.initiatives.map((initiative) => (
                <li key={initiative.id}>
                  {initiative.title} · {initiative.status}
                  {initiative.publish_state === "DRAFT" && " · Draft"} · Committed{" "}
                  {formatDollars(initiative.committed_minor)}
                </li>
              ))}
            </ul>
          ) : (
            <p>No initiatives are linked to this partner.</p>
          )}
          <form onSubmit={link}>
            <label>
              Initiative ID
              <input name="initiative" required pattern="[0-9a-fA-F-]{36}" />
            </label>
            <button className="button" type="submit" disabled={busy}>
              Link initiative
            </button>
          </form>

          <h2>Portal users</h2>
          {detail.users.length ? (
            <ul>
              {detail.users.map((user) => (
                <li key={user.id}>
                  {user.name || user.email} · {user.email} · {user.role === "ADMIN" ? "Admin" : "Viewer"}
                  {!user.accepted && " · Invitation not yet used"}{" "}
                  <button
                    type="button"
                    className="link"
                    disabled={busy}
                    onClick={() =>
                      run(null, `Removed ${user.email}.`, () =>
                        api(`/donor/staff/partners/${id}/users/${encodeURIComponent(user.id)}/remove`, {
                          method: "POST",
                        }),
                      )
                    }
                  >
                    Remove {user.email}
                  </button>
                </li>
              ))}
            </ul>
          ) : (
            <p>This partner has no active portal users.</p>
          )}
          <form onSubmit={invite}>
            <label>
              Email
              <input name="email" type="email" maxLength={320} required />
            </label>
            <label>
              Name
              <input name="name" maxLength={100} />
            </label>
            <label>
              Role
              <select name="role" defaultValue="VIEWER">
                <option value="VIEWER">Viewer</option>
                <option value="ADMIN">Admin</option>
              </select>
            </label>
            <button className="button" type="submit" disabled={busy}>
              Invite user
            </button>
          </form>
        </>
      )}
      <Announcer status={status || messages.status} alert={error || messages.alert} />
      {(resource.state === "offline" || resource.state === "error") && (
        <button type="button" className="secondary" onClick={reload}>
          Retry loading
        </button>
      )}
    </Page>
  );
}
