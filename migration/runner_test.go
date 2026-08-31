package migration

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gochen/db"
	"gochen/db/dialect"
	"gochen/errors"
)

func TestRunCLIDefaultAndNamespacedMigrations(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "000001_init.up.sql"), "CREATE TABLE users (id INTEGER PRIMARY KEY); INSERT INTO users (id) VALUES (1);")
	writeFile(t, filepath.Join(dir, "000001_init.down.sql"), "DROP TABLE IF EXISTS users;")
	writeFile(t, filepath.Join(dir, "000001.demo.demo.up.sql"), "CREATE TABLE demo_users (id INTEGER PRIMARY KEY); INSERT INTO demo_users (id) VALUES (2);")
	writeFile(t, filepath.Join(dir, "000001.demo.demo.down.sql"), "DROP TABLE IF EXISTS demo_users;")
	writeFile(t, filepath.Join(dir, "000001.seed.seed.up.sql"), "CREATE TABLE seed_users (id INTEGER PRIMARY KEY); INSERT INTO seed_users (id) VALUES (3);")
	writeFile(t, filepath.Join(dir, "000001.seed.seed.down.sql"), "DROP TABLE IF EXISTS seed_users;")

	dbPath := filepath.Join(t.TempDir(), "app.db")
	cfg := Config{Driver: "sqlite", DSN: dbPath, SourceDir: dir}

	var upOut bytes.Buffer
	if err := RunCLI(context.Background(), CLIConfig{Config: cfg, Args: []string{"up"}, Stdout: &upOut}); err != nil {
		t.Fatalf("RunCLI default schema up: %v", err)
	}
	if !strings.Contains(upOut.String(), "database migration completed") {
		t.Fatalf("default schema up output = %q, want completion message", upOut.String())
	}

	dbConn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer dbConn.Close()
	assertTableExists(t, dbConn, "users")
	assertRowCount(t, dbConn, "users", 1)
	assertTableMissing(t, dbConn, "demo_users")

	var defaultStatusOut bytes.Buffer
	if err := RunCLI(context.Background(), CLIConfig{Config: cfg, Args: []string{"status"}, Stdout: &defaultStatusOut}); err != nil {
		t.Fatalf("RunCLI status: %v", err)
	}
	if !strings.Contains(defaultStatusOut.String(), "current version: 1") || !strings.Contains(defaultStatusOut.String(), "dirty: false") {
		t.Fatalf("default status output = %q", defaultStatusOut.String())
	}

	if err := RunCLI(context.Background(), CLIConfig{Config: cfg, Args: []string{"-t", "demo", "up"}}); err != nil {
		t.Fatalf("RunCLI -t demo up: %v", err)
	}
	assertTableExists(t, dbConn, "demo_users")
	assertRowCount(t, dbConn, "demo_users", 1)

	if err := RunCLI(context.Background(), CLIConfig{Config: cfg, Args: []string{"-t", "seed", "up"}}); err != nil {
		t.Fatalf("RunCLI -t seed up: %v", err)
	}
	assertTableExists(t, dbConn, "seed_users")
	assertRowCount(t, dbConn, "seed_users", 1)

	var out bytes.Buffer
	if err := RunCLI(context.Background(), CLIConfig{Config: cfg, Args: []string{"status", "--type", "seed"}, Stdout: &out}); err != nil {
		t.Fatalf("RunCLI status --type seed: %v", err)
	}
	if !strings.Contains(out.String(), "current version: 1") || !strings.Contains(out.String(), "dirty: false") {
		t.Fatalf("seed status output = %q", out.String())
	}
}

func TestRunCLIRejectsPositionalMigrationType(t *testing.T) {
	var out bytes.Buffer
	err := RunCLI(context.Background(), CLIConfig{
		Config: Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "typed.db"), SourceDir: t.TempDir()},
		Args:   []string{"demo", "up"},
		Stdout: &out,
	})
	if err == nil {
		t.Fatal("expected positional migration type to be rejected")
	}
	if !strings.Contains(err.Error(), "unsupported migration command") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunCLIHelpDocumentsMigrationType(t *testing.T) {
	var out bytes.Buffer
	if err := RunCLI(context.Background(), CLIConfig{Args: []string{"help"}, Stdout: &out}); err != nil {
		t.Fatalf("RunCLI help: %v", err)
	}
	got := out.String()
	for _, want := range []string{"-t <type>", "--type <type>", "-t demo up", "--type seed status"} {
		if !strings.Contains(got, want) {
			t.Fatalf("help output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\n  demo up\n") || strings.Contains(got, "\n  seed status\n") {
		t.Fatalf("help output should not document positional migration type:\n%s", got)
	}
}

func TestParseArgsMigrationTypeForms(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantType    string
		wantCommand string
		wantArgs    []string
		wantErr     bool
	}{
		{name: "default up", wantCommand: "up"},
		{name: "flag type", args: []string{"-t", "seed", "up"}, wantType: "seed", wantCommand: "up"},
		{name: "trailing flag type", args: []string{"up", "-t", "seed"}, wantType: "seed", wantCommand: "up"},
		{name: "equals flag type", args: []string{"--type=seed", "status"}, wantType: "seed", wantCommand: "status"},
		{name: "trailing equals flag type", args: []string{"status", "--type=seed"}, wantType: "seed", wantCommand: "status"},
		{name: "positional type is command", args: []string{"seed", "force", "2"}, wantCommand: "seed", wantArgs: []string{"force", "2"}},
		{name: "bare demo is command", args: []string{"demo"}, wantCommand: "demo"},
		{name: "unknown command stays command", args: []string{"statsu"}, wantCommand: "statsu"},
		{name: "missing type flag", args: []string{"-t"}, wantErr: true},
		{name: "duplicate type", args: []string{"-t", "seed", "--type", "demo", "up"}, wantErr: true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			gotType, gotCommand, gotArgs, err := parseArgs(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected parseArgs error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseArgs error = %v", err)
			}
			if gotType != tt.wantType || gotCommand != tt.wantCommand || !equalStrings(gotArgs, tt.wantArgs) {
				t.Fatalf("parseArgs() = (%q, %q, %#v), want (%q, %q, %#v)", gotType, gotCommand, gotArgs, tt.wantType, tt.wantCommand, tt.wantArgs)
			}
		})
	}
}

