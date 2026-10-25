package store

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"dad/donor-api/internal/payments"
	"dad/donor-api/internal/push"
	"dad/donor-api/internal/recognition"
	"dad/donor-api/internal/store/queries"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrPushUnconfigured refuses a device registration for a platform this
// deployment cannot deliver to, so no notification is queued for it.
var ErrPushUnconfigured = errors.New("push notifications are not configured for this platform")

// ErrPushToken refuses a token that is not the form its platform issues.
var ErrPushToken = errors.New("invalid push token")

var (
	// APNs device tokens are hex; Apple documents them as variable length.
	apnsToken = regexp.MustCompile(`^(?:[0-9a-f]{2}){32,100}$`)
	// FCM documents no length; tokens run to about 200 characters.
	fcmToken = regexp.MustCompile(`^[A-Za-z0-9_:-]+$`)
)

// pushLifetime is how long a notification stays worth showing. One that
// could not be sent by then, or that waited on an unconfigured provider, is
// dropped rather than shown late.
const pushLifetime = 24 * time.Hour

// PushDevice is the installation behind the caller's app session.
type PushDevice struct {
	Platform    string // push.IOS or push.Android
	Token       string
	Environment string // SANDBOX or PRODUCTION for iOS, where it defaults to PRODUCTION
}

// PushRegistration is the caller's session's push registration.
type PushRegistration struct {
	Registered  bool    `json:"registered"`
	Platform    *string `json:"platform"`
	Environment *string `json:"environment"`
}

type PushService struct {
	DB        *pgxpool.Pool
	Platforms []string // those with a configured provider
}

// normalize validates the device's token for its platform. APNs tokens are
// hex, which apps may print in either case.
func (d PushDevice) normalize() (PushDevice, error) {
	switch d.Platform {
	case push.IOS:
		d.Token = strings.ToLower(d.Token)
		if d.Environment == "" {
			d.Environment = "PRODUCTION"
		}
		if !apnsToken.MatchString(d.Token) || (d.Environment != "SANDBOX" && d.Environment != "PRODUCTION") {
			return d, ErrPushToken
		}
	case push.Android:
		if len(d.Token) < 32 || len(d.Token) > 1024 || !fcmToken.MatchString(d.Token) || d.Environment != "" {
			return d, ErrPushToken
		}
	default:
		return d, ErrPushToken
	}
	return d, nil
}

// RegisterDevice records device's token on the caller's app session,
// replacing any it held. A token held by another session, the donor's or
// anyone's, moves here: it names an installation, which signed in again.
func (s PushService) RegisterDevice(ctx context.Context, token string, device PushDevice) (PushRegistration, error) {
	device, err := device.normalize()
	if err != nil {
		return PushRegistration{}, err
	}
	if !slices.Contains(s.Platforms, device.Platform) {
		return PushRegistration{}, ErrPushUnconfigured
	}
	return sessionWrite(ctx, s.DB, token, func(q *queries.Queries, user signedIn, _ time.Time) (PushRegistration, error) {
		if err := q.ReleasePushToken(ctx, queries.ReleasePushTokenParams{PushToken: text(device.Token), ID: user.session}); err != nil {
			return PushRegistration{}, err
		}
		environment := pgtype.Text{String: device.Environment, Valid: device.Environment != ""}
		set, err := q.SetPushToken(
			ctx,
			queries.SetPushTokenParams{
				ID:              user.session,
				Platform:        text(device.Platform),
				PushToken:       text(device.Token),
				PushEnvironment: environment,
			},
		)
		if err != nil {
			return PushRegistration{}, err
		}
		// A browser session's cookie presented as a bearer token is no app.
		if set == 0 {
			return PushRegistration{}, ErrDenied
		}
		return PushRegistration{Registered: true, Platform: &device.Platform, Environment: textPointer(environment)}, nil
	})
}

// UnregisterDevice removes the caller's session's push token, if any.
func (s PushService) UnregisterDevice(ctx context.Context, token string) (PushRegistration, error) {
	return sessionWrite(ctx, s.DB, token, func(q *queries.Queries, user signedIn, _ time.Time) (PushRegistration, error) {
		return PushRegistration{}, q.ClearPushToken(ctx, user.session)
	})
}

