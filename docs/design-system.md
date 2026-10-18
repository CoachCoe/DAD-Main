# R1 design foundations

The live web palette, corner radii, spacing scale (`space`, 4–64 px), and type scale
(`text`, 12–36 px) are authored in `apps/web/design/tokens.ts`, a framework-independent
TypeScript module. The web adapter maps those values to CSS custom properties on the
server-rendered document; `globals.css` consumes them and holds no other colors. React
Native can consume the same color strings and unitless numbers without copying CSS. Typography loading, responsive layout, focus treatment, and browser motion
preferences remain web concerns in the stylesheet and root layout.

The donor dashboard keeps Link and IRL counts side by side at equal visual weight, with
realized dollars, streak, and founding recognition in a row of three beneath them. The
Founding Donor card appears only once earned; otherwise a single line explains how it is
earned. Responsive grid items must not inherit vertical document-flow spacing.

Buttons follow one hierarchy: `.button` is the primary brand action (one per view),
`.secondary` is the outlined default, `.quiet` is a text-weight action, and
`.secondary.danger` confirms destructive steps. Amount presets are outlined until
selected, and show the monthly amount and daily framing at equal weight. Browser tests cover desktop,
mobile, and tablet layouts, automated WCAG 2.2 checks, keyboard confirmation, and partial
or failed data recovery. Automated checks do not establish full assistive-technology
conformance.

Manual assistive-technology acceptance is deferred by product agreement, alongside
browser analytics. The future pass covers VoiceOver with iPhone/Safari, TalkBack with
Android/Chrome, and desktop keyboard-only navigation through giving, account claim and
sign-in, dashboard, preferences, recurring-gift changes, and critical staff tasks. Check
control names and reading order, focus movement and restoration, dialogs, validation,
loading/error announcements, and completion of each journey. Record device/browser
versions and results, fix failures, and retest; automated accessibility results alone
do not complete this acceptance.

These shared values are an engineering foundation, not completion of the agency design
deliverables. The eleven donor app screens, native component implementations, interactive
prototype, responsive/redline specifications, approved Wallet artwork, store assets, and
manual accessibility conformance still require design and device acceptance. The R2 app
remains unbuilt; importing these tokens must not introduce recipient or partner surfaces.
