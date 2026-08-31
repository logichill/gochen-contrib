package migration

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	gomigrate "gochen-runtime/db/migrate"
	"gochen/errors"
)

// CLIConfig 配置通用 migration 命令执行器。
type CLIConfig struct {
	Config           Config
	Args             []string
	Stdin            io.Reader
	Stdout           io.Writer
	SkipConfirmation bool
}

// RunCLI 针对配置的 migration 来源执行通用命令。
func RunCLI(ctx context.Context, cfg CLIConfig) error {
	stdout := cfg.Stdout
	if stdout == nil {
		stdout = io.Discard
	}
	migrationType, command, commandArgs, err := parseArgs(cfg.Args)
	if err != nil {
		PrintUsage(stdout)
		return err
	}
	if isHelpCommand(command) {
		PrintUsage(stdout)
		return nil
	}
	runnerConfig := cfg.Config
	if migrationType != "" {
		runnerConfig.MigrationType = migrationType
	}

	runner, err := NewRunner(ctx, runnerConfig)
	if err != nil {
		return err
	}
	defer func() { _ = runner.Close() }()

	switch command {
	case "up":
		if err := runner.Up(ctx); err != nil {
			if errors.Is(err, gomigrate.ErrNoChange) {
				_, _ = fmt.Fprintln(stdout, "database is already up to date")
				return nil
			}
			return err
		}
		_, _ = fmt.Fprintln(stdout, "database migration completed")
		return nil
	case "status":
		version, dirty, err := runner.Version(ctx)
		if err != nil {
			if errors.Is(err, gomigrate.ErrNilVersion) {
				_, _ = fmt.Fprintln(stdout, "current version: none")
				return nil
			}
			return err
		}
		_, _ = fmt.Fprintf(stdout, "current version: %d\n", version)
		_, _ = fmt.Fprintf(stdout, "dirty: %v\n", dirty)
		return nil
	case "force":
		if len(commandArgs) < 1 {
			return errors.NewCode(errors.InvalidInput, "force requires version")
		}
		version, err := strconv.ParseUint(commandArgs[0], 10, 64)
		if err != nil {
			return errors.Wrap(err, errors.InvalidInput, "invalid migration version")
		}
		if err := runner.Force(ctx, version); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stdout, "forced version: %d\n", version)
		return nil
	case "down":
		if !cfg.SkipConfirmation {
			if err := confirm(cfg.Stdin, stdout, "this will roll back all migrations"); err != nil {
				return err
			}
		}
		if err := runner.Migrate(ctx, 0); err != nil {
			if errors.Is(err, gomigrate.ErrNoChange) || errors.Is(err, gomigrate.ErrNilVersion) {
				_, _ = fmt.Fprintln(stdout, "database has no migrations to roll back")
				return nil
			}
			return err
		}
		_, _ = fmt.Fprintln(stdout, "database migrations rolled back")
		return nil
	case "drop":
		if !cfg.SkipConfirmation {
			if err := confirm(cfg.Stdin, stdout, "this will drop all database tables"); err != nil {
				return err
			}
		}
		if err := runner.Drop(ctx); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stdout, "database tables dropped")
		return nil
	default:
		PrintUsage(stdout)
		return errors.NewCode(errors.InvalidInput, "unsupported migration command").WithContext("command", command)
	}
}

// PrintUsage 输出通用 migration 命令用法。
func PrintUsage(w io.Writer) {
	if w == nil {
		return
	}
	_, _ = fmt.Fprint(w, `Migration commands:
  up                 apply all pending migrations for the selected type (default)
  status             show current migration status for the selected type
  force <version>    force the selected type version and clear dirty state
  down               roll back all migrations for the selected type to version 0
  drop               drop all non-system database tables
  help               show this help

Migration type:
  The default type comes from Config.MigrationType, or "schema" when empty.
  Use -t <type> / --type <type> to run another safe migration namespace.

Examples:
  up
  -t demo up
  --type seed status
  -t seed force 2
`)
}

// IsDemoMigrationType reports whether the given CLI args specify a "demo" migration type.
func IsDemoMigrationType(args []string) bool {
	migrationType, _, _ := parseTypeFlag(args)
	return strings.EqualFold(migrationType, "demo")
}

func parseArgs(args []string) (string, string, []string, error) {
	migrationType, args, err := parseTypeFlag(args)
	if err != nil {
		return "", "", nil, err
	}
	if len(args) == 0 {
		return migrationType, "up", nil, nil
	}
	if isHelpCommand(args[0]) {
		return migrationType, args[0], nil, nil
	}

	first := strings.TrimSpace(args[0])
	if isMigrationCommand(first) {
		return migrationType, first, args[1:], nil
	}

	return migrationType, first, args[1:], nil
}

func parseTypeFlag(args []string) (string, []string, error) {
	migrationType := ""
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch {
		case arg == "-t" || arg == "--type":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return "", nil, errors.NewCode(errors.InvalidInput, "migration type flag requires a value")
			}
			if migrationType != "" {
				return "", nil, errors.NewCode(errors.InvalidInput, "migration type specified more than once")
			}
			migrationType = strings.TrimSpace(args[i+1])
			i++
		case strings.HasPrefix(arg, "--type="):
			value := strings.TrimSpace(strings.TrimPrefix(arg, "--type="))
			if value == "" {
				return "", nil, errors.NewCode(errors.InvalidInput, "migration type flag requires a value")
			}
			if migrationType != "" {
				return "", nil, errors.NewCode(errors.InvalidInput, "migration type specified more than once")
			}
			migrationType = value
		default:
			out = append(out, args[i])
		}
	}
	return migrationType, out, nil
}

func isHelpCommand(command string) bool {
	switch strings.TrimSpace(command) {
	case "help", "--help", "-h":
		return true
	default:
		return false
	}
}

func isMigrationCommand(command string) bool {
	switch strings.TrimSpace(command) {
	case "up", "status", "force", "down", "drop":
		return true
	default:
		return false
	}
}

func confirm(stdin io.Reader, stdout io.Writer, message string) error {
	if stdin == nil {
		return errors.NewCode(errors.InvalidInput, "confirmation input is required")
	}
	_, _ = fmt.Fprintf(stdout, "WARNING: %s. Type yes to continue:\n", message)
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && err != io.EOF {
		return err
	}
	if strings.TrimSpace(line) != "yes" {
		return errors.NewCode(errors.InvalidInput, "migration command canceled")
	}
	return nil
}
