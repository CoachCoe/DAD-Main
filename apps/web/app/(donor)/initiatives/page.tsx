import type { Metadata } from "next";
import { Page } from "@/components/donor/Page";
import { ContentUnavailable, InitiativeList } from "@/components/donor/PublicContent";
import { launchMonthLabel } from "@/lib/launch";
import { loadInitiatives } from "@/lib/publicContent";

export const metadata: Metadata = {
  title: "Planned initiatives",
  description: `Aid initiatives DAD is preparing with partner organizations and board approval for its ${launchMonthLabel()} launch.`,
};

export default async function InitiativesPage() {
  const initiatives = await loadInitiatives();
  return (
    <Page wide eyebrow={`Building toward ${launchMonthLabel()}`} title="Planned initiatives">
      <p className="lead">
        Preparing to launch in {launchMonthLabel()}, with partner organizations and board approval.
      </p>
      {initiatives ? <InitiativeList initiatives={initiatives} /> : <ContentUnavailable />}
    </Page>
  );
}
