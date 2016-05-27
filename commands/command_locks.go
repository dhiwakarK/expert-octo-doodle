package commands

import (
	"github.com/github/git-lfs/api"
	"github.com/github/git-lfs/config"
	"github.com/spf13/cobra"
)

var (
	locksCmdFlags = new(locksFlags)
	locksCmd      = &cobra.Command{
		Use: "locks",
		Run: locksCommand,
	}
)

func locksCommand(cmd *cobra.Command, args []string) {
	setLockRemoteFor(config.Config)

	s, resp := API.Locks.Search(&api.LockSearchRequest{
		Filters: locksCmdFlags.Filters(),
		Cursor:  locksCmdFlags.Cursor,
		Limit:   locksCmdFlags.Limit,
	})

	if _, err := API.Do(s); err != nil {
		Error(err.Error())
		Exit("Error communicating with LFS API.")
	}

	Print("\n%d lock(s) matched query:", len(resp.Locks))
	for _, lock := range resp.Locks {
		Print("%s\t%s <%s>", lock.Path, lock.Committer.Name, lock.Committer.Email)
	}
}

func init() {
	locksCmd.Flags().StringVarP(&lockRemote, "remote", "r", config.Config.CurrentRemote, lockRemoteHelp)

	locksCmd.Flags().StringVarP(&locksCmdFlags.Path, "path", "p", "", "filter locks results matching a particular path")
	locksCmd.Flags().StringVarP(&locksCmdFlags.Id, "id", "i", "", "filter locks results matching a particular ID")
	locksCmd.Flags().StringVarP(&locksCmdFlags.Cursor, "cursor", "c", "", "cursor for last seen lock result")
	locksCmd.Flags().IntVarP(&locksCmdFlags.Limit, "limit", "l", 0, "optional limit for number of results to return")

	RootCmd.AddCommand(locksCmd)
}

// locksFlags wraps up and holds all of the flags that can be given to the
// `git lfs locks` command.
type locksFlags struct {
	// Path is an optional filter parameter to filter against the lock's
	// path
	Path string
	// Id is an optional filter parameter used to filtere against the lock's
	// ID.
	Id string
	// cursor is an optional request parameter used to indicate to the
	// server the position of the last lock seen by the client in a
	// paginated request.
	Cursor string
	// limit is an optional request parameter sent to the server used to
	// limit the
	Limit int
}

// Filters produces a slice of api.Filter instances based on the internal state
// of this locksFlags instance. The return value of this method is capable (and
// recommend to be used with) the api.LockSearchRequest type.
func (l *locksFlags) Filters() []api.Filter {
	filters := make([]api.Filter, 0)

	if l.Path != "" {
		filters = append(filters, api.Filter{"path", l.Path})
	}
	if l.Id != "" {
		filters = append(filters, api.Filter{"id", l.Id})
	}

	return filters
}
