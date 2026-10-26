package store

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"time"

	"dad/donor-api/internal/store/queries"
	"github.com/go-pdf/fpdf"
	"github.com/jackc/pgx/v5"
)

// minimumGroup is the smallest number of employees any partner figure may
// describe (EM-3); a smaller group is suppressed.
const minimumGroup = 5

// PartnerOverview is the §7.3 dashboard header (P-10). Figures are published
// and verified only, never estimated. EmployeesEnrolled and EnrolledPercent
// are nil while fewer than minimumGroup employees are linked, and
// EnrolledPercent also without a workforce size. UpdatedAt is when any figure
// last changed, nil before the partner has a published initiative. Role is the
// caller's own, so a client can offer only the actions it may take (§7.2).
type PartnerOverview struct {
	Organization      string     `json:"organization"`
	Description       string     `json:"description"`
	Role              string     `json:"role"`
	CompanyCode       string     `json:"company_code"`
	CommittedMinor    int64      `json:"committed_minor"`
	DeployedMinor     int64      `json:"deployed_minor"`
	DeployedPercent   int        `json:"deployed_percent"`
	PeopleReached     int64      `json:"people_reached"`
	EmployeesEnrolled *int64     `json:"employees_enrolled"`
	WorkforceSize     int32      `json:"workforce_size"`
	EnrolledPercent   *int       `json:"enrolled_percent"`
	UpdatedAt         *time.Time `json:"updated_at"`
}

// PartnerMetric is one published, verified headline metric of an initiative.
type PartnerMetric struct {
	Label      string `json:"label"`
	Value      int64  `json:"value"`
	Unit       string `json:"unit"`
	VerifiedOn string `json:"verified_on"`
}

