import type { Metadata } from "next";
import InitiativeEditor from "../[initiativeId]/InitiativeEditor";

export const metadata: Metadata = { title: "New initiative" };

export default function Page() {
  return <InitiativeEditor initiative={null} />;
}
