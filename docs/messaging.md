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
| Enrolment confirmation | Program submission; enrolled person | R2 activation only; final preference classification requires approval | Prepared copy below; no R1 sending trigger |

Implementation paths are relative to `apps/donor-api`. Marketing consent is independent
of referral notices. Treating financial and authentication messages as essential is the
current product rule; legal must approve that classification before launch. Staff resends
require permission and an audit trail. Delivery history distinguishes queued, completed,
failed, and suppressed messages; queued does not mean received.

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
