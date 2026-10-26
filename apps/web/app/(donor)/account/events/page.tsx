import type { Metadata } from "next";
import Events from "./Events";

export const metadata: Metadata = { title: "Events near you" };

export default function Page() {
  return <Events />;
}
