"use client";
import Link from "next/link";
import { Announcer } from "@/components/donor/Announcer";
import { Page } from "@/components/donor/Page";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";

export default function Exports() {
  const { resource, data, reload } = useApiResource<{ datasets: string[] }>("/donor/staff/exports");
  const messages = resourceMessages(resource, { loading: "Checking export permissions…",
    failure: "Exports could not be loaded right now. Please retry in a moment.",
    forbidden: "Your role does not permit exports." });
  return <Page wide eyebrow="Staff" back={{ href: "/staff", label: "Staff back office" }} title="Exports">
    <p>Exports contain private donor information. Store them only in approved locations. Each export records its actor and completion status.</p>
    {resource.state === "signedOut" && <p><Link className="button" href="/staff/sign-in">Staff sign-in</Link></p>}
    {data && <ul>{data.datasets.map((name) => <li key={name}><a href={`/api/donor/staff/exports/${name}.csv`}>Download {name} CSV</a></li>)}</ul>}
    <h2>Recipient enrolment queue</h2><p>Activates at R2. R1 does not collect recipient enrolments. The enrolments export currently contains column headers only.</p>
    <Announcer status={messages.status} alert={messages.alert} />
    {resource.state !== "signedOut" && <button type="button" className="secondary" onClick={reload}>Refresh exports</button>}
  </Page>;
}
