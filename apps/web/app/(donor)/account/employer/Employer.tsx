"use client";
import { useState } from "react";
import { Announcer } from "@/components/donor/Announcer";
import { Page } from "@/components/donor/Page";
import { api, type ResponseBody } from "@/lib/api";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";

type Team = ResponseBody<"/donor/me/team", "get">;

export default function Employer() {
  const { resource, data, reload } = useApiResource<Team>("/donor/me/team");
  const [code, setCode] = useState("");
  const [team, setTeam] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [status, setStatus] = useState("");

  const linked = Boolean(data?.partner_id);

  async function enroll(event: React.FormEvent) {
    event.preventDefault();
    if (busy) return;
    setBusy(true);
    setError("");
    setStatus("");
    try {
      const result = await api<{ organization: string }>("/donor/me/employer", {
        method: "POST",
        body: JSON.stringify({ company_code: code }),
      });
      setStatus(`You're now linked to ${result.organization}.`);
      setCode("");
      reload();
    } catch {
      setError("That company code is not recognised. Check it with your employer and try again.");
    } finally {
      setBusy(false);
    }
  }

  async function joinTeam(event: React.FormEvent) {
    event.preventDefault();
    if (busy) return;
    setBusy(true);
    setError("");
    setStatus("");
    try {
      await api("/donor/me/team", { method: "POST", body: JSON.stringify({ name: team }) });
      setStatus(`You've joined the ${team} team.`);
      setTeam("");
      reload();
    } catch {
      setError("That team name could not be saved. Use up to 80 characters.");
    } finally {
      setBusy(false);
    }
  }

  const messages = resourceMessages(resource, {
    loading: "Loading your employer…",
    failure: "Your employer details could not be loaded right now. Please retry in a moment.",
  });

  return (
    <Page eyebrow="Account" back={{ href: "/account", label: "Account" }} title="Employer and team">
      {messages.alert && <p className="callout">{messages.alert}</p>}
      {data && !linked && (
        <>
          <p className="lead">
            Does your employer partner with DAD? Enter your company code to link your giving to their impact. Your
            individual gifts are never shown to your employer — figures are only ever reported for groups of five or
            more.
          </p>
          <form onSubmit={enroll}>
            <label>
              Company code
              <input
                value={code}
                maxLength={64}
                autoCapitalize="characters"
                required
                onChange={(e) => setCode(e.target.value)}
              />
            </label>
            <button className="button" type="submit" disabled={busy}>
              {busy ? "Linking…" : "Link my employer"}
            </button>
          </form>
        </>
      )}
      {data && linked && (
        <>
          <p className="lead">
            You're linked to <strong>{data.organization}</strong>
            {data.team_name ? (
              <>
                {" "}
                on the <strong>{data.team_name}</strong> team.
              </>
            ) : (
              " — you're not on a team yet."
            )}
          </p>
          <form onSubmit={joinTeam}>
            <label>
              {data.team_name ? "Change team" : "Join a team"}
              <input value={team} maxLength={80} required onChange={(e) => setTeam(e.target.value)} />
            </label>
            <button className="button" type="submit" disabled={busy}>
              {busy ? "Saving…" : data.team_name ? "Change team" : "Join team"}
            </button>
          </form>
        </>
      )}
      <Announcer status={status} alert={error} />
    </Page>
  );
}
