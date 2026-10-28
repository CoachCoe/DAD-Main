import type { Metadata } from "next";
import EventEditor from "./EventEditor";

export const metadata: Metadata = { title: "Event" };

export default async function Page({ params }: PageProps<"/staff/events/[eventId]">) {
  const { eventId } = await params;
  return <EventEditor event={eventId} />;
}
