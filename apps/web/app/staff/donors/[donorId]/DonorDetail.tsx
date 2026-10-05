"use client";
import { useState } from "react";
import Link from "next/link";
import { Announcer } from "@/components/donor/Announcer";
import { api } from "@/lib/api";
import { formatMoney } from "@/lib/money";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";
import type { StaffMember } from "../../Staff";

type Detail = { id: string; email: string; public_name: string | null;
  streak: { current: number | null; best: number; at_risk: boolean; computed_at: string } | null;
  referrer: { id: string; email: string; type: string } | null;
  gifts: { id: string; amount_minor: number; currency: string; frequency: string; paid_at: string }[];
  referred: { id: string; email: string; type: string; net_minor: number | null }[];
  next_gifts_offset: number | null; next_referrals_offset: number | null };
type Actions = { actions: { id: string; actor_id: string; kind: string; target_id: string; amount_minor: number | null; period_end: string | null;
  reason: string; status: string; completed_at: string | null }[]; next_offset: number | null };

export default function DonorDetail({ donor }: { donor: string }) {
  const id = encodeURIComponent(donor);
  const [gifts, setGifts] = useState(0);
  const [referrals, setReferrals] = useState(0);
  const path = `/donor/staff/donors/${id}?gifts_offset=${gifts}&referrals_offset=${referrals}`;
  const { resource, data, reload } = useApiResource(path, {
    load: async (signal) => {
      const [detail, actor] = await Promise.all([api<Detail>(path, { signal }), api<StaffMember>("/donor/staff/me", { signal })]);
      return { detail, actor };
    },
  });
  const messages = resourceMessages(resource, { loading: "Loading donor record…",
    failure: "The donor record could not be loaded right now. Please retry in a moment.",
    forbidden: "Your staff role does not permit viewing donor records.",
    notFound: "No donor record matches this link. Search again from the staff back office." });
  const detail = data?.detail;
  const can = (permission: string) => data?.actor.permissions.includes(permission) ?? false;
  return <section className="panel"><h1>{detail?.email ?? "Donor record"}</h1>
    <p><Link href="/staff">Staff back office</Link></p>
    {resource.state === "signedOut" && <p><Link className="button" href="/staff/sign-in">Staff sign-in</Link></p>}
    {detail && <>
      <p><Link href={`/staff/messages?donor=${id}`}>Delivery history and resends</Link></p>
      <p>Public nickname: {detail.public_name || "Not shared"}</p>
      <p>Streak: {detail.streak?.current ?? "Updating"} · Best: {detail.streak?.best ?? "Updating"}{detail.streak?.at_risk ? " · At risk" : ""}</p>
      {detail.streak && <p>Computed {new Date(detail.streak.computed_at).toLocaleString()}</p>}
      <p>Referrer: {detail.referrer ? `${detail.referrer.email} · ${detail.referrer.type}` : "No attributed referrer"}</p>
      <h2>Gifts</h2>
      {can("gift.cancel") && <p><Link href={`/staff/finance?donor=${id}`}>Cancel monthly giving</Link></p>}
      {detail.gifts.length ? <ul>{detail.gifts.map((gift) => <li key={gift.id}>
        {formatMoney(gift.amount_minor, gift.currency)} · {gift.frequency} · {new Date(gift.paid_at).toLocaleString()} · <code>{gift.id}</code>
        {can("gift.refund") && <> · <Link href={`/staff/finance?donor=${id}&gift=${encodeURIComponent(gift.id)}`}>Refund gift</Link></>}
      </li>)}</ul> : <p>No gifts recorded.</p>}
      {gifts > 0 && <button type="button" className="secondary" onClick={() => setGifts(0)}>Newest gifts</button>}
      {detail.next_gifts_offset !== null && <button type="button" className="secondary" onClick={() => setGifts(detail.next_gifts_offset ?? 0)}>Older gifts</button>}
      <h2>People referred</h2>
      {detail.referred.length ? <ul>{detail.referred.map((person) => <li key={person.id}>
        {person.email} · {person.type} · Net {person.net_minor === null ? "Updating" : formatMoney(person.net_minor)}
      </li>)}</ul> : <p>No converting referrals recorded.</p>}
      {referrals > 0 && <button type="button" className="secondary" onClick={() => setReferrals(0)}>First referrals</button>}
      {detail.next_referrals_offset !== null && <button type="button" className="secondary" onClick={() => setReferrals(detail.next_referrals_offset ?? 0)}>More referrals</button>}
      <ActionHistory donor={id} />
    </>}
    <Announcer status={messages.status} alert={messages.alert} />
    {(resource.state === "offline" || resource.state === "error") && <button type="button" className="secondary" onClick={reload}>Retry loading</button>}
  </section>;
}

function ActionHistory({ donor }: { donor: string }) {
  const [offset, setOffset] = useState(0);
  const { resource, data, reload } = useApiResource<Actions>(`/donor/staff/donors/${donor}/actions?offset=${offset}`);
  const messages = resourceMessages(resource, { loading: "Loading staff actions…",
    failure: "Staff actions could not be loaded right now. Please retry in a moment." });
  return <section aria-labelledby="staff-actions"><h2 id="staff-actions">Staff financial actions</h2>
    {data && <>{data.actions.length ? <ul>{data.actions.map((item) => <li key={item.id}>
      {item.kind} · {item.status}{item.amount_minor !== null && ` · ${formatMoney(item.amount_minor)}`}
      {item.period_end && ` · Stops ${new Date(item.period_end).toLocaleString()}`} · {item.completed_at ? `Completed ${new Date(item.completed_at).toLocaleString()}` : "Not completed"}
      <p>Reason: {item.reason}. Staff member: <code>{item.actor_id}</code>. Target: <code>{item.target_id}</code></p>
    </li>)}</ul> : <p>No refunds or cancellations recorded.</p>}
      {offset > 0 && <button type="button" className="secondary" onClick={() => setOffset(0)}>Newest actions</button>}
      {data.next_offset !== null && <button type="button" className="secondary" onClick={() => setOffset(data.next_offset ?? 0)}>Older actions</button>}
    </>}
    <Announcer status={messages.status} alert={messages.alert} />
    {(resource.state === "offline" || resource.state === "error") && <button type="button" className="secondary" onClick={reload}>Retry staff actions</button>}
  </section>;
}
