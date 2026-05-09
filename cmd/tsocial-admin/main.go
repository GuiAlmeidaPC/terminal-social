package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/tsocial/tsocial/internal/store"
)

func usage() {
	fmt.Fprintln(os.Stderr, `tsocial-admin <command> [args]

commands:
  make-admin <handle>
  unmake-admin <handle>
  suspend <handle>
  unsuspend <handle>
  delete-room <name>
  prune-deleted [--older-than-days N]
  stats

env:
  TSOCIAL_DB   path to sqlite (default /var/lib/tsocial/tsocial.db)`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	dbPath := os.Getenv("TSOCIAL_DB")
	if dbPath == "" {
		dbPath = "/var/lib/tsocial/tsocial.db"
	}
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open db:", err)
		os.Exit(1)
	}
	defer st.Close()
	ctx := context.Background()
	cmd := os.Args[1]
	args := os.Args[2:]
	switch cmd {
	case "make-admin":
		need(args, 1)
		check(st.SetAdmin(ctx, args[0], true))
		fmt.Println("ok")
	case "unmake-admin":
		need(args, 1)
		check(st.SetAdmin(ctx, args[0], false))
		fmt.Println("ok")
	case "suspend":
		need(args, 1)
		check(st.SetSuspended(ctx, args[0], true))
		fmt.Println("ok")
	case "unsuspend":
		need(args, 1)
		check(st.SetSuspended(ctx, args[0], false))
		fmt.Println("ok")
	case "delete-room":
		need(args, 1)
		r, err := st.RoomByName(ctx, args[0])
		check(err)
		check(st.DeleteRoom(ctx, r.ID))
		fmt.Println("ok")
	case "prune-deleted":
		fs := flag.NewFlagSet("prune", flag.ExitOnError)
		days := fs.Int("older-than-days", 30, "purge messages soft-deleted older than N days")
		_ = fs.Parse(args)
		n, err := st.PurgeDeletedOlderThan(ctx, time.Duration(*days)*24*time.Hour)
		check(err)
		fmt.Printf("purged %d messages\n", n)
	case "stats":
		s, err := st.Stats(ctx)
		check(err)
		fmt.Printf("users=%d rooms=%d messages=%d\n", s.Users, s.Rooms, s.Messages)
	default:
		usage()
		os.Exit(2)
	}
	_ = strconv.Atoi
}

func need(args []string, n int) {
	if len(args) < n {
		usage()
		os.Exit(2)
	}
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
