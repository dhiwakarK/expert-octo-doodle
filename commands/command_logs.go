package commands

import (
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/github/git-lfs/config"
	"github.com/github/git-lfs/errors"
	"github.com/spf13/cobra"
)

func logsCommand(cmd *cobra.Command, args []string) {
	for _, path := range sortedLogs() {
		Print(path)
	}
}

func logsLastCommand(cmd *cobra.Command, args []string) {
	logs := sortedLogs()
	if len(logs) < 1 {
		Print("No logs to show")
		return
	}

	logsShowCommand(cmd, logs[len(logs)-1:])
}

func logsShowCommand(cmd *cobra.Command, args []string) {
	if len(args) == 0 {
		Print("Supply a log name.")
		return
	}

	name := args[0]
	by, err := ioutil.ReadFile(filepath.Join(config.LocalLogDir, name))
	if err != nil {
		Exit("Error reading log: %s", name)
	}

	Debug("Reading log: %s", name)
	os.Stdout.Write(by)
}

func logsClearCommand(cmd *cobra.Command, args []string) {
	err := os.RemoveAll(config.LocalLogDir)
	if err != nil {
		Panic(err, "Error clearing %s", config.LocalLogDir)
	}

	Print("Cleared %s", config.LocalLogDir)
}

func logsBoomtownCommand(cmd *cobra.Command, args []string) {
	Debug("Debug message")
	err := errors.Wrapf(errors.New("Inner error message!"), "Error")
	Panic(err, "Welcome to Boomtown")
	Debug("Never seen")
}

func sortedLogs() []string {
	fileinfos, err := ioutil.ReadDir(config.LocalLogDir)
	if err != nil {
		return []string{}
	}

	names := make([]string, 0, len(fileinfos))
	for _, info := range fileinfos {
		if info.IsDir() {
			continue
		}
		names = append(names, info.Name())
	}

	return names
}

func init() {
	RegisterSubcommand(func() *cobra.Command {
		cmd := &cobra.Command{
			Use:    "logs",
			PreRun: resolveLocalStorage,
			Run:    logsCommand,
		}

		cmd.AddCommand(
			&cobra.Command{
				Use:    "last",
				PreRun: resolveLocalStorage,
				Run:    logsLastCommand,
			},
			&cobra.Command{
				Use:    "show",
				PreRun: resolveLocalStorage,
				Run:    logsShowCommand,
			},
			&cobra.Command{
				Use:    "clear",
				PreRun: resolveLocalStorage,
				Run:    logsClearCommand,
			},
			&cobra.Command{
				Use:    "boomtown",
				PreRun: resolveLocalStorage,
				Run:    logsBoomtownCommand,
			},
		)
		return cmd
	})
}
