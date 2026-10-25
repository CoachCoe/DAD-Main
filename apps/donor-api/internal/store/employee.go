package store

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"dad/donor-api/internal/identity"
	"dad/donor-api/internal/store/queries"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrEmployeeLink means the company code matches no approved partner.
	ErrEmployeeLink = errors.New("unknown company code")
	// ErrTeam is an invalid team (bad name, or a team in another partner).
	ErrTeam = errors.New("invalid team")
)

// EmployeeService resolves a donor's company code to a partner and reads the
// employee-giving aggregates. Reuses the donor and partner session helpers.
type EmployeeService struct{ DB *pgxpool.Pool }

// PartnerOurPeople is the §7.3 Our People tile (EM-3), k-anonymised.
type PartnerOurPeople struct {
	Enrolled   int64 `json:"enrolled"`
	GivenMinor int64 `json:"given_minor"`
	AvgStreak  int64 `json:"avg_streak"`
	Suppressed bool  `json:"suppressed"`
}

// EmployeeTeamRow is one row of the team leaderboard (EM-5).
type EmployeeTeamRow struct {
	TeamID           string `json:"team_id"`
	Name             string `json:"name"`
	ActiveGivers     int64  `json:"active_givers"`
	AvgStreak        int64  `json:"avg_streak"`
	VolunteerMinutes int64  `json:"volunteer_minutes"`
}

// DonorTeam is the donor's own employer + team (EM-2/EM-4), for /donor/me/team.
type DonorTeam struct {
	PartnerID    string `json:"partner_id"`
	Organization string `json:"organization"`
	TeamID       string `json:"team_id,omitempty"`
	TeamName     string `json:"team_name,omitempty"`
}

// MatchStatus is the §8 matching view (EM-6/EM-7): owed this calendar year and
// received offline. No money moves.
type MatchStatus struct {
	Year             int32 `json:"year"`
	RateBP           int32 `json:"match_rate_bp"`
	CapMinor         int64 `json:"match_cap_minor"`
	GiftsMinor       int64 `json:"gifts_minor"`
	OwedMinor        int64 `json:"owed_minor"`
	PaidMinor        int64 `json:"paid_minor"`
	OutstandingMinor int64 `json:"outstanding_minor"`
}

// Enroll links the authenticated donor to the partner whose company code they
// entered (EM-2). The code is normalised to upper case. Idempotent: a donor
// already linked keeps their employer. Returns the partner organisation name.
func (s EmployeeService) Enroll(ctx context.Context, token, code string) (string, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if !companyCode.MatchString(code) {
		return "", ErrEmployeeLink
	}
	return donorWrite(ctx, s.DB, donorOwner(token), func(tx pgx.Tx, d queries.Donor, now time.Time) (string, error) {
		q := queries.New(tx)
		partnerID, err := q.PartnerByCompanyCode(ctx, text(code))
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrEmployeeLink
		}
		if err != nil {
			return "", err
		}
		if err = q.UpsertEmployeeLink(ctx, queries.UpsertEmployeeLinkParams{
			DonorID: d.ID, PartnerID: partnerID, EnrolledAt: timestamp(now),
		}); err != nil {
			return "", err
		}
		org, err := q.PartnerOrganizationByID(ctx, partnerID)
		if err != nil {
			return "", err
		}
		return org, nil
	})
}

// Team returns the donor's employer + team, or a zero value when unlinked.
func (s EmployeeService) Team(ctx context.Context, token string) (DonorTeam, error) {
	return donorRead(ctx, s.DB, token, func(q *queries.Queries, d queries.Donor, _ time.Time) (DonorTeam, error) {
		link, err := q.EmployeeLink(ctx, d.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return DonorTeam{}, nil
		}
		if err != nil {
			return DonorTeam{}, err
		}
		org, err := q.PartnerOrganizationByID(ctx, link.PartnerID)
		if err != nil {
			return DonorTeam{}, err
		}
		out := DonorTeam{PartnerID: link.PartnerID.String(), Organization: org}
		if link.TeamID.Valid {
			team, err := q.TeamByID(ctx, link.TeamID)
			if err != nil {
				return DonorTeam{}, err
			}
			out.TeamID, out.TeamName = team.ID.String(), team.Name
		}
		return out, nil
	})
}

