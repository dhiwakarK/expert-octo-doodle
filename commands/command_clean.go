package commands

import (
	"io"
	"os"

	"github.com/git-lfs/git-lfs/errors"
	"github.com/git-lfs/git-lfs/lfs"
	"github.com/git-lfs/git-lfs/progress"
	"github.com/git-lfs/git-lfs/tools/longpathos"
	"github.com/spf13/cobra"
)

// clean cleans an object read from the given `io.Reader`, "from", and writes
// out a corresponding pointer to the `io.Writer`, "to". If there were any
// errors encountered along the way, they will be returned immediately if the
// error is non-fatal, otherwise they will halt using the built in
// `commands.Panic`.
//
// If the object read from "from" is _already_ a clean pointer, then it will be
// written out verbatim to "to", without trying to make it a pointer again.
func clean(to io.Writer, from io.Reader, fileName string) error {
	var cb progress.CopyCallback
	var file *os.File
	var fileSize int64

	if len(fileName) > 0 {
		stat, err := longpathos.Stat(fileName)
		if err == nil && stat != nil {
			fileSize = stat.Size()

			localCb, localFile, err := lfs.CopyCallbackFile("clean", fileName, 1, 1)
			if err != nil {
				Error(err.Error())
			} else {
				cb = localCb
				file = localFile
			}
		}
	}

	cleaned, err := lfs.PointerClean(from, fileName, fileSize, cb)
	if file != nil {
		file.Close()
	}

	if cleaned != nil {
		defer cleaned.Teardown()
	}

	if errors.IsCleanPointerError(err) {
		// If the contents read from the working directory was _already_
		// a pointer, we'll get a `CleanPointerError`, with the context
		// containing the bytes that we should write back out to Git.

		_, err = to.Write(errors.GetContext(err, "bytes").([]byte))
		return err
	}

	if err != nil {
		Panic(err, "Error cleaning asset.")
	}

	tmpfile := cleaned.Filename
	mediafile, err := lfs.LocalMediaPath(cleaned.Oid)
	if err != nil {
		Panic(err, "Unable to get local media path.")
	}

	if stat, _ := longpathos.Stat(mediafile); stat != nil {
		if stat.Size() != cleaned.Size && len(cleaned.Pointer.Extensions) == 0 {
			Exit("Files don't match:\n%s\n%s", mediafile, tmpfile)
		}
		Debug("%s exists", mediafile)
	} else {
		if err := longpathos.Rename(tmpfile, mediafile); err != nil {
			Panic(err, "Unable to move %s to %s\n", tmpfile, mediafile)
		}

		Debug("Writing %s", mediafile)
	}

	_, err = lfs.EncodePointer(to, cleaned.Pointer)
	return err
}

func cleanCommand(cmd *cobra.Command, args []string) {
	requireStdin("This command should be run by the Git 'clean' filter")
	lfs.InstallHooks(false)

	var fileName string
	if len(args) > 0 {
		fileName = args[0]
	}

	if err := clean(os.Stdout, os.Stdin, fileName); err != nil {
		Error(err.Error())
	}
}

func init() {
	RegisterCommand("clean", cleanCommand, nil)
}
