package store

import (
	"context"
	"fmt"

	"dad/donor-api/internal/store/queries"
	"github.com/jackc/pgx/v5/pgtype"
)

// Identity roles an address can hold. PRD §3 allows one per address: a partner
// employee who also gives uses a separate donor account with another email.
const (
	identityDonor   = "DONOR"
	identityStaff   = "STAFF"
	identityPartner = "PARTNER"
)

// EmailRoleError reports that an address already signs in to a different kind
// of account. Role is the role that holds it.
type EmailRoleError struct{ Role string }

func (e EmailRoleError) Error() string {
	return fmt.Sprintf("this email already belongs to a %s account", e.Role)
}

// Is lets errors.Is(err, EmailRoleError{}) match any role.
func (EmailRoleError) Is(target error) bool {
	_, ok := target.(EmailRoleError)
	return ok
}

// reserveEmailIdentity locks email for the rest of the transaction and refuses
// it if another kind of account holds it. partner is the organization a
// partner login is for: the same address may be re-invited there, but not
// added to a second organization.
func reserveEmailIdentity(ctx context.Context, q *queries.Queries, email, role string, partner pgtype.UUID) error {
	if err := q.LockEmailIdentity(ctx, email); err != nil {
		return err
	}
	holders, err := q.EmailIdentities(ctx, email)
	if err != nil {
		return err
	}
	for _, holder := range holders {
		if holder.Role != role || (role == identityPartner && holder.PartnerID != partner) {
			return EmailRoleError{Role: holder.Role}
		}
	}
	return nil
}
