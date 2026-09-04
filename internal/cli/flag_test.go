package cli

import (
	"errors"
	"strings"
	"testing"
)

func TestParseValidCommands(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want Options
	}{
		{name: "default serve", want: Options{Action: ActionServe, ConfigPath: DefaultConfigPath}},
		{name: "legacy config", args: []string{"-config", "custom.yaml"}, want: Options{Action: ActionServe, ConfigPath: "custom.yaml"}},
		{name: "legacy inline config", args: []string{"-config=custom.yaml"}, want: Options{Action: ActionServe, ConfigPath: "custom.yaml"}},
		{name: "explicit serve", args: []string{"serve"}, want: Options{Action: ActionServe, ConfigPath: DefaultConfigPath}},
		{name: "config before serve", args: []string{"--config", "before.yaml", "serve"}, want: Options{Action: ActionServe, ConfigPath: "before.yaml"}},
		{name: "config after serve", args: []string{"serve", "--config=after.yaml"}, want: Options{Action: ActionServe, ConfigPath: "after.yaml"}},
		{name: "migration status", args: []string{"migrate", "status"}, want: Options{Action: ActionMigrateStatus, ConfigPath: DefaultConfigPath}},
		{name: "config before migrate", args: []string{"--config", "before.yaml", "migrate", "status"}, want: Options{Action: ActionMigrateStatus, ConfigPath: "before.yaml"}},
		{name: "config between migrate and status", args: []string{"migrate", "--config", "between.yaml", "status"}, want: Options{Action: ActionMigrateStatus, ConfigPath: "between.yaml"}},
		{name: "config after status", args: []string{"migrate", "status", "--config=after.yaml"}, want: Options{Action: ActionMigrateStatus, ConfigPath: "after.yaml"}},
		{name: "migration up", args: []string{"migrate", "up"}, want: Options{Action: ActionMigrateUp, ConfigPath: DefaultConfigPath}},
		{name: "resume dirty", args: []string{"migrate", "up", "--resume-dirty", "20260903_003_versioned_migrations"}, want: Options{Action: ActionMigrateUp, ConfigPath: DefaultConfigPath, ResumeDirtyVersion: "20260903_003_versioned_migrations"}},
		{name: "inline resume before command", args: []string{"--resume-dirty=20260903_003_versioned_migrations", "migrate", "up", "--config", "migrate.yaml"}, want: Options{Action: ActionMigrateUp, ConfigPath: "migrate.yaml", ResumeDirtyVersion: "20260903_003_versioned_migrations"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Parse(test.args)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("Parse() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestParseHelp(t *testing.T) {
	tests := [][]string{
		{"--help"},
		{"help"},
		{"serve", "--help"},
		{"serve", "help"},
		{"migrate", "--help"},
		{"migrate", "help"},
		{"migrate", "status", "--help"},
		{"migrate", "status", "help"},
		{"migrate", "up", "--resume-dirty", "version", "--help"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			options, err := Parse(args)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if options.Action != ActionHelp {
				t.Fatalf("Parse() action = %q, want %q", options.Action, ActionHelp)
			}
		})
	}
}

func TestParseRejectsInvalidCommands(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "migrate missing subcommand", args: []string{"migrate"}},
		{name: "unsupported migration", args: []string{"migrate", "down"}},
		{name: "top level status", args: []string{"status"}},
		{name: "extra serve argument", args: []string{"serve", "extra"}},
		{name: "unknown option", args: []string{"--verbose"}},
		{name: "missing config value", args: []string{"--config"}},
		{name: "config followed by option", args: []string{"--config", "--help"}},
		{name: "empty inline config", args: []string{"--config="}},
		{name: "duplicate config", args: []string{"-config", "one.yaml", "--config=two.yaml"}},
		{name: "missing resume value", args: []string{"migrate", "up", "--resume-dirty"}},
		{name: "empty inline resume", args: []string{"migrate", "up", "--resume-dirty="}},
		{name: "duplicate resume", args: []string{"migrate", "up", "--resume-dirty", "one", "--resume-dirty=two"}},
		{name: "resume with serve", args: []string{"serve", "--resume-dirty", "version"}},
		{name: "resume with status", args: []string{"migrate", "status", "--resume-dirty", "version"}},
		{name: "resume with migrate help", args: []string{"migrate", "--help", "--resume-dirty", "version"}},
		{name: "unsupported short config", args: []string{"-c", "config.yaml"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse(test.args)
			if !errors.Is(err, ErrUsage) {
				t.Fatalf("Parse() error = %v, want ErrUsage", err)
			}
			var usageErr *UsageError
			if !errors.As(err, &usageErr) {
				t.Fatalf("Parse() error type = %T, want *UsageError", err)
			}
		})
	}
}

func TestParseDoesNotRetainState(t *testing.T) {
	first, err := Parse([]string{"migrate", "up", "--config", "first.yaml", "--resume-dirty", "version"})
	if err != nil {
		t.Fatalf("first Parse() error = %v", err)
	}
	second, err := Parse(nil)
	if err != nil {
		t.Fatalf("second Parse() error = %v", err)
	}
	if first.ConfigPath == second.ConfigPath || second != (Options{Action: ActionServe, ConfigPath: DefaultConfigPath}) {
		t.Fatalf("second Parse() retained state: %#v", second)
	}
}

func TestUsageDocumentsCommands(t *testing.T) {
	usage := Usage()
	for _, expected := range []string{"ares migrate status", "ares migrate up", "--resume-dirty", "config/default.yaml"} {
		if !strings.Contains(usage, expected) {
			t.Fatalf("Usage() does not contain %q", expected)
		}
	}
}
