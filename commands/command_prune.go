package commands

import (
	"bytes"
	"fmt"
	"sync"
	"time"

	"github.com/git-lfs/git-lfs/config"
	"github.com/git-lfs/git-lfs/git"
	"github.com/git-lfs/git-lfs/lfs"
	"github.com/git-lfs/git-lfs/localstorage"
	"github.com/git-lfs/git-lfs/progress"
	"github.com/git-lfs/git-lfs/tools"
	"github.com/git-lfs/git-lfs/tools/longpathos"
	"github.com/rubyist/tracerx"
	"github.com/spf13/cobra"
)

var (
	pruneDryRunArg      bool
	pruneVerboseArg     bool
	pruneVerifyArg      bool
	pruneDoNotVerifyArg bool
)

func pruneCommand(cmd *cobra.Command, args []string) {
	// Guts of this must be re-usable from fetch --prune so just parse & dispatch
	if pruneVerifyArg && pruneDoNotVerifyArg {
		Exit("Cannot specify both --verify-remote and --no-verify-remote")
	}

	fetchPruneConfig := cfg.FetchPruneConfig()
	verify := !pruneDoNotVerifyArg &&
		(fetchPruneConfig.PruneVerifyRemoteAlways || pruneVerifyArg)
	prune(fetchPruneConfig, verify, pruneDryRunArg, pruneVerboseArg)
}

type PruneProgressType int

const (
	PruneProgressTypeLocal  = PruneProgressType(iota)
	PruneProgressTypeRetain = PruneProgressType(iota)
	PruneProgressTypeVerify = PruneProgressType(iota)
)

// Progress from a sub-task of prune
type PruneProgress struct {
	ProgressType PruneProgressType
	Count        int // Number of items done
}
type PruneProgressChan chan PruneProgress

func prune(fetchPruneConfig config.FetchPruneConfig, verifyRemote, dryRun, verbose bool) {
	localObjects := make([]localstorage.Object, 0, 100)
	retainedObjects := tools.NewStringSetWithCapacity(100)
	var reachableObjects tools.StringSet
	var taskwait sync.WaitGroup

	// Add all the base funcs to the waitgroup before starting them, in case
	// one completes really fast & hits 0 unexpectedly
	// each main process can Add() to the wg itself if it subdivides the task
	taskwait.Add(4) // 1..4: localObjects, current & recent refs, unpushed, worktree
	if verifyRemote {
		taskwait.Add(1) // 5
	}

	progressChan := make(PruneProgressChan, 100)

	// Collect errors
	errorChan := make(chan error, 10)
	var errorwait sync.WaitGroup
	errorwait.Add(1)
	var taskErrors []error
	go pruneTaskCollectErrors(&taskErrors, errorChan, &errorwait)

	// Populate the single list of local objects
	go pruneTaskGetLocalObjects(&localObjects, progressChan, &taskwait)

	// Now find files to be retained from many sources
	retainChan := make(chan string, 100)

	go pruneTaskGetRetainedCurrentAndRecentRefs(fetchPruneConfig, retainChan, errorChan, &taskwait)
	go pruneTaskGetRetainedUnpushed(fetchPruneConfig, retainChan, errorChan, &taskwait)
	go pruneTaskGetRetainedWorktree(retainChan, errorChan, &taskwait)
	if verifyRemote {
		reachableObjects = tools.NewStringSetWithCapacity(100)
		go pruneTaskGetReachableObjects(&reachableObjects, errorChan, &taskwait)
	}

	// Now collect all the retained objects, on separate wait
	var retainwait sync.WaitGroup
	retainwait.Add(1)
	go pruneTaskCollectRetained(&retainedObjects, retainChan, progressChan, &retainwait)

	// Report progress
	var progresswait sync.WaitGroup
	progresswait.Add(1)
	go pruneTaskDisplayProgress(progressChan, &progresswait)

	taskwait.Wait()   // wait for subtasks
	close(retainChan) // triggers retain collector to end now all tasks have
	retainwait.Wait() // make sure all retained objects added

	close(errorChan) // triggers error collector to end now all tasks have
	errorwait.Wait() // make sure all errors have been processed
	pruneCheckErrors(taskErrors)

	prunableObjects := make([]string, 0, len(localObjects)/2)

	// Build list of prunables (also queue for verify at same time if applicable)
	var verifyQueue *lfs.TransferQueue
	var verifiedObjects tools.StringSet
	var totalSize int64
	var verboseOutput bytes.Buffer
	var verifyc chan string
	var verifywait sync.WaitGroup

	if verifyRemote {
		cfg.CurrentRemote = fetchPruneConfig.PruneRemoteName
		// build queue now, no estimates or progress output
		verifyQueue = lfs.NewDownloadCheckQueue(0, 0)
		verifiedObjects = tools.NewStringSetWithCapacity(len(localObjects) / 2)

		// this channel is filled with oids for which Check() succeeded & Transfer() was called
		verifyc = verifyQueue.Watch()
		verifywait.Add(1)
		go func() {
			for oid := range verifyc {
				verifiedObjects.Add(oid)
				tracerx.Printf("VERIFIED: %v", oid)
				progressChan <- PruneProgress{PruneProgressTypeVerify, 1}
			}
			verifywait.Done()
		}()
	}

	for _, file := range localObjects {
		if !retainedObjects.Contains(file.Oid) {
			prunableObjects = append(prunableObjects, file.Oid)
			totalSize += file.Size
			if verbose {
				// Save up verbose output for the end, spinner still going
				verboseOutput.WriteString(fmt.Sprintf(" * %v (%v)\n", file.Oid, humanizeBytes(file.Size)))
			}

			if verifyRemote {
				tracerx.Printf("VERIFYING: %v", file.Oid)
				pointer := lfs.NewPointer(file.Oid, file.Size, nil)
				verifyQueue.Add(lfs.NewDownloadable(&lfs.WrappedPointer{Pointer: pointer}))
			}
		}
	}

	if verifyRemote {
		verifyQueue.Wait()
		verifywait.Wait()
		close(progressChan) // after verify (uses spinner) but before check
		progresswait.Wait()
		pruneCheckVerified(prunableObjects, reachableObjects, verifiedObjects)
	} else {
		close(progressChan)
		progresswait.Wait()
	}

	if len(prunableObjects) == 0 {
		Print("Nothing to prune")
		return
	}
	if dryRun {
		Print("%d files would be pruned (%v)", len(prunableObjects), humanizeBytes(totalSize))
		if verbose {
			Print(verboseOutput.String())
		}
	} else {
		Print("Pruning %d files, (%v)", len(prunableObjects), humanizeBytes(totalSize))
		if verbose {
			Print(verboseOutput.String())
		}
		pruneDeleteFiles(prunableObjects)
	}

}

