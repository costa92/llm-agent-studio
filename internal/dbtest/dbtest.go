// Package dbtest spins up an isolated, uniquely-named Postgres database for a
// test package. Some checks scan the WHOLE database (health's orphan/divergence
// aggregates, capped at 5 sample ids) or drain a GLOBAL queue with no org filter
// (the worker's FOR UPDATE SKIP LOCKED claim). Those tests pass when their
// package runs alone but fail under `go test ./internal/...`, where every
// package shares one server DB and sibling packages' rows crowd the sample cap
// or get claimed first. Creating a fresh DB per package restores the
// run-alone isolation while keeping the whole suite green.
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CreateFresh derives a new uniquely-named database from baseDSN (cloning its
// connection params, swapping only the dbname) and returns the new database's
// DSN plus a drop function the caller MUST invoke when done. The database is
// created empty; callers migrate it through their existing storage.Open +
// Migrate path (migrations are idempotent). baseDSN must be a postgres:// URL.
// On any error nothing is created, so there is nothing to clean up.
func CreateFresh(ctx context.Context, baseDSN, prefix string) (dsn string, drop func(), err error) {
	u, err := url.Parse(baseDSN)
	if err != nil {
		return "", nil, fmt.Errorf("dbtest: parse dsn: %w", err)
	}
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", nil, fmt.Errorf("dbtest: rand: %w", err)
	}
	name := prefix + "_" + hex.EncodeToString(b[:])

	// Admin connection: same server, but the "postgres" maintenance DB, since
	// CREATE/DROP DATABASE cannot run while connected to the target itself.
	admin := *u
	admin.Path = "/postgres"
	pool, err := pgxpool.New(ctx, admin.String())
	if err != nil {
		return "", nil, fmt.Errorf("dbtest: admin connect: %w", err)
	}
	defer pool.Close()
	// name is prefix + hex (no metacharacters); quoted defensively all the same.
	if _, err := pool.Exec(ctx, `CREATE DATABASE "`+name+`"`); err != nil {
		return "", nil, fmt.Errorf("dbtest: create database %q: %w", name, err)
	}

	fresh := *u
	fresh.Path = "/" + name
	drop = func() {
		p, derr := pgxpool.New(context.Background(), admin.String())
		if derr != nil {
			return
		}
		defer p.Close()
		// WITH (FORCE) terminates any straggler sessions (PG 13+).
		_, _ = p.Exec(context.Background(), `DROP DATABASE IF EXISTS "`+name+`" WITH (FORCE)`)
	}
	return fresh.String(), drop, nil
}
