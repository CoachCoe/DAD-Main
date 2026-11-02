import { AnalyticsChoice } from "./AnalyticsChoice";
import { DonorHeader } from "./DonorHeader";
import { PreviewBanner } from "./PreviewBanner";
import { SiteFooter } from "./SiteFooter";

export function SiteFrame({ children }: { children: React.ReactNode }) {
  return (
    <>
      <a className="skipLink" href="#main">
        Skip to content
      </a>
      <PreviewBanner />
      <DonorHeader />
      <main id="main">{children}</main>
      <AnalyticsChoice />
      <SiteFooter />
    </>
  );
}
