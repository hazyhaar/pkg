package dbsync_test

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/hazyhaar/pkg/dbsync"
)

func ExampleNewPublisher_static() {
	var db *sql.DB // your source database

	tlsCfg := dbsync.SyncClientTLSConfig(true) // dev mode

	pub := dbsync.NewPublisher(db, dbsync.NewStaticTargetProvider(
		dbsync.Target{Name: "fo-1", Strategy: "dbsync", Endpoint: "10.0.0.2:9443"},
	), dbsync.FilterSpec{
		FullTables: []string{"posts", "categories"},
	}, "db/public.db", tlsCfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go pub.Start(ctx)
	// Publisher now watches db and pushes snapshots to fo-1.

	_ = pub // use pub
}

func ExampleNewPublisherWithRoutesDB() {
	var db, routesDB *sql.DB // source + connectivity routes databases

	tlsCfg := dbsync.SyncClientTLSConfig(true)

	pub := dbsync.NewPublisherWithRoutesDB(db, routesDB, dbsync.FilterSpec{
		FullTables: []string{"posts"},
		FilteredTables: map[string]string{
			"comments": "is_hidden = 0",
		},
	}, "db/public.db", tlsCfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go pub.Start(ctx)

	_ = pub // use pub
}

func ExampleNewSubscriber() {
	tlsCfg := dbsync.SyncClientTLSConfig(true) // would be SyncTLSConfig in prod

	sub := dbsync.NewSubscriber("db/public.db", ":9443", tlsCfg)
	sub.OnSwap(func() {
		slog.Info("database swapped", "version", sub.Version())
		// Recreate service instances with sub.DB()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sub.Start(ctx)

	_ = sub // use sub
}

func ExampleFilterSpec() {
	spec := dbsync.FilterSpec{
		// Tables copied without modification.
		FullTables: []string{"badges", "reputation_config", "rate_limits", "maintenance"},

		// Tables with row-level filtering.
		FilteredTables: map[string]string{
			"engagements": "visibility = 'public'",
			"templates":   "is_blacklisted = 0",
		},

		// Tables with column-level filtering and optional WHERE.
		PartialTables: map[string]dbsync.PartialTable{
			"users": {
				Columns: []string{"user_id", "username", "display_name", "avatar_url"},
				Where:   "is_active = 1",
			},
		},
	}

	err := dbsync.ValidateFilterSpec(spec)
	if err != nil {
		fmt.Println("invalid filter:", err)
	}
}
