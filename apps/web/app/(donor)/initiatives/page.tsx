import type { Metadata } from "next";
import { Page } from "@/components/donor/Page";
import { ContentUnavailable, InitiativeList } from "@/components/donor/PublicContent";
import { loadPublicContent } from "@/lib/publicContent";

export const metadata: Metadata = {
  title: "Planned initiatives",
  description:
    "Aid initiatives DAD is preparing with partner organizations and board approval for its April 2027 launch.",
};

export default async function InitiativesPage() {
  const content = await loadPublicContent();
  return (
    <Page wide eyebrow="Building toward April" title="Planned initiatives">
      <p className="lead">Preparing to launch in April 2027, with partner organizations and board approval.</p>
      {content ? <InitiativeList initiatives={content.initiatives} /> : <ContentUnavailable />}
    </Page>
  );
}
