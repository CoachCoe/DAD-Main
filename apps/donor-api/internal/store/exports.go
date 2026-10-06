package store

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/store/queries"
)

type exportDataset struct{ table, fields, permission string }

var exportDatasets = map[string]exportDataset{
	"checkouts":        {"donor_checkouts", "id donor_id amount_minor currency frequency referrer_id referral_kind referral_expires_at created_at", "data.export"},
	"referral-touches": {"donor_referral_touches", "id referrer_id kind touched_at expires_at", "data.export"},
	"donors":           {"donors", "id created_at email claimed_at disabled public_name", "data.export"},
	"email-changes":    {"donor_email_changes", "id donor_id old_email new_email stage expires_at created_at completed_at", "data.export"},
	"identity-events":  {"donor_identity_events", "id donor_id email_change_id action created_at", "data.export"},
	"gifts":            {"donor_gifts", "id donor_id checkout_id amount_minor currency frequency paid_at period_id referrer_id referral_kind", "data.export"},
	"refunds":          {"donor_refunds", "id gift_id amount_minor currency status observed_at stripe_refund_id", "data.export"},
	"refund-history":   {"donor_refund_observations", "id refund_id source_event_id source_action_id status observed_at", "data.export"},
	"billing-periods":  {"donor_billing_periods", "id donor_id checkout_id stripe_invoice_id due_at ends_at status", "data.export"},
	"billing-history":  {"donor_billing_observations", "id period_id source_event_id status amount_minor currency billing_reason occurred_at event_created_at recorded_at", "data.export"},
	"referrals":        {"donor_metric_projections", "donor_id referrer_id referral_kind first_gift_id net_minor computed_at", "data.export"},
	"streaks":          {"donor_metric_projections", "donor_id current_streak best_streak at_risk grace_ends_at founding_awarded_at computed_at", "data.export"},
	"subscriptions":    {"donor_subscriptions", "id donor_id attempt_id status period_end stops_at observed_at", "data.export"},
	"pauses":           {"donor_pauses", "id donor_id attempt_id months starts_at resumes_at confirmed_at created_at", "data.export"},
	"resumptions":      {"donor_resumptions", "id donor_id pause_id next_renewal confirmed_at created_at", "data.export"},
	"amount-changes":   {"donor_amount_changes", "id donor_id checkout_id previous_minor amount_minor effective_at confirmed_at created_at", "data.export"},
	"cancellations":    {"donor_cancellations", "id donor_id attempt_id confirmed_period_end completed_at created_at", "data.export"},
	"preferences":      {"donor_preferences", "donor_id version referral_emails marketing_emails", "data.export"},
	"consent-history":  {"donor_preference_events", "id donor_id version referral_emails marketing_emails consent_version source created_at", "data.export"},
	"messages":         {"donor_messages", "id donor_id kind available_at completed_at attempts last_error suppressed_reason created_at", "data.export"},
	"staff-actions":    {"donor_staff_actions", "id actor_id donor_id kind target_id amount_minor period_end reason status stripe_refund_id completed_at created_at", "data.export"},
	"staff":            {"donor_staff", "id name email role disabled created_at", "staff.manage"},
	"audit":            {"donor_staff_audit", "id actor_id action target created_at details", "audit.read"},
	"partner-signups":  {"donor_partner_signups", "id organization name email website message consent_version created_at", "content.publish"},
	"content-sources":  {"donor_content_sources", "id actor_id name sha256 created_at", "content.publish"},
	"publications":     {"donor_content_publications", "id actor_id payload created_at", "content.publish"},
	"enrolments":       {"", "enrolment_id program_id submitted_at status", "data.export"},
}

type ExportDatasets struct {
	Datasets []string `json:"datasets"`
}