// queuePushIntents queues the notifications the projection p calls for.
func queuePushIntents(ctx context.Context, q *queries.Queries, p projection, now time.Time) error {
	params := queries.QueuePushIntentsParams{
		DonorID:     p.DonorID,
		AsOf:        timestamp(now),
		ExpiresAt:   timestamp(now.Add(pushLifetime)),
		RecentSince: timestamp(now.Add(-pushLifetime)),
	}
	if p.CurrentStreak.Valid && p.streakSince != nil && recognition.Milestone(int64(p.CurrentStreak.Int32)) {
		params.Milestone = pgtype.Int4{Int32: p.CurrentStreak.Int32, Valid: true}
		params.RunStartedAt = timestamp(*p.streakSince)
	}
	return q.QueuePushIntents(ctx, params)
}

// pushSuppression returns why a notification should no longer be sent, if
// it should not: it expired, the donor turned its category off, or the fact
// it announces no longer holds.
func pushSuppression(
	ctx context.Context,
	q *queries.Queries,
	job queries.DonorPushNotification,
	donor queries.Donor,
	now time.Time,
) (string, error) {
	if !now.Before(job.ExpiresAt.Time) {
		return "expired", nil
	}
	if donor.Disabled {
		return "donor_disabled", nil
	}
	pref, err := readPreferences(ctx, q, donor.ID)
	if err != nil {
		return "", err
	}
	data, err := payments.DecodeObject(job.Context)
	if err != nil {
		return "", err
	}
	switch job.Category {
	case "REFERRAL_CONVERTED":
		if !pref.PushReferralConverted {
			return "opted_out", nil
		}
		return referralLapsed(ctx, q, data, donor)
	case "STREAK_AT_RISK":
		if !pref.PushStreakAtRisk {
			return "opted_out", nil
		}
		_, reason, err := failureResolved(ctx, q, data, donor, now)
		return reason, err
	case "STREAK_MILESTONE":
		if !pref.PushStreakMilestone {
			return "opted_out", nil
		}
		months, err := data.Int("months")
		if err != nil {
			return "", payments.ErrFacts
		}
		current, err := q.Projection(ctx, donor.ID)
		if err != nil {
			return "", err
		}
		if int64(current.CurrentStreak.Int32) < months {
			return "streak_ended", nil
		}
		return "", nil
	case "STREAK_LAPSED":
		if !pref.PushStreakLapsed {
			return "opted_out", nil
		}
		// The lapse holds unless the donor recovered: if the failed period that
		// ended the run is no longer FAILED (payment recovered), the lapse news
		// is stale. A cancelled subscription or ended grace does not un-lapse.
		_, reason, err := failureResolved(ctx, q, data, donor, now)
		if err != nil {
			return "", err
		}
		if reason == "payment_recovered" || reason == "subscription_cancelled" {
			return reason, nil
		}
		return "", nil
	case "BADGE_EARNED":
		if !pref.PushBadgeEarned {
			return "opted_out", nil
		}
		// The earn is permanent, but an admin may have revoked it between queue
		// and send (B-4); a revoked badge reads as unearned, so do not announce it.
		code := data.Text("code")
		if code == "" {
			return "", payments.ErrFacts
		}
		earned, err := q.BadgeEarned(ctx, queries.BadgeEarnedParams{DonorID: donor.ID, Code: code})
		if err != nil {
			return "", err
		}
		if !earned {
			return "badge_revoked", nil
		}
		return "", nil
	case "EVENT_PROMOTED":
		// Event alerts ride on the volunteer opt-in (E-1). Expiry (the event
		// start) is already handled above; there is no further fact to recheck.
		if !pref.Volunteer {
			return "opted_out", nil
		}
		return "", nil
	case "EVENT_REMINDER":
		// Same gate as other event alerts; expiry (event start) handled above.
		if !pref.Volunteer {
			return "opted_out", nil
		}
		return "", nil
	}
	return "", payments.ErrFacts
}

