// Package geo resolves a US ZIP code to the donor's IANA time zone, which is
// what lets day-level streak state and the streak-at-risk reminder land in the
// donor's own day rather than a single server day (PRD §10). The mapping is by
// the ZIP's leading three digits (its SCF, the sectional center facility whose
// region a ZIP belongs to); that is the coarsest key that still names one zone
// for nearly every ZIP, and it needs no multi-megabyte per-ZIP database.
//
// A handful of three-digit regions genuinely straddle a zone boundary (parts of
// Florida, Michigan's UP, Idaho, Oregon, North Dakota, Nebraska, Kansas,
// Texas). Those are resolved by the whole five-digit ZIP where it is cheap to
// be exact and left at the region's majority zone otherwise; a donor can always
// be corrected from their profile, so the lookup favours a good default over a
// large table.
package geo

import (
	_ "embed"
	"encoding/json"
	"errors"
	"regexp"
	"time"
	_ "time/tzdata"
)

// ErrZip is returned for input that is not a US ZIP the table can resolve.
var ErrZip = errors.New("enter a valid US ZIP code")

// zipPattern accepts ZIP5 or ZIP+4; only the five-digit core is used for the
// lookup. It mirrors the ck_donor_zip database constraint.
var zipPattern = regexp.MustCompile(`^[0-9]{5}(-[0-9]{4})?$`)

//go:embed zones.json
var encodedZones []byte

type zoneTable struct {
	// Prefix maps a three-digit ZIP prefix ("0" through "999", left-padded) to
	// an IANA zone name.
	Prefix map[string]string `json:"prefix"`
	// Override maps a full five-digit ZIP to a zone, for the ZIPs whose prefix
	// region straddles a time-zone boundary.
	Override map[string]string `json:"override"`
}

var zones = func() zoneTable {
	var t zoneTable
	if err := json.Unmarshal(encodedZones, &t); err != nil {
		panic("invalid embedded geo zones")
	}
	// Every zone the table names must load, so a bad name fails at startup, not
	// when the first donor in that region saves a profile.
	for _, name := range t.Prefix {
		if _, err := time.LoadLocation(name); err != nil {
			panic("geo: unknown IANA zone " + name)
		}
	}
	for _, name := range t.Override {
		if _, err := time.LoadLocation(name); err != nil {
			panic("geo: unknown IANA zone " + name)
		}
	}
	return t
}()

// NormalizeZip returns the canonical ZIP form (digits, optional +4 hyphen) or
// ErrZip. It does not confirm the ZIP resolves to a zone; use Timezone for that.
func NormalizeZip(zip string) (string, error) {
	if !zipPattern.MatchString(zip) {
		return "", ErrZip
	}
	return zip, nil
}

// Timezone returns the IANA zone name for a ZIP: the full-ZIP override when one
// exists, otherwise the ZIP's three-digit-prefix region. A ZIP whose region is
// not in the table (an unassigned or territorial range) returns ErrZip rather
// than a wrong guess.
func Timezone(zip string) (string, error) {
	if !zipPattern.MatchString(zip) {
		return "", ErrZip
	}
	five := zip[:5]
	if name, ok := zones.Override[five]; ok {
		return name, nil
	}
	if name, ok := zones.Prefix[five[:3]]; ok {
		return name, nil
	}
	return "", ErrZip
}