func pruneCheckVerified(prunableObjects []string, reachableObjects, verifiedObjects tools.StringSet) {
	// There's no issue if an object is not reachable and missing, only if reachable & missing
	var problems bytes.Buffer
	for _, oid := range prunableObjects {
		// Test verified first as most likely reachable
		if !verifiedObjects.Contains(oid) {
			if reachableObjects.Contains(oid) {
				problems.WriteString(fmt.Sprintf(" * %v\n", oid))
			} else {
				// Just to indicate why it doesn't matter that we didn't verify
				tracerx.Printf("UNREACHABLE: %v", oid)
			}
		}
	}
	// technically we could still prune the other oids, but this indicates a
	// more serious issue because the local state implies that these can be
	// deleted but that's incorrect; bad state has occurred somehow, might need
	// push --all to resolve
	if problems.Len() > 0 {
		Exit("Abort: these objects to be pruned are missing on remote:\n%v", problems.String())
	}
}

func pruneCheckErrors(taskErrors []error) {
	if len(taskErrors) > 0 {
		for _, err := range taskErrors {
			LoggedError(err, "Prune error: %v", err)
		}
		Exit("Prune sub-tasks failed, cannot continue")
	}
}

func pruneTaskDisplayProgress(progressChan PruneProgressChan, waitg *sync.WaitGroup) {
	defer waitg.Done()

	spinner := progress.NewSpinner()
	localCount := 0
	retainCount := 0
	verifyCount := 0
	var msg string
	for p := range progressChan {
		switch p.ProgressType {
		case PruneProgressTypeLocal:
			localCount++
		case PruneProgressTypeRetain:
			retainCount++
		case PruneProgressTypeVerify:
			verifyCount++
		}
		msg = fmt.Sprintf("%d local objects, %d retained", localCount, retainCount)
		if verifyCount > 0 {
			msg += fmt.Sprintf(", %d verified with remote", verifyCount)
		}
		spinner.Print(OutputWriter, msg)
	}
	spinner.Finish(OutputWriter, msg)
}

func pruneTaskCollectRetained(outRetainedObjects *tools.StringSet, retainChan chan string,
	progressChan PruneProgressChan, retainwait *sync.WaitGroup) {

	defer retainwait.Done()

	for oid := range retainChan {
		if outRetainedObjects.Add(oid) {
			progressChan <- PruneProgress{PruneProgressTypeRetain, 1}
		}
	}

}