// pushContent is what a notification says. It names no one and no amount:
// notifications transit Apple's and Google's servers and show on lock
// screens, so the app fetches any detail through the API.
func pushContent(job queries.DonorPushNotification) (push.Notification, error) {
	data, err := payments.DecodeObject(job.Context)
	if err != nil {
		return push.Notification{}, err
	}
	n := push.Notification{ID: job.ID.String(), Category: job.Category, Path: dashboardPath, Expires: job.ExpiresAt.Time}
	switch job.Category {
	case "REFERRAL_CONVERTED":
		channel := map[string]string{"LINK": "a Link Referral", "IRL": "an IRL Referral"}[data.Text("kind")]
		if channel == "" {
			return n, payments.ErrFacts
		}
		n.Title = "Your invitation worked"
		n.Body = "Someone you invited through " + channel + " just made their first test gift."
	case "STREAK_AT_RISK":
		n.Title, n.Path = "Your giving streak is at risk", paymentMethodPath
		n.Body = "Your monthly test payment was declined. Update your payment method to keep your streak going."
	case "STREAK_MILESTONE":
		months, err := data.Int("months")
		if err != nil || !recognition.Milestone(months) {
			return n, payments.ErrFacts
		}
		span := strconv.FormatInt(months, 10) + " months"
		if months == 12 {
			span = "1 year"
		} else if months > 12 {
			span = strconv.FormatInt(months/12, 10) + " years"
		}
		n.Title = span + " in a row"
		n.Body = "You have given every month for " + span + ". Thank you for keeping your test gifts going."
	case "EVENT_PROMOTED":
		n.Title = "A spot opened up"
		n.Body = "You are off the waitlist — your spot at an upcoming event is confirmed. Open the app for details."
	case "EVENT_REMINDER":
		n.Title = "Your event is coming up"
		if data.Text("window") == "2h" {
			n.Body = "An event you reserved starts in about 2 hours. Open the app for the details."
		} else {
			n.Body = "An event you reserved is tomorrow. Open the app for the details."
		}
	case "STREAK_LAPSED":
		n.Title, n.Path = "Your giving streak ended", paymentMethodPath
		n.Body = "Your monthly test payment did not recover in time, so your streak reset. Start a new streak whenever you are ready."
	case "BADGE_EARNED":
		if data.Text("code") == "" {
			return n, payments.ErrFacts
		}
		n.Title = "You earned a new badge"
		n.Body = "You just earned a badge for your giving. Open the app to see it."
	default:
		return n, payments.ErrFacts
	}
	return n, nil
}

type PushWorker struct {
	DB        *pgxpool.Pool
	Providers map[string]push.Provider
}

// pushTarget is one device a claimed notification goes to.
type pushTarget struct {
	session  pgtype.UUID
	provider push.Provider
	device   push.Device
}

type pushJob struct {
	queries.DonorPushNotification
	note    push.Notification
	targets []pushTarget
}

// pushOutcome is what the providers said about one delivery attempt.
type pushOutcome struct {
	sent     []pgtype.UUID
	dropped  []pushTarget
	retry    bool
	after    time.Duration
	rejected bool
}

// DeliverNext sends the next due push notification to each of its donor's
// devices it has not yet reached. It reports whether it found work.
func (w PushWorker) DeliverNext(ctx context.Context, now time.Time) (bool, error) {
	job, worked, err := w.claim(ctx, now)
	if err != nil || job == nil {
		return worked, err
	}
	// The donor lock is released before the providers are called; the claim
	// keeps other workers off the job, and a resend after a crash replaces
	// the notification on the device rather than repeating it.
	var outcome pushOutcome
	for _, target := range job.targets {
		err := target.provider.Send(ctx, target.device, job.note)
		var unavailable push.Unavailable
		switch {
		case err == nil:
			outcome.sent = append(outcome.sent, target.session)
		case errors.Is(err, push.ErrUnregistered):
			outcome.dropped = append(outcome.dropped, target)
		case errors.Is(err, push.ErrRejected):
			outcome.rejected = true
		case errors.As(err, &unavailable):
			outcome.retry, outcome.after = true, max(outcome.after, unavailable.After)
		default:
			outcome.retry = true
		}
	}
	return true, w.record(ctx, job, outcome, now)
}

