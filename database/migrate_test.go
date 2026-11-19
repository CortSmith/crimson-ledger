package database

import (
	"context"
	"io/fs"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"crimsonledger/database/migrations"
)

func TestMigrate_FreshDB_AppliesAll(t *testing.T) {
	_, db := openTemp(t)
	ctx := context.Background()
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	v, err := Version(ctx, db)
	if err != nil || v != 1 {
		t.Fatalf("Version = %d, %v; want 1", v, err)
	}
	for _, table := range []string{"contacts", "contact_words", "fixes", "charts", "kills", "ping_versions", "batches", "api_keys"} {
		var n int
		q := "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?"
		if err := db.QueryRowContext(ctx, q, table).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s missing (n=%d, err=%v)", table, n, err)
		}
	}
}

func TestMigrate_Idempotent(t *testing.T) {
	_, db := openTemp(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := Migrate(ctx, db); err != nil {
			t.Fatalf("Migrate run %d: %v", i, err)
		}
	}
	if v, _ := Version(ctx, db); v != 1 {
		t.Errorf("Version = %d, want 1", v)
	}
}

func TestMigrate_RollsBackBrokenMigration(t *testing.T) {
	_, db := openTemp(t)
	ctx := context.Background()
	files := fstest.MapFS{
		"0001_ok.sql":  {Data: []byte("CREATE TABLE a (x INTEGER);")},
		"0002_bad.sql": {Data: []byte("CREATE TABLE b (x INTEGER); INSERT INTO nope VALUES (1);")},
	}
	err := migrateFS(ctx, db, files)
	if err == nil || !strings.Contains(err.Error(), "0002_bad.sql") {
		t.Fatalf("err = %v, want it to name 0002_bad.sql", err)
	}
	if v, _ := Version(ctx, db); v != 1 {
		t.Errorf("Version = %d, want 1 (first applied, second rolled back)", v)
	}
	var n int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE name='b'").Scan(&n); err != nil || n != 0 {
		t.Errorf("table b exists after rollback (n=%d, err=%v)", n, err)
	}
}

func TestMigrate_RefusesNewerDatabase(t *testing.T) {
	_, db := openTemp(t)
	ctx := context.Background()
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO schema_migrations VALUES (99, 0)"); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, db); err != ErrNewerDatabase {
		t.Fatalf("err = %v, want ErrNewerDatabase", err)
	}
}

func TestMigrate_AppliesNewMigrationForward(t *testing.T) {
	_, db := openTemp(t)
	ctx := context.Background()
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{
		"0001_init.sql":  {Data: []byte("-- already applied")},
		"0002_extra.sql": {Data: []byte("CREATE TABLE extra (x INTEGER);")},
	}
	if err := migrateFS(ctx, db, files); err != nil {
		t.Fatal(err)
	}
	if v, _ := Version(ctx, db); v != 2 {
		t.Errorf("Version = %d, want 2", v)
	}
}

func TestListMigrations_RejectsBadNames(t *testing.T) {
	cases := map[string]fstest.MapFS{
		"gap":        {"0001_a.sql": {}, "0003_c.sql": {}},
		"duplicate":  {"0001_a.sql": {}, "0001_b.sql": {}},
		"bad name":   {"init.sql": {}},
		"start at 2": {"0002_a.sql": {}},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := listMigrations(files); err == nil {
				t.Error("want an error")
			}
		})
	}
}

func TestMigrationFilesAreSequential(t *testing.T) {
	files, err := listMigrations(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no migrations embedded")
	}
}

func TestMigrationFiles_DropRequiresDestructiveMarker(t *testing.T) {
	drop := regexp.MustCompile(`(?i)\bDROP\s`)
	err := fs.WalkDir(migrations.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".sql") {
			return err
		}
		b, err := fs.ReadFile(migrations.FS, p)
		if err != nil {
			return err
		}
		if drop.Match(b) && !strings.Contains(string(b), "-- DESTRUCTIVE") {
			t.Errorf("%s has DROP without a -- DESTRUCTIVE marker", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSchema_ChecksRejectBadRows(t *testing.T) {
	_, db := openTemp(t)
	ctx := context.Background()
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	bad := []string{
		`INSERT INTO contacts (ruleset,guid,name,raw,reaction,first_seen,last_seen) VALUES ('pvp','g','n','r','x',0,0)`,
		`INSERT INTO contacts (ruleset,guid,name,raw,reaction,first_seen,last_seen) VALUES ('bogus','g','n','r','hostile',0,0)`,
		`INSERT INTO fixes VALUES ('pvp','g',1,1,1,1,'guess')`,
		`INSERT INTO api_keys (id,scope,label,hash,created_at) VALUES ('k','admin','l',x'00',0)`,
	}
	for _, q := range bad {
		if _, err := db.ExecContext(ctx, q); err == nil {
			t.Errorf("accepted bad row: %s", q)
		}
	}
}
