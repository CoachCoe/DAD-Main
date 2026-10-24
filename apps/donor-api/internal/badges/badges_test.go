package badges

import "testing"

func TestCatalogHas24BadgesWithUniqueCodes(t *testing.T) {
	if len(Catalog) != 24 {
		t.Fatalf("catalog has %d badges, want 24", len(Catalog))
	}
	seen := map[string]bool{}
	for _, b := range Catalog {
		if b.Code == "" || b.Title == "" {
			t.Fatalf("badge missing code/title: %+v", b)
		}
		if seen[b.Code] {
			t.Fatalf("duplicate code %q", b.Code)
		}
		seen[b.Code] = true
	}
}

func TestEvaluateLiveRuleBoundaries(t *testing.T) {
	top2 := 2
	top3 := 3
	city50 := 50
	city51 := 51
	cases := []struct {
		name   string
		s      Signals
		want   []string
		absent []string
	}{
		{"nothing", Signals{}, nil, []string{"F", "1D", "7", "NAT2"}},
		{"first gift", Signals{FirstGiftMade: true}, []string{"1D"}, []string{"7"}},
		{"founding", Signals{FoundingQualified: true}, []string{"F"}, nil},
		{"streak 7 not 30", Signals{StreakDays: 7}, []string{"7"}, []string{"30"}},
		{"streak 1000 climbs all", Signals{StreakDays: 1000}, []string{"7", "30", "100", "Y1", "1K"}, []string{"Y3"}},
		{"streak 1095 adds Y3", Signals{StreakDays: 1095}, []string{"Y3", "1K"}, nil},
		{"comeback", Signals{ComebackReached30: true}, []string{"CB"}, nil},
		{"level up", Signals{RaisedAmount: true}, []string{"UP"}, nil},
		{"referrals 1", Signals{ConvertedReferrals: 1}, []string{"REF1"}, []string{"REF3"}},
		{"referrals 20 -> captain", Signals{ConvertedReferrals: 20}, []string{"REF1", "REF3", "REF12", "TC"}, nil},
		{"irl", Signals{HasIRLReferral: true}, []string{"IRL"}, nil},
		{"7 initiatives", Signals{DistinctInitiatives: 7}, []string{"INIT7"}, nil},
		{"6 initiatives not yet", Signals{DistinctInitiatives: 6}, nil, []string{"INIT7"}},
		{"national top 2", Signals{NationalTopPercent: &top2}, []string{"NAT2"}, nil},
		{"national top 3 not 2%", Signals{NationalTopPercent: &top3}, nil, []string{"NAT2"}},
		{"city 50", Signals{CityRank: &city50}, []string{"CITY50"}, nil},
		{"city 51 not top 50", Signals{CityRank: &city51}, nil, []string{"CITY50"}},
	}
	for _, c := range cases {
		got := Evaluate(c.s)
		for _, code := range c.want {
			if !got[code] {
				t.Errorf("%s: expected %q earned, got %v", c.name, code, keys(got))
			}
		}
		for _, code := range c.absent {
			if got[code] {
				t.Errorf("%s: %q should not be earned", c.name, code)
			}
		}
	}
}

func TestInertBadgesNeverEarned(t *testing.T) {
	// Even with every signal maxed, the Phase 4/6 badges have no signal and must
	// never be awarded by Evaluate.
	top := 1
	rank := 1
	all := Signals{
		FirstGiftMade: true, FoundingQualified: true, StreakDays: 100000,
		ComebackReached30: true, RaisedAmount: true, ConvertedReferrals: 1000,
		HasIRLReferral: true, DistinctInitiatives: 1000, NationalTopPercent: &top, CityRank: &rank,
	}
	got := Evaluate(all)
	for _, code := range []string{"V", "SU", "V3", "V10", "TP", "WT"} {
		if got[code] {
			t.Errorf("inert badge %q was earned", code)
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
