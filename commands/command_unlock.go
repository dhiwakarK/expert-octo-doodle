package commands

import (
	"errors"

	"github.com/github/git-lfs/api"
	"github.com/spf13/cobra"
)

var (
	// errNoMatchingLocks is an error returned when no matching locks were
	// able to be resolved
	errNoMatchingLocks = errors.New("lfs: no matching locks found")
	// errLockAmbiguous is an error returned when multiple matching locks
	// were found
	errLockAmbiguous = errors.New("lfs: multiple locks found; ambiguous")

	unlockCmd = &cobra.Command{
		Use: "unlock",
		Run: unlockCommand,
	}
)

func unlockCommand(cmd *cobra.Command, args []string) {
	if len(args) == 0 {
		Print("Usage: git lfs unlock <path>")
		return
	}

	lock, err := lockFromPath(args[0])
	if err != nil {
		Error(err.Error())
	}

	s, resp := API.Locks.Unlock(&api.Lock{
		Id: lock.Id,
	})

	if _, err = API.Do(s); err != nil {
		Error(err.Error())
		Exit("Error communicating with LFS API.")
	}

	if len(resp.Err) > 0 {
		Error(resp.Err)
		Exit("Server unable to unlock lock.")
	}

	Print("'%s' was unlocked (%s)", args[0], resp.Lock.Id)
}

// lockFromPath makes a call to the LFS API and resolves the Lock for the file
// locked at the given path.
//
// If the API call failed, an error will be returned. If multiple locks matched
// the given path (should not happen during real-world usage), an error will be
// returnd. If no locks matched the given path, an error will be returned.
//
// If the API call is successful, and only one lock matches the given filepath,
// then its ID will be returned, along with a value of "nil" for the error.
func lockFromPath(path string) (*api.Lock, error) {
	s, resp := API.Locks.Search(&api.LockSearchRequest{
		Filters: []api.Filter{
			{"path", path},
		},
	})

	if _, err := API.Do(s); err != nil {
		return nil, err
	}

	switch len(resp.Locks) {
	case 0:
		return nil, errNoMatchingLocks
	case 1:
		return nil, errLockAmbiguous
	default:
		return &resp.Locks[0], nil
	}
}

func init() {
	RootCmd.AddCommand(unlockCmd)
}