func (s *StaffService) ExportDatasets(ctx context.Context, token string) (ExportDatasets, error) {
	actor, err := requireStaff(ctx, queries.New(s.DB), token, "data.export", time.Now().UTC())
	if err != nil {
		return ExportDatasets{}, err
	}
	names := []string{}
	permissions := staffPermissions(actor.Role)
	for name, dataset := range exportDatasets {
		if slices.Contains(permissions, dataset.permission) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return ExportDatasets{Datasets: names}, nil
}

// safeCSVCell formats a column value, quoting text a spreadsheet would
// otherwise evaluate as a formula.
func safeCSVCell(value any) (string, error) {
	var text string
	switch v := value.(type) {
	case nil:
		return "", nil
	case time.Time:
		return v.UTC().Format(time.RFC3339Nano), nil
	case [16]byte:
		return (pgtype.UUID{Bytes: v, Valid: true}).String(), nil
	case []byte:
		text = string(v)
	case map[string]any, []any:
		encoded, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		text = string(encoded)
	case string:
		text = v
	default:
		return fmt.Sprint(value), nil
	}
	trimmed := strings.TrimLeft(text, "\ufeff \t\r\n")
	if strings.HasPrefix(text, "\t") || strings.HasPrefix(text, "\r") || strings.HasPrefix(text, "\n") || (len(trimmed) > 0 && strings.ContainsRune("=+-@", rune(trimmed[0]))) {
		return "'" + text, nil
	}
	return text, nil
}

// Export streams dataset name as CSV to destination. Its start and its end,
// complete or interrupted, are both audited.
func (s *StaffService) Export(ctx context.Context, token, name string, destination io.Writer) (resultErr error) {
	dataset, ok := exportDatasets[name]
	if !ok {
		return pgx.ErrNoRows
	}
	var actorID, exportID pgtype.UUID
	_, err := staffRead(ctx, s, token, dataset.permission, func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (struct{}, error) {
		actorID = actor.ID
		var err error
		if exportID, err = NewID(); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, queries.New(tx).InsertStaffAudit(ctx, queries.InsertStaffAuditParams{ID: exportID, ActorID: actor.ID, Action: "data.export_started", Target: name, Details: []byte("{}"), CreatedAt: timestamp(now)})
	})
	if err != nil {
		return err
	}
	var count int64
	completed := false
	defer func() {
		// Disconnection must still leave an incomplete-export audit record.
		auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		now := time.Now().UTC()
		err := auditStaff(auditCtx, queries.New(s.DB), actorID, "data.export_finished", name, map[string]any{"export_id": exportID.String(), "completed": completed, "row_count": count, "finished_at": now}, now)
		if resultErr == nil && err != nil {
			resultErr = err
		}
	}()
	if _, err = requireStaff(ctx, queries.New(s.DB), token, dataset.permission, time.Now().UTC()); err != nil {
		return err
	}
	fields := strings.Fields(dataset.fields)
	writer := csv.NewWriter(destination)
	writer.UseCRLF = true
	if err = writer.Write(fields); err != nil {
		return err
	}
	if dataset.table != "" {
		if count, err = s.writeRows(ctx, writer, name, dataset.table, fields); err != nil {
			return err
		}
	}
	writer.Flush()
	if err = writer.Error(); err != nil {
		return err
	}
	completed = true
	return nil
}

// writeRows writes every row of table from one snapshot, flushing as it goes
// so a large export streams rather than buffers.
func (s *StaffService) writeRows(ctx context.Context, writer *csv.Writer, name, table string, fields []string) (int64, error) {
	// SQL identifiers come only from the fixed business-data allowlist above.
	columns := make([]string, len(fields))
	for i, field := range fields {
		columns[i] = pgx.Identifier{field}.Sanitize()
	}
	query := "SELECT " + strings.Join(columns, ",") + " FROM " + pgx.Identifier{table}.Sanitize()
	if name == "referrals" {
		query += " WHERE referrer_id IS NOT NULL"
	}
	query += " ORDER BY id"
	var count int64
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, query)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			values, err := rows.Values()
			if err != nil {
				return err
			}
			cells := make([]string, len(values))
			for i, value := range values {
				if cells[i], err = safeCSVCell(value); err != nil {
					return err
				}
			}
			if err = writer.Write(cells); err != nil {
				return err
			}
			count++
			if count%500 == 0 {
				writer.Flush()
				if err = writer.Error(); err != nil {
					return err
				}
			}
		}
		return rows.Err()
	})
	return count, err
}
