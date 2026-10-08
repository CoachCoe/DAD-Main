package httpapi

import "net/http"

// auth names the credential a route acts on. The OpenAPI contract test checks
// each operation's documented security against it.
type auth string

const (
	noAuth     auth = "none"
	donorAuth  auth = "donor"  // browser cookie, or bearer token from the donor app
	staffAuth  auth = "staff"  // browser cookie only
	stripeAuth auth = "stripe" // Stripe-Signature over the raw body
)

type route struct {
	pattern string
	auth    auth
	handler http.HandlerFunc
}

// routes is the complete API surface. Reads are unguarded because they cannot
// change state. Mutations pass the Origin and rate-limit guard: donor and
// public routes admit the public origin, and the native donor app on donor
// account routes only; staff routes admit only the staff origin. The Stripe
// webhook is authenticated by its signature instead.
func (a *api) routes() []route {
	return []route{
		a.public("GET /health/live", a.live),
		a.public("GET /health/ready", a.ready),
		a.public("GET /donor/public/content", a.publicContent),
		{"POST /donor/stripe/webhook", stripeAuth, a.stripeWebhook},
		a.visitor("POST /donor/checkouts", a.beginCheckout),
		a.public("GET /donor/referrals/{code}", a.referrer),
		a.visitor("POST /donor/referrals/{code}", a.captureReferral),
		a.visitor("POST /donor/public/partner-signup", a.partnerSignup),

		a.donorEntry("POST /donor/login", a.login),
		a.donorEntry("POST /donor/claim", a.claim),
		a.donorEntry("POST /donor/email-change/confirm", a.confirmEmailChange),
		{"POST /donor/logout", donorAuth, a.guarded(a.Origin, accountLimit, true, a.logout)},
		a.donorWrite("POST /donor/logout-all", a.logoutAll),
		a.donorWrite("POST /donor/me/email-change", a.requestEmailChange),

		a.donorRead("GET /donor/me/dashboard", a.dashboard),
		a.donorRead("GET /donor/me/profile", a.profile),
		a.donorWrite("POST /donor/me/preferences", a.savePreferences),
		a.donorRead("GET /donor/me/sharing", a.sharing),
		a.donorWrite("POST /donor/me/sharing", a.saveSharing),
		a.donorRead("GET /donor/me/referral-code.svg", a.referralCode),
		a.donorRead("GET /donor/me/gifts", a.gifts),
		a.donorRead("GET /donor/me/receipts/{gift_id}", a.receipt),
		a.donorRead("GET /donor/me/annual-summary/{year}", a.annualSummary),
		a.donorRead("GET /donor/me/wallet", a.walletProviders),
		a.donorWrite("POST /donor/me/wallet/apple", a.applePass),
		a.donorWrite("POST /donor/me/wallet/google", a.googlePass),

		a.donorRead("GET /donor/me/recurring", recurringRead(a, RecurringSource.Details)),
		a.donorRead("GET /donor/me/recurring/amount", recurringRead(a, RecurringSource.AmountOptions)),
		a.donorRead("GET /donor/me/recurring/pause", recurringRead(a, RecurringSource.PauseOptions)),
		a.donorRead("GET /donor/me/recurring/resume", recurringRead(a, RecurringSource.ResumeOptions)),
		a.donorRead("GET /donor/me/payment-method", recurringRead(a, RecurringSource.PaymentMethodStatus)),
		a.donorWrite("POST /donor/me/recurring/cancel", recurringCommand(a, cancelRecurring)),
		a.donorWrite("POST /donor/me/recurring/pause", recurringCommand(a, pauseRecurring)),
		a.donorWrite("POST /donor/me/recurring/resume", recurringCommand(a, resumeRecurring)),
		a.donorWrite("POST /donor/me/recurring/amount", recurringCommand(a, changeAmount)),
		a.donorWrite("POST /donor/me/payment-method/setup", a.beginPaymentUpdate),

		a.staffChallenge("POST /donor/staff/sign-in/options", a.beginStaffSignIn),
		a.staffEntry("POST /donor/staff/sign-in", a.staffSignIn),
		a.staffChallenge("POST /donor/staff/activate/options", a.beginStaffActivation),
		a.staffEntry("POST /donor/staff/activate", a.staffActivate),
		{"POST /donor/staff/sign-out", staffAuth, a.guarded(a.StaffOrigin, staffLimit, false, a.staffSignOut)},
		a.staffRead("GET /donor/staff/me", a.staffMe),
		a.staffRead("GET /donor/staff/accounts", a.staffAccounts),
		a.staffWrite("POST /donor/staff/accounts", a.inviteStaff),
		a.staffWrite("POST /donor/staff/accounts/{staff_id}/disable", a.changeStaffAccess("disable")),
		a.staffWrite("POST /donor/staff/accounts/{staff_id}/recovery", a.changeStaffAccess("recovery")),
		a.staffWrite("POST /donor/staff/accounts/{staff_id}/role", a.changeStaffAccess("role")),

		a.staffRead("GET /donor/staff/donors", a.searchDonors),
		a.staffRead("GET /donor/staff/donors/{donor_id}", a.donorDetail),
		a.staffRead("GET /donor/staff/donors/{donor_id}/messages", a.donorMessages),
		a.staffRead("GET /donor/staff/donors/{donor_id}/actions", a.donorActions),
		a.staffRead("GET /donor/staff/donors/{donor_id}/actions/pending", a.pendingAction),
		a.staffRead("GET /donor/staff/audit", a.auditEvents),
		a.staffRead("GET /donor/staff/enrolments", a.enrolments),
		a.staffWrite("POST /donor/staff/donors/{donor_id}/claim-email", a.requestClaim),
		a.staffWrite("POST /donor/staff/messages/{message_id}/resend", a.resendMessage),

		a.staffRead("GET /donor/staff/donors/{donor_id}/gifts/{gift_id}/refund", a.refundQuote),
		a.staffWrite("POST /donor/staff/donors/{donor_id}/gifts/{gift_id}/refund", a.refund),
		a.staffRead("GET /donor/staff/donors/{donor_id}/cancellation", a.cancellationQuote),
		a.staffWrite("POST /donor/staff/donors/{donor_id}/cancellation", a.cancelSubscription),

		a.staffRead("GET /donor/staff/exports", a.exportDatasets),
		a.staffRead("GET /donor/staff/exports/{file}", a.export),

		a.staffRead("GET /donor/staff/content", a.contentInventory),
		a.staffRead("GET /donor/staff/partners", a.partnerInquiries),
		a.staffWrite("POST /donor/staff/content/sources", a.uploadSource),
		a.staffRead("GET /donor/staff/content/sources/{source_id}", a.downloadSource),
		a.staffWrite("POST /donor/staff/content/publish", a.publish),
	}
}