func pruneTaskCollectErrors(outtaskErrors *[]error, errorChan chan error, errorwait *sync.WaitGroup) {
	defer errorwait.Done()

	for err := range errorChan {
		*outtaskErrors = append(*outtaskErrors, err)
	}
}

func pruneDeleteFiles(prunableObjects []string) {
	spinner := progress.NewSpinner()
	var problems bytes.Buffer
	// In case we fail to delete some
	var deletedFiles int
	for i, oid := range prunableObjects {
		spinner.Print(OutputWriter, fmt.Sprintf("Deleting object %d/%d", i, len(prunableObjects)))
		mediaFile, err := lfs.LocalMediaPath(oid)
		if err != nil {
			problems.WriteString(fmt.Sprintf("Unable to find media path for %v: %v\n", oid, err))
			continue
		}
		err = longpathos.Remove(mediaFile)
		if err != nil {
			problems.WriteString(fmt.Sprintf("Failed to remove file %v: %v\n", mediaFile, err))
			continue
		}
		deletedFiles++
	}
	spinner.Finish(OutputWriter, fmt.Sprintf("Deleted %d files", deletedFiles))
	if problems.Len() > 0 {
		LoggedError(fmt.Errorf("Failed to delete some files"), problems.String())
		Exit("Prune failed, see errors above")
	}
}

// Background task, must call waitg.Done() once at end
func pruneTaskGetLocalObjects(outLocalObjects *[]localstorage.Object, progChan PruneProgressChan, waitg *sync.WaitGroup) {
	defer waitg.Done()

	localObjectsChan := lfs.ScanObjectsChan()
	for f := range localObjectsChan {
		*outLocalObjects = append(*outLocalObjects, f)
		progChan <- PruneProgress{PruneProgressTypeLocal, 1}
	}
}

// Background task, must call waitg.Done() once at end
func pruneTaskGetRetainedAtRef(ref string, retainChan chan string, errorChan chan error, waitg *sync.WaitGroup) {
	defer waitg.Done()

	// Only files AT ref, recent is checked in pruneTaskGetRetainedRecentRefs
	opts := lfs.NewScanRefsOptions()
	opts.ScanMode = lfs.ScanRefsMode
	opts.SkipDeletedBlobs = true
	refchan, err := lfs.ScanRefsToChan(ref, "", opts)
	if err != nil {
		errorChan <- err
		return
	}
	for wp := range refchan.Results {
		retainChan <- wp.Pointer.Oid
		tracerx.Printf("RETAIN: %v via ref %v", wp.Pointer.Oid, ref)
	}
	err = refchan.Wait()
	if err != nil {
		errorChan <- err
	}
}

// Background task, must call waitg.Done() once at end
func pruneTaskGetPreviousVersionsOfRef(ref string, since time.Time, retainChan chan string, errorChan chan error, waitg *sync.WaitGroup) {
	defer waitg.Done()

	refchan, err := lfs.ScanPreviousVersionsToChan(ref, since)
	if err != nil {
		errorChan <- err
		return
	}
	for wp := range refchan.Results {
		retainChan <- wp.Pointer.Oid
		tracerx.Printf("RETAIN: %v via ref %v >= %v", wp.Pointer.Oid, ref, since)
	}
	err = refchan.Wait()
	if err != nil {
		errorChan <- err
	}
}

