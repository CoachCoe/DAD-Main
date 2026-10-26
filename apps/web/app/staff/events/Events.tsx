"use client";
import Link from "next/link";
import { useState } from "react";
import { Announcer } from "@/components/donor/Announcer";
import { Page } from "@/components/donor/Page";
import type { Schema } from "@/lib/api";
import { formatDateTime } from "@/lib/dates";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";

export function eventState(event: Schema<"StaffEvent">): string {
  if (event.cancelled_at) return "Cancelled";
  return event.publish_state === "PUBLISHED" ? "Published" : "Draft";
}

export default function Events() {
  const [offset, setOffset] = useState(0);
  const { resource, data, reload } = useApiResource<Schema<"StaffEventPage">>(`/donor/staff/events?offset=${offset}`);
  const messages = resourceMessages(resource, {
    loading: "Loading events…",
    failure: "Events could not be loaded right now. Please retry in a moment.",
    forbidden: "Events require Growth access.",
  });
  return (
    <Page wide eyebrow="Staff" back={{ href: "/staff", label: "Staff back office" }} title="Events">
      <p>
        <Link className="button" href="/staff/events/new">
          New event
        </Link>
      </p>
      {resource.state === "signedOut" && (
        <p>
          <Link className="button" href="/staff/sign-in">
            Staff sign-in
          </Link>
        </p>
      )}
      {data && (
        <>
          {data.events.length ? (
            data.events.map((event) => (
              <article key={event.id}>
                <h2>
                  <Link href={`/staff/events/${encodeURIComponent(event.id)}`}>{event.title}</Link>
                </h2>
                <p className="note">
                  {eventState(event)} · {formatDateTime(event.starts_at)} · {event.venue}, {event.city}
                </p>
                <p>
                  {event.reserved}
                  {event.capacity > 0 ? ` of ${event.capacity}` : ""} reserved · {event.waitlisted} waitlisted ·{" "}
                  {event.checked_in} checked in
                </p>
              </article>
            ))
          ) : (
            <p>No events yet.</p>
          )}
          {offset > 0 && (
            <button type="button" className="secondary" onClick={() => setOffset(Math.max(0, offset - 50))}>
              Later events
            </button>
          )}
          {data.next_offset !== null && (
            <button type="button" className="secondary" onClick={() => setOffset(data.next_offset ?? 0)}>
              Earlier events
            </button>
          )}
        </>
      )}
      <Announcer status={messages.status} alert={messages.alert} />
      {(resource.state === "offline" || resource.state === "error") && (
        <button type="button" className="secondary" onClick={reload}>
          Retry loading
        </button>
      )}
    </Page>
  );
}
