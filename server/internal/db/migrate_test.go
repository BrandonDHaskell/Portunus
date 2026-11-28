package db_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BrandonDHaskell/Portunus/server/internal/db"
)

func emptyMemDB(t *testing.T) *sql.DB {
	t.Helper()
	safe := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	conn, err := sql.Open("sqlite", fmt.Sprintf("file:mig_%s?mode=memory&cache=shared&_pragma=foreign_keys(1)", safe))
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { conn.Close() })
	return conn
}

func migrationFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("migrations/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("no migration files: %v", err)
	}
	return files
}

// Every embedded migration is applied once, in order, and a second run (the
// next server start) is a no-op.
func TestMigrate_AppliesAllOnceAndIsIdempotent(t *testing.T) {
	conn := emptyMemDB(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	rows, err := conn.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var versions []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		versions = append(versions, v)
	}
	files := migrationFiles(t)
	if len(versions) != len(files) {
		t.Fatalf("%d migrations recorded, %d files embedded", len(versions), len(files))
	}
	for i, v := range versions {
		if v != i+1 {
			t.Fatalf("recorded versions %v are not contiguous from 1", versions)
		}
	}
}

// Migration files must be numbered NNNN_name.sql with unique, gap-free
// numbers, or two branches adding the same number would silently skip one.
func TestMigrate_FilenamesAreUniqueAndContiguous(t *testing.T) {
	seen := map[string]string{}
	for i, f := range migrationFiles(t) {
		base := filepath.Base(f)
		num, _, ok := strings.Cut(base, "_")
		if !ok || len(num) != 4 {
			t.Errorf("%s: want NNNN_name.sql", base)
			continue
		}
		if prev, dup := seen[num]; dup {
			t.Errorf("migration number %s used by %s and %s", num, prev, base)
		}
		seen[num] = base
		if want := fmt.Sprintf("%04d", i+1); num != want {
			t.Errorf("%s: expected number %s (gap or misordering)", base, want)
		}
	}
}

func TestMigrate_ProducesSchemaWithForeignKeysIntact(t *testing.T) {
	conn := emptyMemDB(t)
	if err := db.Migrate(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	rows, err := conn.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("migrated schema has foreign key violations")
	}
	for _, table := range []string{"modules", "doors", "member_access", "module_authorizations", "access_events", "admin_users", "sessions", "roles", "role_permissions", "audit_log"} {
		var n int
		if err := conn.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s missing after migration", table)
		}
	}
	for _, gone := range []string{"credentials", "admin_user_credentials"} {
		var n int
		_ = conn.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, gone).Scan(&n)
		if n != 0 {
			t.Errorf("retired table %s still present (hazard #3)", gone)
		}
	}
}

func TestOpen_FileDatabase(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	path := filepath.Join(dir, "portunus.db")
	conn, err := db.Open(context.Background(), db.Config{Path: path})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()

	var fk int
	var journal string
	if err := conn.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Errorf("foreign_keys = %d, %v; want 1", fk, err)
	}
	if err := conn.QueryRow(`PRAGMA journal_mode`).Scan(&journal); err != nil || journal != "wal" {
		t.Errorf("journal_mode = %q, %v; want wal", journal, err)
	}
	var applied int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil || applied != len(migrationFiles(t)) {
		t.Errorf("migrations applied = %d, %v", applied, err)
	}

	if runtime.GOOS != "windows" {
		if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
			t.Errorf("db directory mode = %v, %v; want 0700", info.Mode().Perm(), err)
		}
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("db file mode = %v, %v; want 0600", info.Mode().Perm(), err)
		}
	}
}

func TestOpen_UnusablePath(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Open(context.Background(), db.Config{Path: filepath.Join(blocker, "portunus.db")}); err == nil {
		t.Fatal("Open succeeded with a parent path that is a file")
	}
}

func TestSeedDev_IsIdempotentAndCommissions(t *testing.T) {
	conn := emptyMemDB(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := db.SeedDev(ctx, conn, db.SeedDevOptions{KnownModules: []string{"door-a", " ", "door-b"}}); err != nil {
			t.Fatalf("SeedDev run %d: %v", i+1, err)
		}
	}
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM modules WHERE enabled = 1 AND commissioned_at_ms IS NOT NULL AND door_id = 'door_main'`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("commissioned seed modules = %d, %v; want 2", n, err)
	}

	conn2 := emptyMemDB(t)
	if err := db.Migrate(ctx, conn2); err != nil {
		t.Fatal(err)
	}
	if err := db.SeedDev(ctx, conn2, db.SeedDevOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := conn2.QueryRow(`SELECT COUNT(*) FROM modules WHERE module_id = 'door-001'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("default dev module missing: %d, %v", n, err)
	}
}
