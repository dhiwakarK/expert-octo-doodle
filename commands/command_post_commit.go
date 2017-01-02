package commands

import (
	"os"

	"github.com/git-lfs/git-lfs/git"
	"github.com/git-lfs/git-lfs/locking"
	"github.com/rubyist/tracerx"
	"github.com/spf13/cobra"
)

// postCommitCommand is run through Git's post-commit hook. The hook passes
// no arguments.
// This hook checks that files which are lockable and not locked are made read-only,
// optimising that based on what was added / modified in the commit.
func postCommitCommand(cmd *cobra.Command, args []string) {
	requireGitVersion()

	lockClient, err := locking.NewClient(cfg)
	if err != nil {
		Exit("Unable to create lock system: %v", err)
	}

	// Skip this hook if no lockable patterns have been configured
	if len(lockClient.GetLockablePatterns()) == 0 ||
		!cfg.Os.Bool("GIT_LFS_SET_LOCKABLE_READONLY", true) {
		os.Exit(0)
	}

	tracerx.Printf("post-commit: checking file write flags at HEAD")
	// We can speed things up by looking at what changed in
	// HEAD, and only checking those lockable files
	files, err := git.GetFilesChanged("HEAD", "")

	if err != nil {
		LoggedError(err, "Warning: post-commit failed: %v", err)
		os.Exit(1)
	}
	tracerx.Printf("post-commit: checking write flags on %v", files)
	err = lockClient.FixLockableFileWriteFlags(files)
	if err != nil {
		LoggedError(err, "Warning: post-commit locked file check failed: %v", err)
	}

}

func init() {
	RegisterCommand("post-commit", postCommitCommand, nil)
}
