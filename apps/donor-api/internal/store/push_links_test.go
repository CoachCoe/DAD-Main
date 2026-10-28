package store

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"dad/donor-api/internal/store/queries"
)

func TestPushDestinationsAreAdmittedByMobileLinkPolicy(t *testing.T) {
	data, err := os.ReadFile("../../../../contracts/mobile/links.json")
	if err != nil {
		t.Fatal(err)
	}
	var policy struct {
		Account      []string `json:"account_paths"`
		Notification []string `json:"notification_paths"`
	}
	if err := json.Unmarshal(data, &policy); err != nil {
		t.Fatal(err)
	}
	for _, category := range []string{
		"REFERRAL_CONVERTED", "GIFT_PROCESSED", "STREAK_AT_RISK", "STREAK_MILESTONE",
		"EVENT_PROMOTED", "EVENT_NEARBY", "EVENT_CANCELLED", "EVENT_REMINDER", "STREAK_LAPSED", "BADGE_EARNED",
	} {
		t.Run(category, func(t *testing.T) {
			note, err := pushContent(queries.DonorPushNotification{
				Category: category,
				Context:  []byte(`{"kind":"LINK","gift_id":"synthetic-gift","days":30,"code":"1D"}`),
			})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(policy.Notification, note.Path) || !slices.Contains(policy.Account, note.Path) {
				t.Fatalf("push destination %q is not admitted by the mobile account and notification policy", note.Path)
			}
		})
	}
}