// JoinTeam places the donor on a named team of their own employer, creating the
// team on first use (EM-4). One team per employee.
func (s EmployeeService) JoinTeam(ctx context.Context, token, name string) (string, error) {
	name = identity.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n == 0 || n > 80 || !plainText(name, false) {
		return "", ErrTeam
	}
	return donorWrite(ctx, s.DB, donorOwner(token), func(tx pgx.Tx, d queries.Donor, _ time.Time) (string, error) {
		q := queries.New(tx)
		link, err := q.EmployeeLink(ctx, d.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrEmployeeLink
		}
		if err != nil {
			return "", err
		}
		teamID, err := teamByName(ctx, q, link.PartnerID, name)
		if err != nil {
			return "", err
		}
		if err = q.SetEmployeeTeam(ctx, queries.SetEmployeeTeamParams{DonorID: d.ID, TeamID: teamID}); err != nil {
			return "", err
		}
		return teamID.String(), nil
	})
}

// teamByName finds a partner's team by name or creates it, returning its id.
func teamByName(ctx context.Context, q *queries.Queries, partnerID pgtype.UUID, name string) (pgtype.UUID, error) {
	id, err := q.TeamIDByName(ctx, queries.TeamIDByNameParams{PartnerID: partnerID, Name: name})
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, err
	}
	newID, err := NewID()
	if err != nil {
		return pgtype.UUID{}, err
	}
	if err = q.InsertTeam(ctx, queries.InsertTeamParams{
		ID: newID, PartnerID: partnerID, Name: name, CreatedAt: timestamp(time.Now().UTC()),
	}); err != nil {
		return pgtype.UUID{}, err
	}
	return newID, nil
}

// OurPeople returns the k-anonymised Our People aggregate for the calling
// partner (EM-3). Any partner role may view; individuals are never returned.
func (s EmployeeService) OurPeople(ctx context.Context, token string, portal PartnerPortalService) (PartnerOurPeople, error) {
	return partnerRead(ctx, portal.DB, token, func(q *queries.Queries, actor PartnerActor) (PartnerOurPeople, error) {
		row, err := q.PartnerOurPeople(ctx, actor.PartnerID)
		if err != nil {
			return PartnerOurPeople{}, err
		}
		return PartnerOurPeople{
			Enrolled: row.Enrolled, GivenMinor: row.GivenMinor,
			AvgStreak: row.AvgStreak, Suppressed: row.Suppressed,
		}, nil
	})
}

// Teams returns the partner's team leaderboard (EM-5), teams under 5 suppressed.
func (s EmployeeService) Teams(ctx context.Context, token string, portal PartnerPortalService) ([]EmployeeTeamRow, error) {
	return partnerRead(ctx, portal.DB, token, func(q *queries.Queries, actor PartnerActor) ([]EmployeeTeamRow, error) {
		rows, err := q.TeamLeaderboard(ctx, actor.PartnerID)
		if err != nil {
			return nil, err
		}
		out := make([]EmployeeTeamRow, 0, len(rows))
		for _, r := range rows {
			out = append(out, EmployeeTeamRow{
				TeamID: r.TeamID.String(), Name: r.Name, ActiveGivers: r.ActiveGivers,
				AvgStreak: r.AvgStreak, VolunteerMinutes: r.VolunteerMinutes,
			})
		}
		return out, nil
	})
}

// Match computes the matching status for a partner in a calendar year (EM-6):
// owed = linked-employee gifts (including pre-link) × rate, capped at the annual
// cap; outstanding = owed − received. Staff-only (partner.manage).
func (s EmployeeService) Match(
	ctx context.Context,
	staff *StaffService,
	token string,
	partnerID pgtype.UUID,
	year int32,
) (MatchStatus, error) {
	return staffWrite(ctx, staff, token, partnerPermission, func(tx pgx.Tx, actor queries.DonorStaff, _ time.Time) (MatchStatus, error) {
		q := queries.New(tx)
		terms, err := q.PartnerMatchTerms(ctx, partnerID)
		if err != nil {
			return MatchStatus{}, err
		}
		start := time.Date(int(year), 1, 1, 0, 0, 0, 0, time.UTC)
		end := start.AddDate(1, 0, 0)
		gifts, err := q.LinkedEmployeeGiftsInYear(ctx, queries.LinkedEmployeeGiftsInYearParams{
			PartnerID: partnerID, YearStart: timestamp(start), YearEnd: timestamp(end),
		})
		if err != nil {
			return MatchStatus{}, err
		}
		paid, err := q.MatchPaymentsInYear(ctx, queries.MatchPaymentsInYearParams{PartnerID: partnerID, Year: year})
		if err != nil {
			return MatchStatus{}, err
		}
		owed := gifts * int64(terms.MatchRateBp) / 10000
		if terms.MatchCapMinor > 0 && owed > terms.MatchCapMinor {
			owed = terms.MatchCapMinor
		}
		outstanding := owed - paid
		if outstanding < 0 {
			outstanding = 0
		}
		return MatchStatus{
			Year: year, RateBP: terms.MatchRateBp, CapMinor: terms.MatchCapMinor,
			GiftsMinor: gifts, OwedMinor: owed, PaidMinor: paid, OutstandingMinor: outstanding,
		}, nil
	})
}

