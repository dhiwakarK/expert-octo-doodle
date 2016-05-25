package lfs

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/github/git-lfs/api"
	"github.com/github/git-lfs/config"
	"github.com/github/git-lfs/errutil"
	"github.com/github/git-lfs/progress"
	"github.com/github/git-lfs/tools"
	"github.com/github/git-lfs/vendor/_nuts/github.com/cheggaaa/pb"
	"github.com/github/git-lfs/vendor/_nuts/github.com/rubyist/tracerx"
)

func PointerSmudgeToFile(filename string, ptr *Pointer, download bool, cb progress.CopyCallback) error {
	os.MkdirAll(filepath.Dir(filename), 0755)
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("Could not create working directory file: %v", err)
	}
	defer file.Close()
	if err := PointerSmudge(file, ptr, filename, download, cb); err != nil {
		if errutil.IsDownloadDeclinedError(err) {
			// write placeholder data instead
			file.Seek(0, os.SEEK_SET)
			ptr.Encode(file)
			return err
		} else {
			return fmt.Errorf("Could not write working directory file: %v", err)
		}
	}
	return nil
}

func PointerSmudge(writer io.Writer, ptr *Pointer, workingfile string, download bool, cb progress.CopyCallback) error {
	mediafile, err := LocalMediaPath(ptr.Oid)
	if err != nil {
		return err
	}

	LinkOrCopyFromReference(ptr.Oid, ptr.Size)

	stat, statErr := os.Stat(mediafile)

	if statErr == nil && stat != nil {
		fileSize := stat.Size()
		if fileSize == 0 || fileSize != ptr.Size {
			tracerx.Printf("Removing %s, size %d is invalid", mediafile, fileSize)
			os.RemoveAll(mediafile)
			stat = nil
		}
	}

	if statErr != nil || stat == nil {
		if download {
			// TODO @sinbad use adapter, use readLocalFile on completion callback
			err = downloadFile(writer, ptr, workingfile, mediafile, cb)
		} else {
			return errutil.NewDownloadDeclinedError(nil)
		}
	} else {
		err = readLocalFile(writer, ptr, mediafile, workingfile, cb)
	}

	if err != nil {
		return errutil.NewSmudgeError(err, ptr.Oid, mediafile)
	}

	return nil
}

// PointerSmudgeObject uses a Pointer and ObjectResource to download the object to the
// media directory. It does not write the file to the working directory.
func PointerSmudgeObject(ptr *Pointer, obj *api.ObjectResource, cb progress.CopyCallback) error {
	mediafile, err := LocalMediaPath(obj.Oid)
	if err != nil {
		return err
	}

	stat, statErr := os.Stat(mediafile)
	if statErr == nil && stat != nil {
		fileSize := stat.Size()
		if fileSize == 0 || fileSize != obj.Size {
			tracerx.Printf("Removing %s, size %d is invalid", mediafile, fileSize)
			os.RemoveAll(mediafile)
			stat = nil
		}
	}

	if statErr != nil || stat == nil {
		// TODO @sinbad use adapter, use readLocalFile on completion callback
		err := downloadObject(ptr, obj, mediafile, cb)

		if err != nil {
			return errutil.NewSmudgeError(err, obj.Oid, mediafile)
		}
	}

	return nil
}

// TODO @sinbad remove
func downloadObject(ptr *Pointer, obj *api.ObjectResource, mediafile string, cb progress.CopyCallback) error {
	reader, size, err := api.DownloadObject(obj)
	if reader != nil {
		defer reader.Close()
	}

	if err != nil {
		return errutil.Errorf(err, "Error downloading %s", mediafile)
	}

	if ptr.Size == 0 {
		ptr.Size = size
	}

	if err := bufferDownloadedFile(mediafile, reader, ptr.Size, cb); err != nil {
		return errutil.Errorf(err, "Error buffering media file: %s", err)
	}

	return nil
}

// TODO @sinbad remove
func downloadFile(writer io.Writer, ptr *Pointer, workingfile, mediafile string, cb progress.CopyCallback) error {
	fmt.Fprintf(os.Stderr, "Downloading %s (%s)\n", workingfile, pb.FormatBytes(ptr.Size))
	reader, size, err := api.Download(filepath.Base(mediafile), ptr.Size)
	if reader != nil {
		defer reader.Close()
	}

	if err != nil {
		return errutil.Errorf(err, "Error downloading %s: %s", filepath.Base(mediafile), err)
	}

	if ptr.Size == 0 {
		ptr.Size = size
	}

	if err := bufferDownloadedFile(mediafile, reader, ptr.Size, cb); err != nil {
		return errutil.Errorf(err, "Error buffering media file: %s", err)
	}

	return readLocalFile(writer, ptr, mediafile, workingfile, nil)
}

