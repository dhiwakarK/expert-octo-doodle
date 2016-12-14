package commands

import (
	"bytes"
	"io"
	"os/exec"
	"sync"

	"github.com/git-lfs/git-lfs/lfs"
	"github.com/git-lfs/git-lfs/transfer"
)

// Handles the process of checking out a single file, and updating the git
// index.
func newSingleCheckout() *singleCheckout {
	// Get a converter from repo-relative to cwd-relative
	// Since writing data & calling git update-index must be relative to cwd
	pathConverter, err := lfs.NewRepoToCurrentPathConverter()
	if err != nil {
		Panic(err, "Could not convert file paths")
	}

	return &singleCheckout{
		gitIndexer:    &gitIndexer{},
		pathConverter: pathConverter,
		manifest:      TransferManifest(),
	}
}

type singleCheckout struct {
	gitIndexer    *gitIndexer
	pathConverter lfs.PathConverter
	manifest      *transfer.Manifest
}

func (c *singleCheckout) Run(p *lfs.WrappedPointer) {
	cwdfilepath, err := checkout(p, c.pathConverter, c.manifest)
	if err != nil {
		LoggedError(err, "Checkout error: %s", err)
	}

	if len(cwdfilepath) > 0 {
		// errors are only returned when the gitIndexer is starting a new cmd
		if err := c.gitIndexer.Add(cwdfilepath); err != nil {
			Panic(err, "Could not update the index")
		}
	}
}

func (c *singleCheckout) Close() {
	if err := c.gitIndexer.Close(); err != nil {
		LoggedError(err, "Error updating the git index:\n%s", c.gitIndexer.Output())
	}
}

// Don't fire up the update-index command until we have at least one file to
// give it. Otherwise git interprets the lack of arguments to mean param-less update-index
// which can trigger entire working copy to be re-examined, which triggers clean filters
// and which has unexpected side effects (e.g. downloading filtered-out files)
type gitIndexer struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	output bytes.Buffer
	mu     sync.Mutex
}

func (i *gitIndexer) Add(path string) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.cmd == nil {
		// Fire up the update-index command
		i.cmd = exec.Command("git", "update-index", "-q", "--refresh", "--stdin")
		i.cmd.Stdout = &i.output
		i.cmd.Stderr = &i.output
		stdin, err := i.cmd.StdinPipe()
		if err == nil {
			err = i.cmd.Start()
		}

		if err != nil {
			return err
		}

		i.input = stdin
	}

	i.input.Write([]byte(path + "\n"))
	return nil
}

func (i *gitIndexer) Output() string {
	return i.output.String()
}

func (i *gitIndexer) Close() error {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.input != nil {
		i.input.Close()
	}

	if i.cmd != nil {
		return i.cmd.Wait()
	}

	return nil
}
