"use client";

import { useState } from "react";
import { Announcer } from "@/components/donor/Announcer";
import { api, failureMessage } from "@/lib/api";

export function RecomputeRecognition({
  donor,
  onComplete,
  announce = true,
}: {
  donor: string;
  onComplete: (message?: string) => void;
  announce?: boolean;
}) {
  const [status, setStatus] = useState("");
  const [reason, setReason] = useState("");
  const [reset, setReset] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function submit(event: React.FormEvent) {
    event.preventDefault();
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      await api(`/donor/staff/donors/${donor}/recognition/${reset ? "reset" : "recompute"}`, {
        method: "POST",
        body: JSON.stringify({ reason }),
      });
      setStatus(
        reset
          ? "Streak reset to zero. The reason is in the audit log."
          : "Recognition recomputed from recorded facts. The reason and result are in the audit log.",
      );
      setReason("");
      onComplete(reset ? "Streak reset to zero. The reason is in the audit log." : undefined);
    } catch (cause) {
      setError(failureMessage(cause, "Recognition could not be recomputed. Please retry."));
    } finally {
      setBusy(false);
    }
  }
  return (
    <details>
      <summary>Repair recognition after a system error</summary>
      <p>
        Recompute the current streak and recognition from recorded payment history. Permanent earned badges and
        historical best streaks remain permanent. Record the system error and its resolution below.
      </p>
      <form onSubmit={submit}>
        <label>
          <input type="checkbox" checked={reset} onChange={(e) => setReset(e.target.checked)} />
          Reset the current streak to zero; retain gift history and earned badges
        </label>
        <label>
          Reason for repair
          <textarea
            required
            minLength={10}
            maxLength={1000}
            value={reason}
            disabled={busy}
            onChange={(event) => setReason(event.target.value)}
          />
        </label>
        <button className="secondary" type="submit" disabled={busy}>
          {busy ? "Recomputing…" : "Recompute recognition"}
        </button>
        <Announcer status={announce ? status : ""} alert={error} />
      </form>
    </details>
  );
}
