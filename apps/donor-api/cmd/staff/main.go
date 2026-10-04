package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store"
)

func run() error {
	email := flag.String("email", "", "First administrator email")
	name := flag.String("name", "", "First administrator name")
	flag.Parse()
	if *email == "" || *name == "" {
		return errors.New("email and name are required")
	}
	origin := os.Getenv("DONOR_PUBLIC_ORIGIN")
	if err := payments.ValidateOrigin(origin); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := store.Open(ctx, os.Getenv("DONOR_GO_DATABASE_URL"))
	if err != nil {
		return errors.New("database is unavailable")
	}
	defer pool.Close()
	staff, err := store.NewStaffService(pool, os.Getenv("DONOR_STAFF_ENCRYPTION_KEY"))
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
	_, err = fmt.Fprintln(os.Stdout, "One-use private invitation: "+origin+"/staff/activate#token="+token)
	return err
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
