import type { Metadata } from "next";
import EventEditor from "../[eventId]/EventEditor";

export const metadata: Metadata = { title: "New event" };

export default function Page() {
  return <EventEditor event={null} />;
}
