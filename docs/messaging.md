# R1 messaging contract

Messages currently identify the environment as a sandbox. They are not approved tax
receipts. Verify the receiving entity and EIN, obtain approved wording, and test the
configured provider before enabling live donations. SMTP delivery is at least once.

| Message | Trigger and audience | Preference treatment | Implementation |
|---|---|---|---|
| Gift receipt | Successful gift; giving donor | Essential financial record; no marketing content | `internal/store/receipts.go` and `messages.go` |
| Annual summary | Prior calendar year has gifts; giving donor | Essential financial record | `internal/store/receipts.go` and `outbox.go` |
| Payment recovery | Failed renewal, then days 7 and 13; giving donor | Essential payment notice; stop after recovery or cancellation | `internal/store/messages.go` and `outbox.go` |
| Referral converted | First qualifying referred gift; direct referrer | Optional; recheck referral-email preference before sending | `internal/store/messages.go` |
| Account claim/sign-in | Requested access or initial checkout claim; address owner | Essential authentication; single-use link, 15-minute expiry | `internal/store/email.go` |
| Email change | Signed-in request, then current-address confirmation; current/new address respectively | Essential security confirmation; two single-use proofs, each valid 15 minutes | `internal/store/email_change_delivery.go` |
| Passkey added | A passkey is registered; the account's address at send time | Essential security notice; names the passkey and when it was added, links to the profile, and is sent even if the passkey is removed first | `internal/store/donor_passkeys.go` and `messages.go` |
| Enrolment confirmation | Program submission; enrolled person | R2 activation only; final preference classification requires approval | Prepared copy below; no R1 sending trigger |

Implementation paths are relative to `apps/donor-api`. Marketing consent is independent
of referral notices. Treating financial and authentication messages as essential is the
current product rule; legal must approve that classification before launch. Staff resends
require permission and an audit trail. Delivery history distinguishes queued, completed,
failed, and suppressed messages; queued does not mean received.

## App notifications

The donor app receives push notifications through APNs and FCM
(`internal/store/push.go`), queued with the same facts as the messages above. Each is
optional, starts on, and is rechecked against current preferences before sending. They
name no one and no amount, and open the given app path.

| Notification | Trigger and audience | Text | Opens |
|---|---|---|---|
| Referral converted | First qualifying referred gift; direct referrer | "Your invitation worked" / "Someone you invited through a Link Referral (an IRL Referral) just made their first test gift." | `/account/dashboard` |
| Streak at risk | Failed latest renewal while the streak is at risk, once per period; giving donor | "Your giving streak is at risk" / "Your monthly test payment was declined. Update your payment method to keep your streak going." | `/account/payment-method` |
| Streak milestone | Streak reaches 3 or 6 months, a year, or a further year, once per run; giving donor | "6 months in a row" / "You have given every month for 6 months. Thank you for keeping your test gifts going." | `/account/dashboard` |

A notification not sent within 24 hours is dropped rather than shown late.

## Prepared enrolment confirmation

Subject: Your enrolment in {{program_name}}

Hello {{recipient_name}},

We have received your enrolment in {{program_name}}.

This message confirms your enrolment. It does not promise funds or any particular
amount of support. DAD will contact you about the program's next steps.

If you did not submit this enrolment, contact {{support_contact}}.

Dynamic Aid Delivery

This template is held for R2. Program rules, recipient identity, contact details, and
notification preferences must be defined before connecting it to an enrolment workflow.
Do not collect recipient records or activate this trigger in R1.