// RecordMatchPayment records an offline match payment received (EM-7). No money
// moves. Staff-only.
func (s EmployeeService) RecordMatchPayment(
	ctx context.Context,
	staff *StaffService,
	token string,
	partnerID pgtype.UUID,
	year int32,
	amountMinor int64,
) error {
	if amountMinor <= 0 {
		return ErrPartnerApplication
	}
	_, err := staffWrite(ctx, staff, token, partnerPermission, func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (struct{}, error) {
		q := queries.New(tx)
		id, err := NewID()
		if err != nil {
			return struct{}{}, err
		}
		if err = q.InsertMatchPayment(ctx, queries.InsertMatchPaymentParams{
			ID: id, PartnerID: partnerID, Year: year, AmountMinor: amountMinor,
			ReceivedAt: timestamp(now), RecordedBy: actor.ID,
		}); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, auditStaff(ctx, q, actor.ID, "partner.match_payment", partnerID.String(), nil, now)
	})
	return err
}

// SetWorkforce updates a partner's workforce size (EM-9). Partner admin only.
func (s EmployeeService) SetWorkforce(ctx context.Context, token string, portal PartnerPortalService, size int32) error {
	if size < 0 {
		return ErrPartnerApplication
	}
	_, err := partnerAdminWrite(ctx, portal.DB, token, func(tx pgx.Tx, actor PartnerActor, _ time.Time) (struct{}, error) {
		return struct{}{}, queries.New(tx).
			SetPartnerWorkforce(ctx, queries.SetPartnerWorkforceParams{ID: actor.PartnerID, WorkforceSize: size})
	})
	return err
}

// Adapters bind the extra dependencies (partner portal pool, staff service) so
// the HTTP layer can take narrow, dependency-free source interfaces.

// EmployeeDonorAdapter is the donor-facing surface.
type EmployeeDonorAdapter struct{ Emp EmployeeService }

func (a EmployeeDonorAdapter) Enroll(ctx context.Context, token, code string) (string, error) {
	return a.Emp.Enroll(ctx, token, code)
}
func (a EmployeeDonorAdapter) Team(ctx context.Context, token string) (DonorTeam, error) {
	return a.Emp.Team(ctx, token)
}
func (a EmployeeDonorAdapter) JoinTeam(ctx context.Context, token, name string) (string, error) {
	return a.Emp.JoinTeam(ctx, token, name)
}

// EmployeePartnerAdapter binds the partner portal (for session auth).
type EmployeePartnerAdapter struct {
	Emp    EmployeeService
	Portal PartnerPortalService
}

func (a EmployeePartnerAdapter) OurPeople(ctx context.Context, token string) (PartnerOurPeople, error) {
	return a.Emp.OurPeople(ctx, token, a.Portal)
}
func (a EmployeePartnerAdapter) Teams(ctx context.Context, token string) ([]EmployeeTeamRow, error) {
	return a.Emp.Teams(ctx, token, a.Portal)
}
func (a EmployeePartnerAdapter) SetWorkforce(ctx context.Context, token string, size int32) error {
	return a.Emp.SetWorkforce(ctx, token, a.Portal, size)
}

// EmployeeStaffAdapter binds the staff service (for partner.manage auth).
type EmployeeStaffAdapter struct {
	Emp   EmployeeService
	Staff *StaffService
}

func (a EmployeeStaffAdapter) Match(ctx context.Context, token string, partnerID pgtype.UUID, year int32) (MatchStatus, error) {
	return a.Emp.Match(ctx, a.Staff, token, partnerID, year)
}

func (a EmployeeStaffAdapter) RecordMatchPayment(
	ctx context.Context,
	token string,
	partnerID pgtype.UUID,
	year int32,
	amountMinor int64,
) error {
	return a.Emp.RecordMatchPayment(ctx, a.Staff, token, partnerID, year, amountMinor)
}