// Background task, must call waitg.Done() once at end
func pruneTaskGetRetainedCurrentAndRecentRefs(fetchconf config.FetchPruneConfig, retainChan chan string, errorChan chan error, waitg *sync.WaitGroup) {
	defer waitg.Done()

	// We actually increment the waitg in this func since we kick off sub-goroutines
	// Make a list of what unique commits to keep, & search backward from
	commits := tools.NewStringSet()
	// Do current first
	ref, err := git.CurrentRef()
	if err != nil {
		errorChan <- err
		return
	}
	commits.Add(ref.Sha)
	waitg.Add(1)
	go pruneTaskGetRetainedAtRef(ref.Sha, retainChan, errorChan, waitg)

	// Now recent
	if fetchconf.FetchRecentRefsDays > 0 {
		pruneRefDays := fetchconf.FetchRecentRefsDays + fetchconf.PruneOffsetDays
		tracerx.Printf("PRUNE: Retaining non-HEAD refs within %d (%d+%d) days", pruneRefDays, fetchconf.FetchRecentRefsDays, fetchconf.PruneOffsetDays)
		refsSince := time.Now().AddDate(0, 0, -pruneRefDays)
		// Keep all recent refs including any recent remote branches
		refs, err := git.RecentBranches(refsSince, fetchconf.FetchRecentRefsIncludeRemotes, "")
		if err != nil {
			Panic(err, "Could not scan for recent refs")
		}
		for _, ref := range refs {
			if commits.Add(ref.Sha) {
				// A new commit
				waitg.Add(1)
				go pruneTaskGetRetainedAtRef(ref.Sha, retainChan, errorChan, waitg)
			}
		}
	}

	// For every unique commit we've fetched, check recent commits too
	// Only if we're fetching recent commits, otherwise only keep at refs
	if fetchconf.FetchRecentCommitsDays > 0 {
		pruneCommitDays := fetchconf.FetchRecentCommitsDays + fetchconf.PruneOffsetDays
		for commit := range commits.Iter() {
			// We measure from the last commit at the ref
			summ, err := git.GetCommitSummary(commit)
			if err != nil {
				errorChan <- fmt.Errorf("Couldn't scan commits at %v: %v", commit, err)
				continue
			}
			commitsSince := summ.CommitDate.AddDate(0, 0, -pruneCommitDays)
			waitg.Add(1)
			go pruneTaskGetPreviousVersionsOfRef(commit, commitsSince, retainChan, errorChan, waitg)
		}
	}
}

// Background task, must call waitg.Done() once at end
func pruneTaskGetRetainedUnpushed(fetchconf config.FetchPruneConfig, retainChan chan string, errorChan chan error, waitg *sync.WaitGroup) {
	defer waitg.Done()

	remoteName := fetchconf.PruneRemoteName

	refchan, err := lfs.ScanUnpushedToChan(remoteName)
	if err != nil {
		errorChan <- err
		return
	}
	for wp := range refchan.Results {
		retainChan <- wp.Pointer.Oid
		tracerx.Printf("RETAIN: %v unpushed", wp.Pointer.Oid)
	}
	err = refchan.Wait()
	if err != nil {
		errorChan <- err
	}
}

// Background task, must call waitg.Done() once at end
func pruneTaskGetRetainedWorktree(retainChan chan string, errorChan chan error, waitg *sync.WaitGroup) {
	defer waitg.Done()

	// Retain other worktree HEADs too
	// Working copy, branch & maybe commit is different but repo is shared
	allWorktreeRefs, err := git.GetAllWorkTreeHEADs(config.LocalGitStorageDir)
	if err != nil {
		errorChan <- err
		return
	}
	// Don't repeat any commits, worktrees are always on their own branches but
	// may point to the same commit
	commits := tools.NewStringSet()
	// current HEAD is done elsewhere
	headref, err := git.CurrentRef()
	if err != nil {
		errorChan <- err
		return
	}
	commits.Add(headref.Sha)
	for _, ref := range allWorktreeRefs {
		if commits.Add(ref.Sha) {
			// Worktree is on a different commit
			waitg.Add(1)
			// Don't need to 'cd' to worktree since we share same repo
			go pruneTaskGetRetainedAtRef(ref.Sha, retainChan, errorChan, waitg)
		}
	}

}

// Background task, must call waitg.Done() once at end
func pruneTaskGetReachableObjects(outObjectSet *tools.StringSet, errorChan chan error, waitg *sync.WaitGroup) {
	defer waitg.Done()

	// converts to `git rev-list --all`
	// We only pick up objects in real commits and not the reflog
	opts := lfs.NewScanRefsOptions()
	opts.ScanMode = lfs.ScanAllMode
	opts.SkipDeletedBlobs = false

	pointerchan, err := lfs.ScanRefsToChan("", "", opts)
	if err != nil {
		errorChan <- fmt.Errorf("Error scanning for reachable objects: %v", err)
		return
	}

	for p := range pointerchan.Results {
		outObjectSet.Add(p.Oid)
	}
	err = pointerchan.Wait()
	if err != nil {
		errorChan <- err
	}

}

func init() {
	RegisterCommand("prune", pruneCommand, func(cmd *cobra.Command) {
		cmd.Flags().BoolVarP(&pruneDryRunArg, "dry-run", "d", false, "Don't delete anything, just report")
		cmd.Flags().BoolVarP(&pruneVerboseArg, "verbose", "v", false, "Print full details of what is/would be deleted")
		cmd.Flags().BoolVarP(&pruneVerifyArg, "verify-remote", "c", false, "Verify that remote has LFS files before deleting")
		cmd.Flags().BoolVar(&pruneDoNotVerifyArg, "no-verify-remote", false, "Override lfs.pruneverifyremotealways and don't verify")
	})
}
