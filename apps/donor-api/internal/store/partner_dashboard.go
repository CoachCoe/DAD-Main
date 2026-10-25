package store

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"dad/donor-api/internal/store/queries"
	"github.com/go-pdf/fpdf"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// PartnerOverview is the §7.3 dashboard header: committed, deployed (with the
// share of committed actually spent), and verified people reached. Figures are
// published + verified only — never estimated (P-10).
type PartnerOverview struct {
	Organization    string `json:"organization"`
	CommittedMinor  int64  `json:"committed_minor"`
	DeployedMinor   int64  `json:"deployed_minor"`
	DeployedPercent int    `json:"deployed_percent"`
	PeopleReached   int64  `json:"people_reached"`
	// OurPeopleAvailable is false in Phase 5 — the "Our People" tile (P-13)
	// and employees-enrolled land in Phase 6. The field keeps the contract
	// stable so the web tile can render a "coming soon" state.
	OurPeopleAvailable bool `json:"our_people_available"`
}

// PartnerInitiative is one row of the §7.3 initiatives list (P-11).
type PartnerInitiative struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Summary        string `json:"summary"`
	CommittedMinor int64  `json:"committed_minor"`
	DeployedMinor  int64  `json:"deployed_minor"`
}

// PartnerSpendRow is one category of §7.3 spend-by-category (P-12).
type PartnerSpendRow struct {
	Category    string `json:"category"`
	AmountMinor int64  `json:"amount_minor"`
}

// PartnerReportRow is one past export in the §7.3 history (P-14).
type PartnerReportRow struct {
	ID             string    `json:"id"`
	CommittedMinor int64     `json:"committed_minor"`
	DeployedMinor  int64     `json:"deployed_minor"`
	PeopleReached  int64     `json:"people_reached"`
	ExportedBy     string    `json:"exported_by"`
	CreatedAt      time.Time `json:"created_at"`
}

// PartnerDashboardService reads the authenticated partner's dashboard.
type PartnerDashboardService struct{ Portal PartnerPortalService }

// The portal-user + auth operations delegate to the embedded PartnerPortalService
// so the HTTP layer can take one partner-portal dependency covering both the
// dashboard (§7.3) and user management (§7.2).

func (s PartnerDashboardService) AcceptInvite(ctx context.Context, invite string) (string, error) {
	return s.Portal.AcceptInvite(ctx, invite)
}

func (s PartnerDashboardService) SignOut(ctx context.Context, token string) error {
	return s.Portal.SignOut(ctx, token)
}

func (s PartnerDashboardService) Users(ctx context.Context, token string) ([]PartnerUser, error) {
	return s.Portal.Users(ctx, token)
}

func (s PartnerDashboardService) Invite(ctx context.Context, token, email, name, role string) (string, error) {
	return s.Portal.Invite(ctx, token, email, name, role)
}

func (s PartnerDashboardService) RemoveUser(ctx context.Context, token string, userID pgtype.UUID) error {
	return s.Portal.RemoveUser(ctx, token, userID)
}

// Overview computes the header tiles for the calling partner.
func (s PartnerDashboardService) Overview(ctx context.Context, token string) (PartnerOverview, error) {
	return partnerRead(ctx, s.Portal.DB, token, func(q *queries.Queries, actor PartnerActor) (PartnerOverview, error) {
		t, err := q.PartnerOverviewTotals(ctx, actor.PartnerID)
		if err != nil {
			return PartnerOverview{}, err
		}
		return PartnerOverview{
			Organization:   actor.Organization,
			CommittedMinor: t.CommittedMinor, DeployedMinor: t.DeployedMinor,
			DeployedPercent: deployedPercent(t.CommittedMinor, t.DeployedMinor),
			PeopleReached:   t.PeopleReached, OurPeopleAvailable: false,
		}, nil
	})
}

func deployedPercent(committed, deployed int64) int {
	if committed <= 0 {
		return 0
	}
	p := int(deployed * 100 / committed)
	if p > 100 {
		p = 100
	}
	return p
}

// Initiatives lists the partner's published initiatives (P-11).
func (s PartnerDashboardService) Initiatives(ctx context.Context, token string) ([]PartnerInitiative, error) {
	return partnerRead(ctx, s.Portal.DB, token, func(q *queries.Queries, actor PartnerActor) ([]PartnerInitiative, error) {
		rows, err := q.PartnerInitiativesList(ctx, actor.PartnerID)
		if err != nil {
			return nil, err
		}
		out := make([]PartnerInitiative, 0, len(rows))
		for _, r := range rows {
			out = append(out, PartnerInitiative{
				ID: r.ID.String(), Title: r.Title, Summary: r.Summary,
				CommittedMinor: r.CommittedMinor, DeployedMinor: r.DeployedMinor,
			})
		}
		return out, nil
	})
}

// Spend lists spend by category across the partner's initiatives (P-12).
func (s PartnerDashboardService) Spend(ctx context.Context, token string) ([]PartnerSpendRow, error) {
	return partnerRead(ctx, s.Portal.DB, token, func(q *queries.Queries, actor PartnerActor) ([]PartnerSpendRow, error) {
		rows, err := q.PartnerSpendByCategory(ctx, actor.PartnerID)
		if err != nil {
			return nil, err
		}
		out := make([]PartnerSpendRow, 0, len(rows))
		for _, r := range rows {
			out = append(out, PartnerSpendRow{Category: r.Category, AmountMinor: r.AmountMinor})
		}
		return out, nil
	})
}

