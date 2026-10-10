// backfill-upstream-urls imports URLs from channels already assigned to upstreams.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"controltower/server/internal/dashboard"
	"controltower/server/internal/mysqlstore"
)

func main() {
	site := flag.String("site", "", "site to inspect")
	apply := flag.Bool("apply", false, "append unambiguous URLs; default is preview only")
	flag.Parse()
	if err := run(*site, *apply); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(site string, apply bool) error {
	if site == "" || os.Getenv("CT_DATABASE_DSN") == "" || os.Getenv("CT_SECRET_KEY") == "" {
		return fmt.Errorf("requires -site, CT_DATABASE_DSN and CT_SECRET_KEY (same as Server); migrations 128 through 132 must be applied")
	}
	db, err := mysqlstore.Open(os.Getenv("CT_DATABASE_DSN"))
	if err != nil {
		return fmt.Errorf("cannot open Control Tower database")
	}
	defer db.Close()
	store := mysqlstore.New(db)
	source := dashboard.BillingReadonlySource{Handler: &dashboard.PassthroughHandler{Config: store, SecretKey: os.Getenv("CT_SECRET_KEY")}}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	channels, err := source.CurrentChannels(ctx, site)
	if err != nil {
		return fmt.Errorf("cannot read site channel directory; check Server readonly configuration and secret key")
	}
	reports, err := store.BackfillBillingUpstreamURLs(ctx, site, channels, apply)
	if err != nil {
		return fmt.Errorf("URL backfill failed (no partial commit): %w", err)
	}
	output := struct {
		Site      string                           `json:"site"`
		Applied   bool                             `json:"applied"`
		Upstreams []mysqlstore.UpstreamURLBackfill `json:"upstreams"`
	}{site, apply, reports}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(output)
}
