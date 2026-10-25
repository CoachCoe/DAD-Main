package store

import (
	"context"
	"time"

	"dad/donor-api/internal/store/queries"
	"github.com/jackc/pgx/v5"
)

// ReportingService serves the PRD §4 success metrics as a staff read. Every
// number comes from committed server facts (gifts, billing periods, refunds,
// and the donor metric projection); nothing is read from the browser
// analytics_events stream, whose collection docs/analytics.md defers by product
// agreement. Metrics that depend on that stream are reported unavailable with a
// reason, never as a fabricated zero.
type ReportingService struct {
	Staff *StaffService
}

// reportReadPermission gates the report. It is held by every staff role (see
// rolePermissions): the org-wide KPIs inform each operator's area, and the read
// grants no mutation.
const reportReadPermission = "report.read"

// shareProxyUnavailable explains why the seven-day share rate is not computed:
// it requires the browser share-handoff observation that analytics.md defers.
const shareProxyUnavailable = "browser share-action collection is not enabled (deferred by product agreement); " +
	"this metric cannot be computed from server facts"

// Metric is one §4 number with its availability. A metric the server cannot
// compute yet carries Available=false and a Reason, so the report never shows a
// fabricated zero for a deferred browser metric.
type Metric struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	Value     int64  `json:"value"`
}

// SourceConversions is the first-gift conversion count and gross collected
// dollars for one attribution source (DIRECT/LINK/IRL), split by gift
// frequency so first-gift donor mix is visible without conflating gifts and
// donors.
type SourceConversions struct {
	Source     string `json:"source"`
	Frequency  string `json:"frequency"`
	Donors     int64  `json:"donors"`
	GrossMinor int64  `json:"gross_minor"`
}

// GiftMix is the successful gift count and collected dollars for one frequency.
type GiftMix struct {
	Frequency      string `json:"frequency"`
	Gifts          int64  `json:"gifts"`
	CollectedMinor int64  `json:"collected_minor"`
}

// ViralCoefficient reports gross and currently-qualified conversions over the
// referring cohort separately, so refund behaviour cannot silently rewrite the
// acquisition number. The ratio is left to the reader (cohort may be zero).
type ViralCoefficient struct {
	Cohort               int64 `json:"cohort"`
	GrossConversions     int64 `json:"gross_conversions"`
	QualifiedConversions int64 `json:"qualified_conversions"`
}

// InvoluntaryChurn is the lost scheduled value from failed renewals past the
// retry deadline, with the recovered count/value against that same cohort.
type InvoluntaryChurn struct {
	FailedPeriods    int64 `json:"failed_periods"`
	LostMinor        int64 `json:"lost_minor"`
	RecoveredPeriods int64 `json:"recovered_periods"`
	RecoveredMinor   int64 `json:"recovered_minor"`
}

// Retention is the month-three / month-six recurring retention: donors who
// reached a milestone (best streak) and those still at it (current streak).
type Retention struct {
	ReachedM3  int64 `json:"reached_m3"`
	RetainedM3 int64 `json:"retained_m3"`
	ReachedM6  int64 `json:"reached_m6"`
	RetainedM6 int64 `json:"retained_m6"`
}

// TimeToFirstGift is the median seconds from account creation to first verified
// collection, with the sample size so the median carries its denominator.
type TimeToFirstGift struct {
	Sample        int64 `json:"sample"`
	MedianSeconds int64 `json:"median_seconds"`
}

// Report is the §4 success-metrics snapshot over a half-open [start, end)
// window. PeriodStart/End and ObservationCutoff are echoed so the numbers carry
// their window and the as-of time (analytics.md requires periods to state their
// observation cutoff and timezone; all timestamps are UTC). CoverageNote
// discloses the committed-facts coverage boundary.
type Report struct {
	PeriodStart       time.Time           `json:"period_start"`
	PeriodEnd         time.Time           `json:"period_end"`
	ObservationCutoff time.Time           `json:"observation_cutoff"`
	Timezone          string              `json:"timezone"`
	CoverageNote      string              `json:"coverage_note"`
	FoundingDonors    int64               `json:"founding_donors"`
	FirstGiftBySource []SourceConversions `json:"first_gift_by_source"`
	GiftMix           []GiftMix           `json:"gift_mix"`
	ViralCoefficient  ViralCoefficient    `json:"viral_coefficient"`
	ReferralLinkMinor int64               `json:"referral_link_minor"`
	ReferralIRLMinor  int64               `json:"referral_irl_minor"`
	InvoluntaryChurn  InvoluntaryChurn    `json:"involuntary_churn"`
	TimeToFirstGift   TimeToFirstGift     `json:"time_to_first_gift"`
	Retention         Retention           `json:"retention"`
	SevenDayShareRate Metric              `json:"seven_day_share_rate"`
}

