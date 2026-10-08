import Link from "next/link";

/** The back office's frame: internal tools, so no public navigation, giving prompt, or footer. */
export function StaffFrame({ children }: { children: React.ReactNode }) {
  return <>
    <a className="skipLink" href="#main">Skip to content</a>
    <header className="header">
      <Link href="/staff" className="brand"><span className="brandMark" aria-hidden>D</span><span>DAD staff</span></Link>
    </header>
    <main id="main">{children}</main>
  </>;
}
