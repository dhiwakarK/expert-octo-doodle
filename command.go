package gitmedia

import (
	"flag"
	"os"
)

func SubCommand() string {
	if len(os.Args) < 2 {
		return "version"
	} else {
		return os.Args[1]
	}
}

func NewCommand(run func(*Command)) *Command {
	var args []string
	if len(os.Args) > 1 {
		args = os.Args[2:]
	}

	return &Command{
		FlagSet: flag.NewFlagSet(os.Args[0], flag.ExitOnError),
		Args:    args,
		run:     run,
	}
}

type Command struct {
	run     func(c *Command)
	FlagSet *flag.FlagSet
	Args    []string
}

func (c *Command) parse() {
	c.FlagSet.Parse(c.Args)
}

func (c *Command) Run() {
	c.run(c)
}