// Exports lists the partner's previous report exports, newest first (P-14).
func (s PartnerDashboardService) Exports(ctx context.Context, token string) ([]PartnerReportRow, error) {
	return partnerRead(ctx, s.Portal.DB, token, func(q *queries.Queries, actor PartnerActor) ([]PartnerReportRow, error) {
		rows, err := q.PartnerReports(ctx, actor.PartnerID)
		if err != nil {
			return nil, err
		}
		out := make([]PartnerReportRow, 0, len(rows))
		for _, r := range rows {
			out = append(out, PartnerReportRow{
				ID: r.ID.String(), CommittedMinor: r.CommittedMinor, DeployedMinor: r.DeployedMinor,
				PeopleReached: r.PeopleReached, ExportedBy: r.ExportedByName, CreatedAt: r.CreatedAt.Time,
			})
		}
		return out, nil
	})
}

// ExportReport generates a branded PDF of the current dashboard snapshot and
// records the export in the history (P-14). Admin-only (§7.2 export is an admin
// action). Returns the PDF bytes.
func (s PartnerDashboardService) ExportReport(ctx context.Context, token string) ([]byte, error) {
	return partnerAdminWrite(ctx, s.Portal.DB, token, func(tx pgx.Tx, actor PartnerActor, now time.Time) ([]byte, error) {
		q := queries.New(tx)
		t, err := q.PartnerOverviewTotals(ctx, actor.PartnerID)
		if err != nil {
			return nil, err
		}
		spend, err := q.PartnerSpendByCategory(ctx, actor.PartnerID)
		if err != nil {
			return nil, err
		}
		id, err := NewID()
		if err != nil {
			return nil, err
		}
		if err = q.InsertPartnerReport(ctx, queries.InsertPartnerReportParams{
			ID: id, PartnerID: actor.PartnerID, ExportedBy: actor.UserID,
			CommittedMinor: t.CommittedMinor, DeployedMinor: t.DeployedMinor,
			PeopleReached: t.PeopleReached, CreatedAt: timestamp(now),
		}); err != nil {
			return nil, err
		}
		return renderPartnerReport(actor.Organization, t, spend, now)
	})
}

// renderPartnerReport builds the branded single-page PDF: DAD wordmark header,
// the partner org, the dashboard figures, spend by category, a UTC timestamp,
// and the "Figures verified by DAD" statement (P-14).
func renderPartnerReport(
	org string,
	t queries.PartnerOverviewTotalsRow,
	spend []queries.PartnerSpendByCategoryRow,
	now time.Time,
) ([]byte, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(18, 18, 18)
	pdf.AddPage()

	// Branded header band.
	pdf.SetFillColor(13, 59, 102)
	pdf.Rect(0, 0, 210, 26, "F")
	pdf.SetY(8)
	pdf.SetFont("Helvetica", "B", 20)
	pdf.SetTextColor(255, 255, 255)
	pdf.CellFormat(0, 10, "DAD — Impact Report", "", 1, "L", false, 0, "")

	pdf.SetTextColor(20, 20, 20)
	pdf.Ln(10)
	pdf.SetFont("Helvetica", "B", 15)
	pdf.CellFormat(0, 9, org, "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 10)
	pdf.SetTextColor(90, 90, 90)
	pdf.CellFormat(0, 6, "Generated "+now.UTC().Format("2006-01-02 15:04 MST"), "", 1, "L", false, 0, "")
	pdf.Ln(6)

	// Figure tiles.
	pdf.SetTextColor(20, 20, 20)
	tile := func(label, value string) {
		pdf.SetFont("Helvetica", "B", 16)
		pdf.CellFormat(0, 8, value, "", 1, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 10)
		pdf.SetTextColor(90, 90, 90)
		pdf.CellFormat(0, 6, label, "", 1, "L", false, 0, "")
		pdf.SetTextColor(20, 20, 20)
		pdf.Ln(3)
	}
	tile(money(t.CommittedMinor), "Committed")
	tile(fmt.Sprintf("%s  (%d%% of committed)", money(t.DeployedMinor), deployedPercent(t.CommittedMinor, t.DeployedMinor)), "Deployed")
	tile(fmt.Sprintf("%d", t.PeopleReached), "People reached (verified)")

	// Spend by category.
	if len(spend) > 0 {
		pdf.Ln(4)
		pdf.SetFont("Helvetica", "B", 12)
		pdf.CellFormat(0, 8, "Spend by category", "", 1, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 10)
		for _, row := range spend {
			pdf.CellFormat(120, 6, row.Category, "", 0, "L", false, 0, "")
			pdf.CellFormat(0, 6, money(row.AmountMinor), "", 1, "R", false, 0, "")
		}
	}

	// Verification footer.
	pdf.SetY(-24)
	pdf.SetDrawColor(200, 200, 200)
	pdf.Line(18, pdf.GetY(), 192, pdf.GetY())
	pdf.Ln(3)
	pdf.SetFont("Helvetica", "I", 9)
	pdf.SetTextColor(90, 90, 90)
	pdf.CellFormat(
		0,
		6,
		"Figures verified by DAD. Only published, verified figures are shown; unverified figures are never estimated.",
		"",
		1,
		"L",
		false,
		0,
		"",
	)

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
