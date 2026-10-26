import type { Metadata } from "next";
import EventDetail from "./EventDetail";

export const metadata: Metadata = { title: "Event" };

export default async function Page({ params }: PageProps<"/events/[eventId]">) {
  const { eventId } = await params;
  return <EventDetail event={eventId} />;
}
