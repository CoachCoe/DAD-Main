import Link from "next/link";

type Props = {
  title: React.ReactNode;
  eyebrow?: string;
  back?: { href: string; label: string };
  wide?: boolean;
  className?: string;
  ref?: React.Ref<HTMLElement>;
  children?: React.ReactNode;
};

/** The single content surface every donor and staff screen sits on. */
export function Page({ title, eyebrow, back, wide = false, className, ref, children }: Props) {
  return <div className={wide ? "page" : "page narrow"}>
    <section ref={ref} className={className ? `panel ${className}` : "panel"}>
      {back && <Link className="back" href={back.href}><span aria-hidden>←</span>{back.label}</Link>}
      {eyebrow && <span className="eyebrow">{eyebrow}</span>}
      <h1>{title}</h1>
      {children}
    </section>
  </div>;
}