// PartnerInitiative is one co-initiative with its detail (P-11): every
// published metric, in display order, and its spend by category.
type PartnerInitiative struct {
	ID             string            `json:"id"`
	Title          string            `json:"title"`
	Summary        string            `json:"summary"`
	Status         string            `json:"status"`
	Region         string            `json:"region"`
	CountyCount    int32             `json:"county_count"`
	CommittedMinor int64             `json:"committed_minor"`
	DeployedMinor  int64             `json:"deployed_minor"`
	Metrics        []PartnerMetric   `json:"metrics"`
	Spend          []PartnerSpendRow `json:"spend"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

// PartnerSpendRow is one category of spend, as a total (P-12).
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

// PartnerDashboardService is the partner portal: the session and user
// management of PartnerPortalService plus the dashboard reads (§7.3).
type PartnerDashboardService struct{ PartnerPortalService }

func partnerOverview(actor PartnerActor, t queries.PartnerOverviewTotalsRow) PartnerOverview {
	o := PartnerOverview{
		Organization:   actor.Organization,
		Description:    t.Description,
		Role:           actor.Role,
		CompanyCode:    t.CompanyCode.String,
		CommittedMinor: t.CommittedMinor, DeployedMinor: t.DeployedMinor,
		DeployedPercent: percent(t.DeployedMinor, t.CommittedMinor),
		PeopleReached:   t.PeopleReached,
		WorkforceSize:   t.WorkforceSize,
	}
	if t.EmployeesEnrolled >= minimumGroup {
		o.EmployeesEnrolled = &t.EmployeesEnrolled
		if t.WorkforceSize > 0 {
			p := percent(t.EmployeesEnrolled, int64(t.WorkforceSize))
			o.EnrolledPercent = &p
		}
	}
	if t.UpdatedAt.Valid {
		o.UpdatedAt = &t.UpdatedAt.Time
	}
	return o
}

// percent is part as a whole percentage of whole, at most 100.
func percent(part, whole int64) int {
	if whole <= 0 {
		return 0
	}
	return int(min(part*100/whole, 100))
}

// Overview computes the header tiles for the calling partner.
func (s PartnerDashboardService) Overview(ctx context.Context, token string) (PartnerOverview, error) {
	return partnerRead(ctx, s.DB, token, func(q *queries.Queries, actor PartnerActor) (PartnerOverview, error) {
		t, err := q.PartnerOverviewTotals(ctx, actor.PartnerID)
		if err != nil {
			return PartnerOverview{}, err
		}
		return partnerOverview(actor, t), nil
	})
}

// Initiatives lists the partner's published co-initiatives with their detail.
func (s PartnerDashboardService) Initiatives(ctx context.Context, token string) ([]PartnerInitiative, error) {
	return partnerRead(ctx, s.DB, token, func(q *queries.Queries, actor PartnerActor) ([]PartnerInitiative, error) {
		rows, err := q.PartnerInitiativesList(ctx, actor.PartnerID)
		if err != nil {
			return nil, err
		}
		metrics, err := q.PartnerInitiativeMetrics(ctx, actor.PartnerID)
		if err != nil {
			return nil, err
		}
		spend, err := q.PartnerInitiativeSpend(ctx, actor.PartnerID)
		if err != nil {
			return nil, err
		}
		out := make([]PartnerInitiative, 0, len(rows))
		index := make(map[[16]byte]int, len(rows))
		for i, r := range rows {
			index[r.ID.Bytes] = i
			out = append(out, PartnerInitiative{
				ID: r.ID.String(), Title: r.Title, Summary: r.Summary, Status: r.Status, Region: r.Region,
				CountyCount: r.CountyCount, CommittedMinor: r.CommittedMinor, DeployedMinor: r.DeployedMinor,
				Metrics: []PartnerMetric{}, Spend: []PartnerSpendRow{}, UpdatedAt: r.UpdatedAt.Time,
			})
		}
		for _, m := range metrics {
			if i, ok := index[m.InitiativeID.Bytes]; ok {
				out[i].Metrics = append(out[i].Metrics, PartnerMetric{
					Label: m.Label, Value: m.Value, Unit: m.Unit, VerifiedOn: m.VerifiedOn.Time.Format(time.DateOnly),
				})
			}
		}
		for _, c := range spend {
			if i, ok := index[c.InitiativeID.Bytes]; ok {
				out[i].Spend = append(out[i].Spend, PartnerSpendRow{Category: c.Category, AmountMinor: c.AmountMinor})
			}
		}
		return out, nil
	})
}

// Spend lists spend by category across the partner's initiatives (P-12).
func (s PartnerDashboardService) Spend(ctx context.Context, token string) ([]PartnerSpendRow, error) {
	return partnerRead(ctx, s.DB, token, func(q *queries.Queries, actor PartnerActor) ([]PartnerSpendRow, error) {
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
	return partnerRead(ctx, s.DB, token, func(q *queries.Queries, actor PartnerActor) ([]PartnerReportRow, error) {
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

// ExportReport renders the branded PDF of the dashboard at this moment and
// records the export in the history (P-14). Exporting is admin-only (§7.2).
func (s PartnerDashboardService) ExportReport(ctx context.Context, token string) ([]byte, error) {
	return partnerAdminWrite(ctx, s.DB, token, func(tx pgx.Tx, actor PartnerActor, now time.Time) ([]byte, error) {
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
		return renderPartnerReport(partnerOverview(actor, t), spend, now)
	})
}

// renderPartnerReport builds the branded single-page PDF: the DAD header band,
// the partner organization, the dashboard figures, spend by category, the
// export time, and the "Figures verified by DAD" statement (P-14). The core
// fonts are cp1252, so every string is translated rather than written as UTF-8.
func renderPartnerReport(o PartnerOverview, spend []queries.PartnerSpendByCategoryRow, now time.Time) ([]byte, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	tr := pdf.UnicodeTranslatorFromDescriptor("")
	pdf.SetMargins(18, 18, 18)
	pdf.AddPage()

	pdf.SetFillColor(13, 59, 102)
	pdf.Rect(0, 0, 210, 26, "F")
	pdf.SetY(8)
	pdf.SetFont("Helvetica", "B", 20)
	pdf.SetTextColor(255, 255, 255)
	pdf.CellFormat(0, 10, tr("DAD — Impact Report"), "", 1, "L", false, 0, "")

	pdf.SetTextColor(20, 20, 20)
	pdf.Ln(10)
	pdf.SetFont("Helvetica", "B", 15)
	pdf.CellFormat(0, 9, tr(o.Organization), "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 10)
	pdf.SetTextColor(90, 90, 90)
	pdf.CellFormat(0, 6, "Exported "+now.UTC().Format("2 January 2006, 15:04 UTC"), "", 1, "L", false, 0, "")
	if o.UpdatedAt != nil {
		pdf.CellFormat(0, 6, "Figures last updated "+o.UpdatedAt.UTC().Format("2 January 2006, 15:04 UTC"), "", 1, "L", false, 0, "")
	}
	pdf.Ln(6)

	pdf.SetTextColor(20, 20, 20)
	tile := func(value, label string) {
		pdf.SetFont("Helvetica", "B", 16)
		pdf.CellFormat(0, 8, tr(value), "", 1, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 10)
		pdf.SetTextColor(90, 90, 90)
		pdf.CellFormat(0, 6, tr(label), "", 1, "L", false, 0, "")
		pdf.SetTextColor(20, 20, 20)
		pdf.Ln(3)
	}
	tile(money(o.CommittedMinor), "Committed")
	tile(fmt.Sprintf("%s  (%d%% of committed)", money(o.DeployedMinor), o.DeployedPercent), "Deployed")
	tile(strconv.FormatInt(o.PeopleReached, 10), "People reached (verified)")
	if o.EmployeesEnrolled != nil {
		enrolled := strconv.FormatInt(*o.EmployeesEnrolled, 10)
		if o.EnrolledPercent != nil {
			enrolled += fmt.Sprintf("  (%d%% of workforce)", *o.EnrolledPercent)
		}
		tile(enrolled, "Employees enrolled")
	}

	if len(spend) > 0 {
		pdf.Ln(4)
		pdf.SetFont("Helvetica", "B", 12)
		pdf.CellFormat(0, 8, "Spend by category", "", 1, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 10)
		for _, row := range spend {
			pdf.CellFormat(120, 6, tr(row.Category), "", 0, "L", false, 0, "")
			pdf.CellFormat(0, 6, money(row.AmountMinor), "", 1, "R", false, 0, "")
		}
	}

	pdf.SetY(-24)
	pdf.SetDrawColor(200, 200, 200)
	pdf.Line(18, pdf.GetY(), 192, pdf.GetY())
	pdf.Ln(3)
	pdf.SetFont("Helvetica", "I", 9)
	pdf.SetTextColor(90, 90, 90)
	pdf.MultiCell(0, 5,
		"Figures verified by DAD. Only published, verified figures are shown; unverified figures are never estimated.",
		"", "L", false)

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
