"use client";

import { useState } from "react";
import { Announcer } from "@/components/donor/Announcer";
import { api, failureMessage } from "@/lib/api";

export function RecomputeRecognition({ donor, onComplete }: { donor: string; onComplete: () => void }) {
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState("");
  const [error, setError] = useState("");
  async function submit(event: React.FormEvent) {
    event.preventDefault();
    if (busy) return;
    setBusy(true);
    setStatus("");
    setError("");
    try {
      await api(`/donor/staff/donors/${donor}/recognition/recompute`, {
        method: "POST",
        body: JSON.stringify({ reason }),
      });
      setStatus("Recognition recomputed from recorded facts. The reason and result are in the audit log.");
      setReason("");
      onComplete();
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
        <Announcer status={status} alert={error} />
      </form>
    </details>
  );
}