// claim selects the next due notification and, unless it should no longer
// be sent or has no device to reach, reserves it for one delivery attempt.
// It returns no job when the one found was finished or failed instead.
func (w PushWorker) claim(ctx context.Context, now time.Time) (*pushJob, bool, error) {
	var job *pushJob
	worked := false
	err := pgx.BeginFunc(ctx, w.DB, func(tx pgx.Tx) error {
		q := queries.New(tx)
		row, err := q.NextPushNotification(ctx, timestamp(now))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		worked = true
		donor, err := q.LockDonor(ctx, row.DonorID)
		if err != nil {
			return err
		}
		reason, err := pushSuppression(ctx, q, row, donor, now)
		var note push.Notification
		if err == nil && reason == "" {
			note, err = pushContent(row)
		}
		if err != nil {
			if !messageDataError(err) {
				return err
			}
			return q.FailPushNotification(
				ctx,
				queries.FailPushNotificationParams{
					ID:          row.ID,
					LastError:   text("InvalidNotification"),
					AvailableAt: messageRetryAt(now, row.Attempts+1),
					Attempts:    row.Attempts + 1,
				},
			)
		}
		if reason != "" {
			return completePush(ctx, q, row.ID, now, reason)
		}
		devices, err := q.PushTargets(ctx, queries.PushTargetsParams{DonorID: row.DonorID, AsOf: timestamp(now), SentTo: row.SentTo})
		if err != nil {
			return err
		}
		claimed := &pushJob{DonorPushNotification: row, note: note}
		for _, d := range devices {
			if provider := w.Providers[d.Platform.String]; provider != nil {
				claimed.targets = append(
					claimed.targets,
					pushTarget{d.ID, provider, push.Device{Token: d.PushToken.String, Environment: d.PushEnvironment.String}},
				)
			}
		}
		switch {
		case len(devices) == 0 && len(row.SentTo) == 0:
			return completePush(ctx, q, row.ID, now, "no_device")
		case len(devices) == 0:
			return completePush(ctx, q, row.ID, now, "")
		case len(claimed.targets) == 0:
			return completePush(ctx, q, row.ID, now, "push_unconfigured")
		}
		job = claimed
		return q.ClaimPushNotification(ctx, queries.ClaimPushNotificationParams{ID: row.ID, AvailableAt: timestamp(now.Add(deliveryLease))})
	})
	if err != nil {
		return nil, false, err
	}
	return job, worked, nil
}

// record applies one attempt's outcome: reached sessions are not sent to
// again, rejected tokens are dropped, and the job completes, retries with
// backoff, or parks for an operator when a provider refused its
// configuration.
func (w PushWorker) record(ctx context.Context, job *pushJob, outcome pushOutcome, now time.Time) error {
	return pgx.BeginFunc(ctx, w.DB, func(tx pgx.Tx) error {
		q := queries.New(tx)
		if len(outcome.sent) > 0 {
			if err := q.RecordPushSent(ctx, queries.RecordPushSentParams{ID: job.ID, Sessions: outcome.sent}); err != nil {
				return err
			}
		}
		for _, target := range outcome.dropped {
			if err := q.DropPushToken(ctx, queries.DropPushTokenParams{ID: target.session, PushToken: text(target.device.Token)}); err != nil {
				return err
			}
		}
		attempt := job.Attempts + 1
		switch {
		case outcome.rejected:
			return q.FailPushNotification(
				ctx,
				queries.FailPushNotificationParams{
					ID:          job.ID,
					LastError:   text("ProviderRejected"),
					AvailableAt: timestamp(now),
					Attempts:    maxDeliveryAttempts,
				},
			)
		case outcome.retry:
			retryAt := messageRetryAt(now, attempt)
			if later := now.Add(outcome.after); later.After(retryAt.Time) {
				retryAt = timestamp(later)
			}
			return q.FailPushNotification(
				ctx,
				queries.FailPushNotificationParams{ID: job.ID, LastError: text("DeliveryError"), AvailableAt: retryAt, Attempts: attempt},
			)
		case len(outcome.sent) == 0 && len(job.SentTo) == 0:
			return completePush(ctx, q, job.ID, now, "unregistered")
		}
		return completePush(ctx, q, job.ID, now, "")
	})
}

func completePush(ctx context.Context, q *queries.Queries, id pgtype.UUID, now time.Time, reason string) error {
	return q.CompletePushNotification(
		ctx,
		queries.CompletePushNotificationParams{
			ID:               id,
			CompletedAt:      timestamp(now),
			SuppressedReason: pgtype.Text{String: reason, Valid: reason != ""},
		},
	)
}
