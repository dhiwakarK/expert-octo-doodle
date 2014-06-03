package commands

import (
	"flag"
	"github.com/github/git-media/gitmedia"
	"os"
	"os/exec"
	"path/filepath"
)

var commands = make(map[string]func(*Command) RunnableCommand)

func Run() {
	runcmd := true
	subname := SubCommand(1)

	if subname == "help" {
		runcmd = false
		subname = SubCommand(2)
	}

	cmd := NewCommand(filepath.Base(os.Args[0]), subname)
	cmdcb, ok := commands[subname]
	if ok {
		subcmd := cmdcb(cmd)
		subcmd.Setup()

		if runcmd {
			subcmd.Parse()
			subcmd.Run()
		} else {
			subcmd.Usage()
		}
	} else {
		missingCommand(cmd, subname)
	}
}

func SubCommand(pos int) string {
	if len(os.Args) < (pos + 1) {
		return "version"
	} else {
		return os.Args[pos]
	}
}

func NewCommand(name, subname string) *Command {
	var args []string
	if len(os.Args) > 1 {
		args = os.Args[2:]
	}

	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	gitmedia.SetupDebugging(fs)
	fs.SetOutput(gitmedia.ErrorWriter)

	return &Command{name, subname, fs, args, args}
}

func PipeMediaCommand(name string, args ...string) error {
	return PipeCommand("bin/"+name, args...)
}

func PipeCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	return cmd.Run()
}

type RunnableCommand interface {
	Setup()
	Parse()
	Run()
	Usage()
}

type Command struct {
	Name        string
	SubCommand  string
	FlagSet     *flag.FlagSet
	Args        []string
	SubCommands []string
}

func (c *Command) Usage() {
	gitmedia.Print("usage: %s %s", c.Name, c.SubCommand)
	c.FlagSet.PrintDefaults()
}

func (c *Command) Parse() {
	c.FlagSet.Parse(c.Args)
	c.SubCommands = c.FlagSet.Args()
}

func (c *Command) Setup() {}
func (c *Command) Run()   {}

func registerCommand(name string, cmdcb func(*Command) RunnableCommand) {
	commands[name] = cmdcb
}

func missingCommand(cmd *Command, subname string) {
	gitmedia.Error("%s: '%s' is not a %s command.  See %s help.",
		cmd.Name, subname, cmd.Name, cmd.Name)
}
