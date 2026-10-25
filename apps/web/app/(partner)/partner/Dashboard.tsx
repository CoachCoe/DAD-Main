"use client";
import Link from "next/link";
import { useRef, useState } from "react";
import { Announcer } from "@/components/donor/Announcer";
import { Page } from "@/components/donor/Page";
import { api, type ResponseBody } from "@/lib/api";
import { formatDateTime } from "@/lib/dates";
import { formatMoney } from "@/lib/money";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";

type Overview = ResponseBody<"/donor/partner/overview", "get">;
type Initiatives = ResponseBody<"/donor/partner/initiatives", "get">;
type Spend = ResponseBody<"/donor/partner/spend", "get">;
type Users = ResponseBody<"/donor/partner/users", "get">;
type Exports = ResponseBody<"/donor/partner/exports", "get">;

function Tile({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <article className="tile">
      <p className="tile-value">{value}</p>
      <p className="tile-label">{label}</p>
      {hint && <p className="note">{hint}</p>}
    </article>
  );
}

export default function Dashboard() {
  const overview = useApiResource<Overview>("/donor/partner/overview");
  const initiatives = useApiResource<Initiatives>("/donor/partner/initiatives");
  const spend = useApiResource<Spend>("/donor/partner/spend");
  const users = useApiResource<Users>("/donor/partner/users");
  const exports = useApiResource<Exports>("/donor/partner/exports");

  const o = overview.data;
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [status, setStatus] = useState("");
  const [invite, setInvite] = useState({ email: "", name: "", role: "VIEWER" });
  const inviteLink = useRef("");

  async function downloadReport() {
    setError("");
    try {
      const response = await fetch("/api/donor/partner/report.pdf", { credentials: "include" });
      if (!response.ok) throw new Error(String(response.status));
      const blob = await response.blob();
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = "dad-impact-report.pdf";
      link.click();
      URL.revokeObjectURL(url);
      setStatus("Your branded impact report was downloaded.");
      exports.reload();
    } catch {
      setError("The report could not be generated right now. Only partner administrators can export reports.");
    }
  }

  async function sendInvite(event: React.FormEvent) {
    event.preventDefault();
    if (busy) return;
    setBusy(true);
    setError("");
    setStatus("");
    inviteLink.current = "";
    try {
      const result = await api<{ invite: string }>("/donor/partner/users", {
        method: "POST",
        body: JSON.stringify(invite),
      });
      inviteLink.current = `${window.location.origin}/partner/accept#token=${result.invite}`;
      setStatus(`Invited ${invite.email}. Share their sign-in link below.`);
      setInvite({ email: "", name: "", role: "VIEWER" });
      users.reload();
    } catch {
      setError("The invitation could not be sent. Only partner administrators can invite users.");
    } finally {
      setBusy(false);
    }
  }

  async function removeUser(id: string, email: string) {
    setError("");
    setStatus("");
    try {
      await api(`/donor/partner/users/${id}/remove`, { method: "POST", body: "{}" });
      setStatus(`Removed ${email}.`);
      users.reload();
    } catch {
      setError("That user could not be removed. Administrators cannot remove themselves.");
    }
  }

  const messages = resourceMessages(overview.resource, {
    loading: "Loading your dashboard…",
    failure: "Your partner dashboard could not be loaded right now. Please retry in a moment.",
    forbidden: "This dashboard requires a partner account.",
  });

  return (
    <Page wide eyebrow="Partner portal" title={o?.organization ?? "Partner dashboard"}>
      {overview.resource.state === "signedOut" && (
        <p>
          Your session has ended. <Link href="/partner/apply">Apply to partner with DAD</Link> or open your invitation
          link again.
        </p>
      )}
      {messages.alert && <p className="callout">{messages.alert}</p>}

      {o && (
        <section aria-label="Overview">
          <div className="tiles">
            <Tile label="Committed" value={formatMoney(o.committed_minor)} />
            <Tile label="Deployed" value={formatMoney(o.deployed_minor)} hint={`${o.deployed_percent}% of committed`} />
            <Tile label="People reached" value={o.people_reached.toLocaleString()} hint="Verified figures only" />
            <Tile label="Our People" value="Coming soon" hint="Employee enrolment arrives in a later release" />
          </div>
        </section>
      )}

      {initiatives.data && (
        <section aria-label="Initiatives">
          <h2>Initiatives</h2>
          {initiatives.data.initiatives.length ? (
            initiatives.data.initiatives.map((item) => (
              <article key={item.id}>
                <h3>{item.title}</h3>
                <p>{item.summary}</p>
                <p className="note">
                  {formatMoney(item.committed_minor)} committed · {formatMoney(item.deployed_minor)} deployed
                </p>
              </article>
            ))
          ) : (
            <p>No published initiatives are linked to your organization yet.</p>
          )}
        </section>
      )}

      {spend.data && spend.data.categories.length > 0 && (
        <section aria-label="Spend by category">
          <h2>Spend by category</h2>
          <table>
            <thead>
              <tr>
                <th scope="col">Category</th>
                <th scope="col">Amount</th>
              </tr>
            </thead>
            <tbody>
              {spend.data.categories.map((row) => (
                <tr key={row.category}>
                  <td>{row.category}</td>
                  <td>{formatMoney(row.amount_minor)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      )}

      <section aria-label="Reports">
        <h2>Reports</h2>
        <p>Download a branded PDF of these figures. Figures are verified by DAD.</p>
        <button type="button" className="button" onClick={downloadReport}>
          Download impact report (PDF)
        </button>
        {exports.data && exports.data.exports.length > 0 && (
          <>
            <h3>Previous exports</h3>
            <ul>
              {exports.data.exports.map((row) => (
                <li key={row.id}>
                  {formatDateTime(row.created_at)} · {formatMoney(row.committed_minor)} committed ·{" "}
                  {row.people_reached.toLocaleString()} people · by {row.exported_by}
                </li>
              ))}
            </ul>
          </>
        )}
      </section>

      {users.data && (
        <section aria-label="Users">
          <h2>Users</h2>
          <ul>
            {users.data.users.map((u) => (
              <li key={u.id}>
                {u.name} · {u.email} · {u.role}{" "}
                <button type="button" className="link" onClick={() => removeUser(u.id, u.email)}>
                  Remove
                </button>
              </li>
            ))}
          </ul>
          <form onSubmit={sendInvite}>
            <h3>Invite a user</h3>
            <fieldset disabled={busy}>
              <label>
                Email
                <input
                  type="email"
                  value={invite.email}
                  required
                  maxLength={320}
                  onChange={(e) => setInvite({ ...invite, email: e.target.value })}
                />
              </label>
              <label>
                Name
                <input
                  value={invite.name}
                  required
                  maxLength={100}
                  onChange={(e) => setInvite({ ...invite, name: e.target.value })}
                />
              </label>
              <label>
                Role
                <select value={invite.role} onChange={(e) => setInvite({ ...invite, role: e.target.value })}>
                  <option value="VIEWER">Viewer (read-only)</option>
                  <option value="ADMIN">Administrator</option>
                </select>
              </label>
            </fieldset>
            <button type="submit" className="button" disabled={busy}>
              {busy ? "Inviting…" : "Send invitation"}
            </button>
            {inviteLink.current && (
              <p className="note">
                Sign-in link: <code>{inviteLink.current}</code>
              </p>
            )}
          </form>
        </section>
      )}

      <p>
        <button
          type="button"
          className="link"
          onClick={() =>
            api("/donor/partner/sign-out", { method: "POST", body: "{}" }).finally(() =>
              window.location.assign("/partner/accept"),
            )
          }
        >
          Sign out
        </button>
      </p>
      <Announcer status={status} alert={error} />
    </Page>
  );
}
