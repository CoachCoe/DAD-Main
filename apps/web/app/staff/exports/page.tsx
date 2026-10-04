"use client";
import { useEffect, useState } from "react";
import Link from "next/link";
import { api, ApiError } from "@/lib/api";

export default function ExportsPage() {
  const [datasets, setDatasets] = useState<string[] | null>(null);
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    let active = true;
    setError(""); setDatasets(null);
    api<{ datasets: string[] }>("/donor/staff/exports", { cache: retry > 0 ? "reload" : "default" }).then((value) => { if (active) setDatasets(value.datasets); }).catch((cause) => {
      if (active) setError(cause instanceof ApiError && cause.status === 403 ? "Your role does not permit exports." : "Exports could not be loaded. Sign in with an authorized staff account or retry when connected.");
    });
    return () => { active = false; };
  }, [retry]);
  return <section className="panel"><h1>Exports</h1><p><Link href="/staff">Staff back office</Link> · <Link href="/staff/sign-in">Staff sign-in</Link></p>
    <p>Exports contain private donor information. Store them only in approved locations. Each export records its actor and completion status.</p>
    {!datasets && !error && <p role="status">Checking export permissions…</p>}
    {datasets && <ul>{datasets.map((name) => <li key={name}><a href={`/api/donor/staff/exports/${name}.csv`}>Download {name} CSV</a></li>)}</ul>}
    <h2>Recipient enrolment queue</h2><p>Activates at R2. R1 does not collect recipient enrolments. The enrolments export currently contains column headers only.</p>
    {error && <p role="alert">{error}</p>}<button type="button" onClick={() => setRetry((value) => value + 1)}>Refresh exports</button>
  </section>;
}