// public routes are anonymous reads.
func (a *api) public(pattern string, handler http.HandlerFunc) route {
	return route{pattern, noAuth, handler}
}

// visitor routes are anonymous browser mutations on the public site.
func (a *api) visitor(pattern string, handler http.HandlerFunc) route {
	return route{pattern, noAuth, a.guarded(a.Origin, visitorLimit, false, handler)}
}

// donorEntry routes exchange an emailed proof, from a browser or the app.
func (a *api) donorEntry(pattern string, handler http.HandlerFunc) route {
	return route{pattern, noAuth, a.guarded(a.Origin, authLimit, true, handler)}
}

func (a *api) donorRead(pattern string, handler sessionHandler) route {
	return route{pattern, donorAuth, signedIn(donorSession, handler)}
}

func (a *api) donorWrite(pattern string, handler sessionHandler) route {
	return route{pattern, donorAuth, a.guarded(a.Origin, accountLimit, true, signedIn(donorSession, handler))}
}

// staffChallenge routes start passkey ceremonies. A page asks for one before
// anyone acts, so they take the staff budget; answering one takes the
// stricter sign-in budget.
func (a *api) staffChallenge(pattern string, handler http.HandlerFunc) route {
	return route{pattern, noAuth, a.guarded(a.StaffOrigin, staffLimit, false, handler)}
}

// staffEntry routes answer passkey ceremonies; the back office is browser-only.
func (a *api) staffEntry(pattern string, handler http.HandlerFunc) route {
	return route{pattern, noAuth, a.guarded(a.StaffOrigin, authLimit, false, handler)}
}

func (a *api) staffRead(pattern string, handler sessionHandler) route {
	return route{pattern, staffAuth, signedIn(a.staffSession, handler)}
}

func (a *api) staffWrite(pattern string, handler sessionHandler) route {
	return route{pattern, staffAuth, a.guarded(a.StaffOrigin, staffLimit, false, signedIn(a.staffSession, handler))}
}

// sessionHandler serves a signed-in request. The session is only a claim: the
// store authenticates it, and for staff checks the route's named permission
// against current staff state.
type sessionHandler func(w http.ResponseWriter, r *http.Request, session string)

// signedIn rejects requests without a session before they reach storage.
func signedIn(session func(*http.Request) string, next sessionHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := session(r)
		if token == "" {
			writeError(w, r, http.StatusUnauthorized, "Sign in required", nil)
			return
		}
		next(w, r, token)
	}
}
