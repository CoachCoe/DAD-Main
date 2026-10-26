"use client";

import { useState } from "react";
import { api, failureMessage, type Schema } from "@/lib/api";
import { formatDate } from "@/lib/dates";
import { formatMoney } from "@/lib/money";
import { useApiResource } from "@/lib/useApiResource";

/** A headline metric as the card shows it, e.g. "1,200 people" or "$4,500.00". */
export function metricValue(metric: Schema<"TodayMetric">): string {
  switch (metric.unit) {
    case "USD_CENTS":
      return formatMoney(metric.value);
    case "PEOPLE":
      return `${metric.value.toLocaleString()} ${metric.value === 1 ? "person" : "people"}`;
    case "INITIATIVES":
      return `${metric.value.toLocaleString()} ${metric.value === 1 ? "initiative" : "initiatives"}`;
    case "PARTNERS":
      return `${metric.value.toLocaleString()} ${metric.value === 1 ? "partner" : "partners"}`;
  }
}

function Spend({ initiative }: { initiative: string }) {
  const [spend, setSpend] = useState<Schema<"InitiativeSpend"> | null>(null);
  const [error, setError] = useState("");
  async function load() {
    if (spend) return;
    setError("");
    try {
      setSpend(await api<Schema<"InitiativeSpend">>(`/initiatives/${encodeURIComponent(initiative)}/spend`));
    } catch (cause) {
      setError(failureMessage(cause, "Spending could not be loaded right now. Please retry in a moment."));
    }
  }
  return (
    <details onToggle={(event) => event.currentTarget.open && load()}>
      <summary>See every dollar this initiative has spent</summary>
      {spend &&
        (spend.categories.length ? (
          <ul>
            {spend.categories.map((row) => (
              <li key={row.category}>
                {row.category}: {formatMoney(row.amount_minor)}
              </li>
            ))}
            <li>Total: {formatMoney(spend.total_minor)}</li>
          </ul>
        ) : (
          <p>No spending has been recorded yet.</p>
        ))}
      {error && <p role="alert">{error}</p>}
    </details>
  );
}

/** "Today your $1 funded": the donor's initiative of the day (A-1 to A-3). */
export function TodayInitiative() {
  const { resource, data } = useApiResource<Schema<"TodayInitiative">>("/donor/me/today-initiative");
  if (resource.state !== "ready" || !data) return null;
  return (
    <article className="metric" aria-labelledby="today-initiative">
      <h2>Today your $1 funded</h2>
      <h3 id="today-initiative" className="initiativeTitle">
        {data.title}
      </h3>
      <p>
        {data.type} · {data.location} · Since {formatDate(`${data.started_on}T12:00:00Z`)}
      </p>
      {data.metrics.length > 0 && (
        <ul>
          {data.metrics.map((metric) => (
            <li key={metric.label}>
              {metric.label}: {metricValue(metric)}
              {metric.verified_on && ` (verified ${formatDate(`${metric.verified_on}T12:00:00Z`)})`}
            </li>
          ))}
        </ul>
      )}
      <Spend initiative={data.initiative_id} />
    </article>
  );
}