// coverageNote is the fixed disclosure that server facts, not browser events,
// back this report, with the committed-facts coverage boundary.
const coverageNote = "Computed from committed server facts (gifts, billing periods, refunds, projection) as of the " +
	"observation cutoff. Browser-dependent metrics are unavailable until analytics collection is approved."

// Report computes the §4 metrics over [start, end). end also serves as the
// observation cutoff: the involuntary-churn deadline (due_at + 14 days) is
// measured against it, so a failed renewal still inside its retry window is not
// yet counted as lost. It is a pure read, so one snapshotted read transaction
// gives every metric a consistent view.
func (s *ReportingService) Report(ctx context.Context, token string, start, end time.Time) (Report, error) {
	if end.Before(start) || end.Equal(start) {
		return Report{}, ErrStaffInput
	}
	start, end = start.UTC(), end.UTC()
	return staffRead(ctx, s.Staff, token, reportReadPermission, func(tx pgx.Tx, _ queries.DonorStaff, _ time.Time) (Report, error) {
		q := queries.New(tx)
		startTS, endTS := timestamp(start), timestamp(end)
		report := Report{
			PeriodStart:       start,
			PeriodEnd:         end,
			ObservationCutoff: end,
			Timezone:          "UTC",
			CoverageNote:      coverageNote,
			SevenDayShareRate: Metric{Available: false, Reason: shareProxyUnavailable},
		}

		founding, err := q.ReportFoundingDonors(
			ctx,
			queries.ReportFoundingDonorsParams{FoundingAwardedAt: startTS, FoundingAwardedAt_2: endTS},
		)
		if err != nil {
			return Report{}, err
		}
		report.FoundingDonors = founding

		sources, err := q.ReportFirstGiftBySource(ctx, queries.ReportFirstGiftBySourceParams{PaidAt: startTS, PaidAt_2: endTS})
		if err != nil {
			return Report{}, err
		}
		report.FirstGiftBySource = make([]SourceConversions, 0, len(sources))
		for _, row := range sources {
			source, _ := row.Source.(string)
			report.FirstGiftBySource = append(report.FirstGiftBySource, SourceConversions{
				Source:     source,
				Frequency:  row.Frequency,
				Donors:     row.Donors,
				GrossMinor: row.GrossMinor,
			})
		}

		mix, err := q.ReportGiftMix(ctx, queries.ReportGiftMixParams{PaidAt: startTS, PaidAt_2: endTS})
		if err != nil {
			return Report{}, err
		}
		report.GiftMix = make([]GiftMix, 0, len(mix))
		for _, row := range mix {
			report.GiftMix = append(report.GiftMix, GiftMix{Frequency: row.Frequency, Gifts: row.Gifts, CollectedMinor: row.CollectedMinor})
		}

		viral, err := q.ReportViralCoefficient(ctx, queries.ReportViralCoefficientParams{CreatedAt: startTS, CreatedAt_2: endTS})
		if err != nil {
			return Report{}, err
		}
		report.ViralCoefficient = ViralCoefficient{
			Cohort:               viral.Cohort,
			GrossConversions:     viral.GrossConversions,
			QualifiedConversions: viral.QualifiedConversions,
		}

		dollars, err := q.ReportReferralDollars(ctx, queries.ReportReferralDollarsParams{CreatedAt: startTS, CreatedAt_2: endTS})
		if err != nil {
			return Report{}, err
		}
		report.ReferralLinkMinor = dollars.LinkMinor
		report.ReferralIRLMinor = dollars.IrlMinor

		churn, err := q.ReportInvoluntaryChurn(ctx, queries.ReportInvoluntaryChurnParams{DueAt: startTS, DueAt_2: endTS, DueAt_3: endTS})
		if err != nil {
			return Report{}, err
		}
		report.InvoluntaryChurn = InvoluntaryChurn{
			FailedPeriods:    churn.FailedPeriods,
			LostMinor:        churn.LostMinor,
			RecoveredPeriods: churn.RecoveredPeriods,
			RecoveredMinor:   churn.RecoveredMinor,
		}

		ttfg, err := q.ReportTimeToFirstGift(ctx, queries.ReportTimeToFirstGiftParams{PaidAt: startTS, PaidAt_2: endTS})
		if err != nil {
			return Report{}, err
		}
		report.TimeToFirstGift = TimeToFirstGift{Sample: ttfg.Sample, MedianSeconds: ttfg.MedianSeconds}

		retention, err := q.ReportRetention(ctx, queries.ReportRetentionParams{StreakSince: startTS, StreakSince_2: endTS})
		if err != nil {
			return Report{}, err
		}
		report.Retention = Retention{
			ReachedM3:  retention.ReachedM3,
			RetainedM3: retention.RetainedM3,
			ReachedM6:  retention.ReachedM6,
			RetainedM6: retention.RetainedM6,
		}

		return report, nil
	})
}
