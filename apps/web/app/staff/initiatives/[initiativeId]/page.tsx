import type { Metadata } from "next";
import InitiativeEditor from "./InitiativeEditor";

export const metadata: Metadata = { title: "Initiative" };

export default async function Page({ params }: PageProps<"/staff/initiatives/[initiativeId]">) {
  const { initiativeId } = await params;
  return <InitiativeEditor initiative={initiativeId} />;
}
