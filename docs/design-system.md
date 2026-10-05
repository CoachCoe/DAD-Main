# R1 design foundations

The live web palette and corner radii are authored in
`apps/web/design/tokens.ts`, a framework-independent TypeScript module. The web adapter
maps those values to CSS custom properties on the server-rendered document; `globals.css`
consumes them. React Native can consume the same color strings and numeric radii without
copying CSS. Typography loading, responsive layout, focus treatment, and browser motion
preferences remain web concerns in the stylesheet and root layout.

The donor dashboard keeps Link and IRL counts at equal visual weight, with realized
dollars, streak, and founding recognition as the other three elements. Responsive grid
items must not inherit vertical document-flow spacing. Browser tests cover desktop,
mobile, and tablet layouts, automated WCAG 2.2 checks, keyboard confirmation, and partial
or failed data recovery. Automated checks do not establish full assistive-technology
conformance.

These shared values are an engineering foundation, not completion of the agency design
deliverables. The eleven donor app screens, native component implementations, interactive
prototype, responsive/redline specifications, approved Wallet artwork, store assets, and
manual accessibility conformance still require design and device acceptance. The R2 app
remains unbuilt; importing these tokens must not introduce recipient or partner surfaces.
