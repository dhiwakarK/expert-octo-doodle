package lfs

import (
	"fmt"
	"path/filepath"

	"github.com/git-lfs/git-lfs/api"
	"github.com/git-lfs/git-lfs/config"
	"github.com/git-lfs/git-lfs/errors"
	"github.com/git-lfs/git-lfs/tools/longpathos"
	"github.com/git-lfs/git-lfs/transfer"
)

// Uploadable describes a file that can be uploaded.
type Uploadable struct {
	oid      string
	OidPath  string
	Filename string
	size     int64
	object   *api.ObjectResource
}

func (u *Uploadable) Object() *api.ObjectResource {
	return u.object
}

func (u *Uploadable) Oid() string {
	return u.oid
}

func (u *Uploadable) Size() int64 {
	return u.size
}

func (u *Uploadable) Name() string {
	return u.Filename
}

func (u *Uploadable) SetObject(o *api.ObjectResource) {
	u.object = o
}

func (u *Uploadable) Path() string {
	return u.OidPath
}

// TODO LEGACY API: remove when legacy API removed
func (u *Uploadable) LegacyCheck() (*api.ObjectResource, error) {
	return api.UploadCheck(config.Config, u.Oid(), u.Size())
}

// NewUploadable builds the Uploadable from the given information.
// "filename" can be empty if a raw object is pushed (see "object-id" flag in push command)/
func NewUploadable(oid, filename string) (*Uploadable, error) {
	localMediaPath, err := LocalMediaPath(oid)
	if err != nil {
		return nil, errors.Wrapf(err, "Error uploading file %s (%s)", filename, oid)
	}

	if len(filename) > 0 {
		if err := ensureFile(filename, localMediaPath); err != nil {
			return nil, err
		}
	}

	fi, err := longpathos.Stat(localMediaPath)
	if err != nil {
		return nil, errors.Wrapf(err, "Error uploading file %s (%s)", filename, oid)
	}

	return &Uploadable{oid: oid, OidPath: localMediaPath, Filename: filename, size: fi.Size()}, nil
}

// NewUploadQueue builds an UploadQueue, allowing `workers` concurrent uploads.
func NewUploadQueue(files int, size int64, dryRun bool) *TransferQueue {
	return newTransferQueue(files, size, dryRun, transfer.Upload)
}

// ensureFile makes sure that the cleanPath exists before pushing it.  If it
// does not exist, it attempts to clean it by reading the file at smudgePath.
func ensureFile(smudgePath, cleanPath string) error {
	if _, err := longpathos.Stat(cleanPath); err == nil {
		return nil
	}

	expectedOid := filepath.Base(cleanPath)
	localPath := filepath.Join(config.LocalWorkingDir, smudgePath)
	file, err := longpathos.Open(localPath)
	if err != nil {
		return err
	}

	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return err
	}

	cleaned, err := PointerClean(file, file.Name(), stat.Size(), nil)
	if cleaned != nil {
		cleaned.Teardown()
	}

	if err != nil {
		return err
	}

	if expectedOid != cleaned.Oid {
		return fmt.Errorf("Trying to push %q with OID %s.\nNot found in %s.", smudgePath, expectedOid, filepath.Dir(cleanPath))
	}

	return nil
}
