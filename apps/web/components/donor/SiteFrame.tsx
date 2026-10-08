import { DonorHeader } from "./DonorHeader";
import { SiteFooter } from "./SiteFooter";

export function SiteFrame({ children }: { children: React.ReactNode }) {
  return <>
    <a className="skipLink" href="#main">Skip to content</a>
    <DonorHeader />
    <main id="main">{children}</main>
    <SiteFooter />
  </>;
}
