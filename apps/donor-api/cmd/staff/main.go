// Command staff invites the first administrator. Later staff are invited from
// the back office by an administrator.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store"
)

func run(args []string, getenv func(string) string, out io.Writer) error {
	flags := flag.NewFlagSet("staff", flag.ContinueOnError)
	email := flags.String("email", "", "First administrator email")
	name := flags.String("name", "", "First administrator name")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *email == "" || *name == "" {
		return errors.New("--email and --name are required")
	}
	origin := getenv("DONOR_PUBLIC_ORIGIN")
	if err := payments.ValidateOrigin(origin); err != nil {
		return err
	}
	dsn := getenv("DONOR_GO_DATABASE_URL")
	if dsn == "" {
		return errors.New("DONOR_GO_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := store.Open(ctx, dsn)
	if err != nil {
		return errors.New("database is unavailable")
	}
	defer pool.Close()
	staff, err := store.NewStaffService(pool, getenv("DONOR_STAFF_ENCRYPTION_KEY"))
	if err != nil {
		return errors.New("staff encryption is not configured correctly")
	}
	token, err := staff.Bootstrap(ctx, *email, *name)
	if errors.Is(err, store.ErrStaffConflict) {
		return errors.New("staff already exists; use an administrator account")
	}
	if err != nil {
		return errors.New("administrator invitation failed")
	}
	_, err = fmt.Fprintln(out, "One-use private invitation: "+origin+"/staff/activate#token="+token)
	return err
}

func main() {
	if err := run(os.Args[1:], os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