// TODO @sinbad remove bufferDownloadedFile
// Writes the content of reader to filename atomically by writing to a temp file
// first, and confirming the content SHA-256 is valid. This is basically a copy
// of atomic.WriteFile() at:
//
//   https://github.com/natefinch/atomic/blob/a62ce929ffcc871a51e98c6eba7b20321e3ed62d/atomic.go#L12-L17
//
// filename - Absolute path to a file to write, with the filename a 64 character
//            SHA-256 hex signature.
// reader   - Any io.Reader
// size     - Expected byte size of the content. Used for the progress bar in
//            the optional CopyCallback.
// cb       - Optional CopyCallback object for providing download progress to
//            external Git LFS tools.
func bufferDownloadedFile(filename string, reader io.Reader, size int64, cb progress.CopyCallback) error {
	oid := filepath.Base(filename)
	f, err := ioutil.TempFile(LocalObjectTempDir(), oid+"-")
	if err != nil {
		return fmt.Errorf("cannot create temp file: %v", err)
	}

	defer func() {
		if err != nil {
			// Don't leave the temp file lying around on error.
			_ = os.Remove(f.Name()) // yes, ignore the error, not much we can do about it.
		}
	}()

	hasher := tools.NewHashingReader(reader)

	// ensure we always close f. Note that this does not conflict with  the
	// close below, as close is idempotent.
	defer f.Close()
	name := f.Name()
	written, err := tools.CopyWithCallback(f, hasher, size, cb)
	if err != nil {
		return fmt.Errorf("cannot write data to tempfile %q: %v", name, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("can't close tempfile %q: %v", name, err)
	}

	if actual := hasher.Hash(); actual != oid {
		return fmt.Errorf("Expected OID %s, got %s after %d bytes written", oid, actual, written)
	}

	// get the file mode from the original file and use that for the replacement
	// file, too.
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		// no original file
	} else if err != nil {
		return err
	} else {
		if err := os.Chmod(name, info.Mode()); err != nil {
			return fmt.Errorf("can't set filemode on tempfile %q: %v", name, err)
		}
	}

	if err := os.Rename(name, filename); err != nil {
		return fmt.Errorf("cannot replace %q with tempfile %q: %v", filename, name, err)
	}
	return nil
}

func readLocalFile(writer io.Writer, ptr *Pointer, mediafile string, workingfile string, cb progress.CopyCallback) error {
	reader, err := os.Open(mediafile)
	if err != nil {
		return errutil.Errorf(err, "Error opening media file.")
	}
	defer reader.Close()

	if ptr.Size == 0 {
		if stat, _ := os.Stat(mediafile); stat != nil {
			ptr.Size = stat.Size()
		}
	}

	if len(ptr.Extensions) > 0 {
		registeredExts := config.Config.Extensions()
		extensions := make(map[string]config.Extension)
		for _, ptrExt := range ptr.Extensions {
			ext, ok := registeredExts[ptrExt.Name]
			if !ok {
				err := fmt.Errorf("Extension '%s' is not configured.", ptrExt.Name)
				return errutil.Error(err)
			}
			ext.Priority = ptrExt.Priority
			extensions[ext.Name] = ext
		}
		exts, err := config.SortExtensions(extensions)
		if err != nil {
			return errutil.Error(err)
		}

		// pipe extensions in reverse order
		var extsR []config.Extension
		for i := range exts {
			ext := exts[len(exts)-1-i]
			extsR = append(extsR, ext)
		}

		request := &pipeRequest{"smudge", reader, workingfile, extsR}

		response, err := pipeExtensions(request)
		if err != nil {
			return errutil.Error(err)
		}

		actualExts := make(map[string]*pipeExtResult)
		for _, result := range response.results {
			actualExts[result.name] = result
		}

		// verify name, order, and oids
		oid := response.results[0].oidIn
		if ptr.Oid != oid {
			err = fmt.Errorf("Actual oid %s during smudge does not match expected %s", oid, ptr.Oid)
			return errutil.Error(err)
		}

		for _, expected := range ptr.Extensions {
			actual := actualExts[expected.Name]
			if actual.name != expected.Name {
				err = fmt.Errorf("Actual extension name '%s' does not match expected '%s'", actual.name, expected.Name)
				return errutil.Error(err)
			}
			if actual.oidOut != expected.Oid {
				err = fmt.Errorf("Actual oid %s for extension '%s' does not match expected %s", actual.oidOut, expected.Name, expected.Oid)
				return errutil.Error(err)
			}
		}

		// setup reader
		reader, err = os.Open(response.file.Name())
		if err != nil {
			return errutil.Errorf(err, "Error opening smudged file: %s", err)
		}
		defer reader.Close()
	}

	_, err = tools.CopyWithCallback(writer, reader, ptr.Size, cb)
	if err != nil {
		return errutil.Errorf(err, "Error reading from media file: %s", err)
	}

	return nil
}
