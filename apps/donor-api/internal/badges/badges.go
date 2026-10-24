// Package badges is the badge catalog and earn engine (PRD §6.9, Appendix A).
// It is pure: Evaluate takes a snapshot of a donor's signals and returns the set
// of badge codes the donor currently qualifies for, so it is unit-testable
// without a database and the store layer only has to supply the snapshot and
// persist the permanent earn-state.
package badges

// Perk categories a badge may unlock (PRD B-3). Most badges have none.
type Perk string

const (
	PerkNone            Perk = ""
	PerkFoundingEvents  Perk = "FOUNDING_EVENTS"
	PerkMilestoneEvents Perk = "MILESTONE_EVENTS"
	PerkTeamCaptain     Perk = "TEAM_CAPTAIN"
)

// Badge is one catalog entry. Code is the stable identifier stored in the
// earn-state and emitted to clients; Title and Perk are display/entitlement
// data. Live is false for a badge whose earning signal does not exist yet
// (volunteer check-ins, teams — Phases 4 and 6): it stays in the catalog, shows
// as unearned, and the engine never awards it until its phase lands.
type Badge struct {
	Code  string
	Title string
	Perk  Perk
	Live  bool
}

// Catalog is the 24 approved badges in Appendix A order. Codes match the PRD.
var Catalog = []Badge{
	{Code: "F", Title: "Founding Donor", Perk: PerkFoundingEvents, Live: true},
	{Code: "1D", Title: "First Day", Live: true},
	{Code: "7", Title: "One Week", Live: true},
	{Code: "30", Title: "One Month", Live: true},
	{Code: "100", Title: "Hundred", Live: true},
	{Code: "Y1", Title: "One Year", Live: true},
	{Code: "1K", Title: "1,000 Days", Perk: PerkMilestoneEvents, Live: true},
	{Code: "Y3", Title: "Three Years", Live: true},
	{Code: "CB", Title: "Comeback", Live: true},
	{Code: "UP", Title: "Level Up", Live: true},
	{Code: "REF1", Title: "First Referral", Live: true},
	{Code: "REF3", Title: "Brought 3 In", Live: true},
	{Code: "REF12", Title: "Brought 12 In", Live: true},
	{Code: "TC", Title: "Team Captain", Perk: PerkTeamCaptain, Live: true},
	{Code: "IRL", Title: "IRL", Live: true},
	{Code: "V", Title: "Volunteer", Live: false},        // Phase 4: volunteer opt-in
	{Code: "SU", Title: "Showed Up", Live: false},       // Phase 4: event check-in
	{Code: "V3", Title: "Volunteer ×3", Live: false},    // Phase 4
	{Code: "V10", Title: "Volunteer ×10", Live: false},  // Phase 4
	{Code: "INIT7", Title: "7 Initiatives", Live: true}, // A-4 distinct-initiative exposures
	{Code: "CITY50", Title: "Top 50 City", Live: true},  // 3.3 rankings
	{Code: "NAT2", Title: "Top 2%", Live: true},         // 3.3 rankings
	{Code: "TP", Title: "Team Player", Live: false},     // Phase 6: teams
	{Code: "WT", Title: "Winning Team", Live: false},    // Phase 6: team leaderboard
}

// byCode indexes the catalog for O(1) lookup.
var byCode = func() map[string]Badge {
	m := make(map[string]Badge, len(Catalog))
	for _, b := range Catalog {
		m[b.Code] = b
	}
	return m
}()

// Lookup returns the catalog badge for a code.
func Lookup(code string) (Badge, bool) { b, ok := byCode[code]; return b, ok }

// CodesForPerk returns the badge codes that carry a given perk. Used by the
// invite-only event gate (E-5): a donor "holds" the perk when they hold any of
// these badges. Returns nil for PerkNone or an unknown perk.
func CodesForPerk(perk Perk) []string {
	if perk == PerkNone {
		return nil
	}
	var codes []string
	for _, b := range Catalog {
		if b.Perk == perk {
			codes = append(codes, b.Code)
		}
	}
	return codes
}

// HoldsPerk reports whether any of the donor's earned badge codes carries perk.
func HoldsPerk(earned map[string]bool, perk Perk) bool {
	if perk == PerkNone {
		return false
	}
	for _, code := range CodesForPerk(perk) {
		if earned[code] {
			return true
		}
	}
	return false
}

// Signals is the donor's current earning state, assembled by the store from the
// projection, the streak ladder, the initiative-exposure ledger and the ranking
// rollup. A signal whose source phase has not shipped is simply zero/false, so
// the inert badges never qualify.
type Signals struct {
	FirstGiftMade       bool  // completed a first gift
	FoundingQualified   bool  // first gift before the Founding cutoff / waitlist
	StreakDays          int64 // days since the current run began
	ComebackReached30   bool  // restarted after a lapse and reached a 30-day run
	RaisedAmount        bool  // increased their monthly amount at least once
	ConvertedReferrals  int64 // referrals who completed a gift
	HasIRLReferral      bool  // someone joined by scanning their IRL code
	DistinctInitiatives int64 // distinct initiatives funded/shown (A-4)
	NationalTopPercent  *int  // national percentile rank (1 = top 1%); nil if unranked
	CityRank            *int  // rank within their city; nil if unranked
}

// milestoneDays maps the streak-day badges to their thresholds.
var streakDayBadges = []struct {
	code string
	days int64
}{{"7", 7}, {"30", 30}, {"100", 100}, {"Y1", 365}, {"1K", 1000}, {"Y3", 1095}}

// Evaluate returns the set of badge codes the donor currently qualifies for,
// considering only Live badges with available signals. It is monotonic in the
// sense the caller relies on: it reports current qualification; the store then
// unions this with the permanent earn-state so a badge never un-earns (PRD B-2).
func Evaluate(s Signals) map[string]bool {
	earned := map[string]bool{}
	if s.FoundingQualified {
		earned["F"] = true
	}
	if s.FirstGiftMade {
		earned["1D"] = true
	}
	for _, m := range streakDayBadges {
		if s.StreakDays >= m.days {
			earned[m.code] = true
		}
	}
	if s.ComebackReached30 {
		earned["CB"] = true
	}
	if s.RaisedAmount {
		earned["UP"] = true
	}
	for _, r := range []struct {
		code string
		n    int64
	}{{"REF1", 1}, {"REF3", 3}, {"REF12", 12}, {"TC", 20}} {
		if s.ConvertedReferrals >= r.n {
			earned[r.code] = true
		}
	}
	if s.HasIRLReferral {
		earned["IRL"] = true
	}
	if s.DistinctInitiatives >= 7 {
		earned["INIT7"] = true
	}
	if s.NationalTopPercent != nil && *s.NationalTopPercent <= 2 {
		earned["NAT2"] = true
	}
	if s.CityRank != nil && *s.CityRank <= 50 {
		earned["CITY50"] = true
	}
	// Drop any code that is not a Live catalog badge (defensive — none of the
	// above are inert, but this keeps Evaluate honest if the catalog changes).
	for code := range earned {
		if b, ok := byCode[code]; !ok || !b.Live {
			delete(earned, code)
		}
	}
	return earned
}
