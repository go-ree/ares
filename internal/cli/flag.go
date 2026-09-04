package cli

import (
	"errors"
	"fmt"
	"strings"
)

const DefaultConfigPath = "config/default.yaml"

// Action identifies the operation selected by the command line.
type Action string

const (
	ActionServe         Action = "serve"
	ActionMigrateStatus Action = "migrate-status"
	ActionMigrateUp     Action = "migrate-up"
	ActionHelp          Action = "help"
)

// Options is the side-effect-free result of parsing command-line arguments.
type Options struct {
	Action             Action
	ConfigPath         string
	ResumeDirtyVersion string
}

// ErrUsage allows the program entry point to map malformed command lines to a
// stable exit code without inspecting error text.
var ErrUsage = errors.New("invalid command line")

// UsageError describes a command-line contract violation.
type UsageError struct {
	Message string
}

func (e *UsageError) Error() string {
	return e.Message
}

func (e *UsageError) Unwrap() error {
	return ErrUsage
}

// Parse parses args without reading or mutating process-global flag state.
// The config option is deliberately accepted before, between or after command
// words so both the historical invocation and explicit subcommands work.
func Parse(args []string) (Options, error) {
	options := Options{
		Action:     ActionServe,
		ConfigPath: DefaultConfigPath,
	}
	positionals := make([]string, 0, len(args))
	configSeen := false
	resumeSeen := false
	helpRequested := false

	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch {
		case argument == "-h" || argument == "--help":
			helpRequested = true
		case argument == "-config" || argument == "--config":
			if configSeen {
				return Options{}, usageError("配置参数不能重复")
			}
			value, next, err := optionValue(args, index, argument)
			if err != nil {
				return Options{}, err
			}
			options.ConfigPath = value
			configSeen = true
			index = next
		case strings.HasPrefix(argument, "-config=") || strings.HasPrefix(argument, "--config="):
			if configSeen {
				return Options{}, usageError("配置参数不能重复")
			}
			value, err := inlineOptionValue(argument, "config")
			if err != nil {
				return Options{}, err
			}
			options.ConfigPath = value
			configSeen = true
		case argument == "--resume-dirty":
			if resumeSeen {
				return Options{}, usageError("--resume-dirty 不能重复")
			}
			value, next, err := optionValue(args, index, argument)
			if err != nil {
				return Options{}, err
			}
			options.ResumeDirtyVersion = value
			resumeSeen = true
			index = next
		case strings.HasPrefix(argument, "--resume-dirty="):
			if resumeSeen {
				return Options{}, usageError("--resume-dirty 不能重复")
			}
			value, err := inlineOptionValue(argument, "resume-dirty")
			if err != nil {
				return Options{}, err
			}
			options.ResumeDirtyVersion = value
			resumeSeen = true
		case strings.HasPrefix(argument, "-"):
			return Options{}, usageError("未知参数 %q", argument)
		default:
			positionals = append(positionals, argument)
		}
	}

	if helpRequested && len(positionals) == 1 && positionals[0] == "migrate" {
		if resumeSeen {
			return Options{}, usageError("--resume-dirty 只能用于 migrate up")
		}
		options.Action = ActionHelp
		return options, nil
	}
	action, helpTopic, err := parseAction(positionals)
	if err != nil {
		return Options{}, err
	}
	if resumeSeen && action != ActionMigrateUp {
		return Options{}, usageError("--resume-dirty 只能用于 migrate up")
	}
	if helpRequested || helpTopic {
		options.Action = ActionHelp
		return options, nil
	}
	options.Action = action
	return options, nil
}

func parseAction(positionals []string) (Action, bool, error) {
	switch {
	case len(positionals) == 0:
		return ActionServe, false, nil
	case len(positionals) == 1 && positionals[0] == "help":
		return ActionHelp, true, nil
	case len(positionals) == 1 && positionals[0] == "serve":
		return ActionServe, false, nil
	case len(positionals) == 2 && positionals[0] == "serve" && positionals[1] == "help":
		return ActionServe, true, nil
	case len(positionals) == 1 && positionals[0] == "migrate":
		return "", false, usageError("migrate 需要 status 或 up 子命令")
	case len(positionals) == 2 && positionals[0] == "migrate" && positionals[1] == "help":
		return ActionHelp, true, nil
	case len(positionals) == 2 && positionals[0] == "migrate" && positionals[1] == "status":
		return ActionMigrateStatus, false, nil
	case len(positionals) == 3 && positionals[0] == "migrate" && positionals[1] == "status" && positionals[2] == "help":
		return ActionMigrateStatus, true, nil
	case len(positionals) == 2 && positionals[0] == "migrate" && positionals[1] == "up":
		return ActionMigrateUp, false, nil
	case len(positionals) == 3 && positionals[0] == "migrate" && positionals[1] == "up" && positionals[2] == "help":
		return ActionMigrateUp, true, nil
	default:
		return "", false, usageError("未知命令 %q", strings.Join(positionals, " "))
	}
}

func optionValue(args []string, index int, option string) (string, int, error) {
	next := index + 1
	if next >= len(args) || strings.HasPrefix(args[next], "-") || strings.TrimSpace(args[next]) == "" {
		return "", index, usageError("%s 缺少参数值", option)
	}
	return args[next], next, nil
}

func inlineOptionValue(argument, option string) (string, error) {
	_, value, found := strings.Cut(argument, "=")
	if !found || strings.TrimSpace(value) == "" {
		return "", usageError("--%s 缺少参数值", option)
	}
	return value, nil
}

func usageError(format string, args ...any) error {
	return &UsageError{Message: fmt.Sprintf(format, args...)}
}

// Usage returns the stable top-level help text. Help is a successful action;
// callers should print this text without loading configuration.
func Usage() string {
	return `用法：
  ares [-config <文件>] [serve]
  ares migrate status [--config <文件>]
  ares migrate up [--resume-dirty <版本>] [--config <文件>]

参数：
  -config, --config <文件>      配置文件路径（默认 config/default.yaml）
  --resume-dirty <版本>         从指定的 dirty 迁移版本继续
  -h, --help                    显示帮助
`
}
