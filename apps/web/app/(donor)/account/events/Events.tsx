"use client";

import Link from "next/link";
import { Announcer } from "@/components/donor/Announcer";
import { Page } from "@/components/donor/Page";
import { SignInPrompt } from "@/components/donor/SignInPrompt";
import { api, type Schema } from "@/lib/api";
import { formatDateTime } from "@/lib/dates";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";

type Event = Schema<"EventView">;
type Lists = { near: Event[]; invited: Event[] };

/** What a donor's place, or the room left, at an event looks like in a list. */
export function placeSummary(event: Event): string {
  if (event.cancelled) return "Cancelled";
  if (event.reservation_status === "RESERVED") return "You have a spot";
  if (event.reservation_status === "WAITLISTED") return "You are on the waitlist";
  if (event.spots_left === null) return "Open to everyone";
  return event.spots_left > 0
    ? `${event.spots_left} ${event.spots_left === 1 ? "spot" : "spots"} left`
    : "Full — join the waitlist";
}

function EventList({ events }: { events: Event[] }) {
  return (
    <ul className="events">
      {events.map((event) => (
        <li key={event.id}>
          <Link href={`/events/${encodeURIComponent(event.id)}`}>{event.title}</Link>
          <p className="note">
            {formatDateTime(event.starts_at)} · {event.venue}, {event.city} · {placeSummary(event)}
          </p>
          {event.invitation_note && <p className="note">{event.invitation_note}</p>}
        </li>
      ))}
    </ul>
  );
}

export default function Events() {
  const { resource, data, reload, failed } = useApiResource<Lists>("/donor/me/events", {
    load: async (signal) => {
      const [near, invited] = await Promise.all([
        api<{ events: Event[] }>("/donor/me/events", { signal }),
        api<{ events: Event[] }>("/donor/me/events/invite-only", { signal }),
      ]);
      return { near: near.events, invited: invited.events };
    },
  });
  const messages = resourceMessages(resource, {
    loading: "Loading events…",
    failure: "Events could not be loaded right now. Please retry in a moment.",
  });
  return (
    <Page eyebrow="My account" title="Events near you">
      {resource.state === "signedOut" ? (
        <SignInPrompt>Sign in to see events near you</SignInPrompt>
      ) : (
        <>
          {data && (
            <>
              {data.invited.length > 0 && (
                <section aria-labelledby="invited">
                  <h2 id="invited">You’re invited</h2>
                  <EventList events={data.invited} />
                </section>
              )}
              <section aria-labelledby="near">
                <h2 id="near">Coming up near you</h2>
                {data.near.length ? (
                  <EventList events={data.near} />
                ) : (
                  <p>
                    No events are planned near you yet. Events are matched to the city and ZIP code in your{" "}
                    <Link href="/account/profile">profile</Link>.
                  </p>
                )}
              </section>
            </>
          )}
          <Announcer status={messages.status} alert={messages.alert} />
          {failed && (
            <button type="button" className="secondary" onClick={reload}>
              Retry loading
            </button>
          )}
          <p>
            <Link href="/account/dashboard">Back to your dashboard</Link>
          </p>
        </>
      )}
    </Page>
  );
}
