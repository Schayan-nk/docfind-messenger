package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
	"github.com/Schayan-nk/docfind-messenger/internal/pgstore"
	"github.com/Schayan-nk/docfind-messenger/internal/sms"
)

// setPhone is `-set-phone <username> -phone <mobile>`: one account's mobile
// number, written straight to the account database -- the number SMS sign-in
// codes and password resets go to.
//
// RUN IT WITH THE SERVER STOPPED. A running server keeps every account in
// memory and writes them all back on its next save, which would put the old
// number back.
func setPhone(username, phone string) error {
	username = strings.ToLower(strings.TrimSpace(username))
	p, ok := sms.NormalizePhone(phone)
	if !ok {
		return fmt.Errorf("%q is not an Iranian mobile number", phone)
	}
	pc := pgstore.FromEnv()
	if !pc.Enabled() {
		return errors.New("accounts are not kept in PostgreSQL here (DOCFIND_PG_* unset); set the number on the users page")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := pgstore.Open(ctx, pc)
	if err != nil {
		return fmt.Errorf("the account database: %w", err)
	}
	defer db.Close()
	found, err := pgstore.NewAccounts(db).SetPhone(ctx, username, p)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no docfind account called %q", username)
	}
	fmt.Printf("%s: mobile set to %s\n", username, p[:4]+strings.Repeat("*", len(p)-8)+p[len(p)-4:])
	return nil
}

// setGrant is `docfind -set-grant <username> -perm <id> -state allow|deny|inherit`:
// one permission override, written straight to the account database, with
// the server stopped (as setPhone). The permission must be one the
// catalogue declares, or a report's.
func setGrant(username, perm, state string) error {
	username = strings.ToLower(strings.TrimSpace(username))
	perm = strings.TrimSpace(perm)
	switch state = strings.ToLower(strings.TrimSpace(state)); state {
	case "allow", "deny":
	case "inherit", "clear", "":
		state = ""
	default:
		return fmt.Errorf("-state is allow, deny or inherit, not %q", state)
	}
	if !auth.NewCatalogue().Has(perm) {
		return fmt.Errorf("%q is not a permission docfind declares", perm)
	}
	pc := pgstore.FromEnv()
	if !pc.Enabled() {
		return errors.New("accounts are not kept in PostgreSQL here (DOCFIND_PG_* unset); use the permissions page")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := pgstore.Open(ctx, pc)
	if err != nil {
		return fmt.Errorf("the account database: %w", err)
	}
	defer db.Close()
	found, err := pgstore.NewAccounts(db).SetGrant(ctx, username, perm, state)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no docfind account called %q", username)
	}
	if state == "" {
		state = "inherit (the role decides)"
	}
	fmt.Printf("%s: %s set to %s\n", username, perm, state)
	return nil
}
