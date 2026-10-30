package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dad/donor-api/internal/store"
	"github.com/jackc/pgx/v5/pgtype"
)

type overallSpendFunc func(context.Context) (store.OverallSpend, error)

func (f overallSpendFunc) OverallSpend(ctx context.Context) (store.OverallSpend, error) {
	return f(ctx)
}
func (overallSpendFunc) Spend(context.Context, pgtype.UUID) (store.InitiativeSpend, error) {
	return store.InitiativeSpend{}, nil
}
func (overallSpendFunc) Published(context.Context) ([]store.PublicInitiative, error) { return nil, nil }
func (overallSpendFunc) PublishedBySlug(context.Context, string) (store.PublicInitiative, error) {
	return store.PublicInitiative{}, nil
}

func TestOverallSpendIsCacheableAndBoundedByADeadline(t *testing.T) {
	reader := overallSpendFunc(func(ctx context.Context) (store.OverallSpend, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 3*time.Second {
			t.Error("database request lacks bounded deadline")
		}
		return store.OverallSpend{TotalMinor: 100, Categories: []store.SpendCategory{{Category: "Direct aid", AmountMinor: 100}}}, nil
	})
	response := httptest.NewRecorder()
	NewServer("", Services{InitiativeRead: reader}).Handler.ServeHTTP(response, httptest.NewRequest("GET", "/donor/public/spend", nil))
	want := `{"total_minor":100,"categories":[{"category":"Direct aid","amount_minor":100}]}`
	if response.Code != 200 || response.Header().Get("Cache-Control") != "public, max-age=60" ||
		strings.TrimSpace(response.Body.String()) != want {
		t.Fatalf("bad spend response %d %v %s", response.Code, response.Header(), response.Body)
	}
}

func TestOverallSpendFailureIsPrivateAndNotCached(t *testing.T) {
	reader := overallSpendFunc(func(context.Context) (store.OverallSpend, error) {
		return store.OverallSpend{}, errors.New("private database detail")
	})
	response := httptest.NewRecorder()
	NewServer("", Services{InitiativeRead: reader}).Handler.ServeHTTP(response, httptest.NewRequest("GET", "/donor/public/spend", nil))
	if response.Code != 503 || response.Header().Get("Cache-Control") != "no-store" || strings.Contains(response.Body.String(), "private") {
		t.Fatalf("unsafe error %s", response.Body.String())
	}
}