func TestRunCLIDownRequiresConfirmation(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "000001_init.up.sql"), "CREATE TABLE users (id INTEGER PRIMARY KEY);")
	writeFile(t, filepath.Join(dir, "000001_init.down.sql"), "DROP TABLE IF EXISTS users;")
	dbPath := filepath.Join(t.TempDir(), "app.db")
	cfg := Config{Driver: "sqlite", DSN: dbPath, SourceDir: dir}
	if err := Up(context.Background(), cfg); err != nil {
		t.Fatalf("Up: %v", err)
	}

	var out bytes.Buffer
	if err := RunCLI(context.Background(), CLIConfig{Config: cfg, Args: []string{"down"}, Stdin: strings.NewReader("yes\n"), Stdout: &out}); err != nil {
		t.Fatalf("RunCLI down: %v", err)
	}
	if !strings.Contains(out.String(), "rolled back") {
		t.Fatalf("down output = %q", out.String())
	}
}

func TestRunCLIDropRequiresConfirmation(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "000001_init.up.sql"), "CREATE TABLE users (id INTEGER PRIMARY KEY);")
	writeFile(t, filepath.Join(dir, "000001_init.down.sql"), "DROP TABLE IF EXISTS users;")
	dbPath := filepath.Join(t.TempDir(), "app.db")
	cfg := Config{Driver: "sqlite", DSN: dbPath, SourceDir: dir}
	if err := Up(context.Background(), cfg); err != nil {
		t.Fatalf("Up: %v", err)
	}

	var out bytes.Buffer
	if err := RunCLI(context.Background(), CLIConfig{Config: cfg, Args: []string{"drop"}, Stdin: strings.NewReader("yes\n"), Stdout: &out}); err != nil {
		t.Fatalf("RunCLI drop: %v", err)
	}
	if !strings.Contains(out.String(), "tables dropped") {
		t.Fatalf("drop output = %q", out.String())
	}

	dbConn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer dbConn.Close()
	assertTableMissing(t, dbConn, "users")
	assertTableMissing(t, dbConn, "schema_migrations")
}

func TestListTablesUsesCurrentSchemaForPostgres(t *testing.T) {
	database := &queryRecordingDB{dialectName: string(dialect.NamePostgres)}
	if _, err := listTables(context.Background(), database, dialect.New("postgres"), nil); err != nil {
		t.Fatalf("listTables: %v", err)
	}
	if !strings.Contains(database.query, "current_schema()") {
		t.Fatalf("query = %q, want current_schema()", database.query)
	}
	if strings.Contains(database.query, "'public'") {
		t.Fatalf("query = %q, should not hard-code public schema", database.query)
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}

func assertTableMissing(t *testing.T, dbConn *sql.DB, table string) {
	t.Helper()
	var count int
	if err := dbConn.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count); err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	if count != 0 {
		t.Fatalf("table %s exists", table)
	}
}

func assertRowCount(t *testing.T, dbConn *sql.DB, table string, wantCount int) {
	t.Helper()
	var count int
	if err := dbConn.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatalf("query row count for %s: %v", table, err)
	}
	if count != wantCount {
		t.Fatalf("table %s row count = %d, want %d", table, count, wantCount)
	}
}

func assertTableExists(t *testing.T, dbConn *sql.DB, table string) {
	t.Helper()
	var count int
	if err := dbConn.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count); err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	if count == 0 {
		t.Fatalf("table %s does not exist", table)
	}
}

func equalStrings(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type queryRecordingDB struct {
	db.IDatabase
	dialectName string
	query       string
}

func (d *queryRecordingDB) DialectName() string { return d.dialectName }

func (d *queryRecordingDB) Query(_ context.Context, query string, _ ...any) (db.IRows, error) {
	d.query = query
	return emptyRows{}, nil
}

type emptyRows struct{}

func (emptyRows) Next() bool                              { return false }
func (emptyRows) Scan(...any) error                       { return errors.New("unexpected scan") }
func (emptyRows) Close() error                            { return nil }
func (emptyRows) Err() error                              { return nil }
func (emptyRows) Columns() ([]string, error)              { return nil, nil }
func (emptyRows) ColumnTypes() ([]*sql.ColumnType, error) { return nil, nil }
