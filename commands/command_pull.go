package commands

import (
	"fmt"
	"sync"
	"time"

	"github.com/git-lfs/git-lfs/filepathfilter"
	"github.com/git-lfs/git-lfs/git"
	"github.com/git-lfs/git-lfs/lfs"
	"github.com/git-lfs/git-lfs/progress"
	"github.com/rubyist/tracerx"
	"github.com/spf13/cobra"
)

func pullCommand(cmd *cobra.Command, args []string) {
	requireGitVersion()
	requireInRepo()

	if len(args) > 0 {
		// Remote is first arg
		if err := git.ValidateRemote(args[0]); err != nil {
			Panic(err, fmt.Sprintf("Invalid remote name '%v'", args[0]))
		}
		cfg.CurrentRemote = args[0]
	} else {
		// Actively find the default remote, don't just assume origin
		defaultRemote, err := git.DefaultRemote()
		if err != nil {
			Panic(err, "No default remote")
		}
		cfg.CurrentRemote = defaultRemote
	}

	includeArg, excludeArg := getIncludeExcludeArgs(cmd)
	filter := buildFilepathFilter(cfg, includeArg, excludeArg)
	pull(filter)
}

func pull(filter *filepathfilter.Filter) {
	ref, err := git.CurrentRef()
	if err != nil {
		Panic(err, "Could not pull")
	}

	pointers := newPointerMap()
	meter := progress.NewMeter(progress.WithOSEnv(cfg.Os))
	singleCheckout := newSingleCheckout()
	q := lfs.NewDownloadQueue(lfs.WithProgress(meter))
	gitscanner := lfs.NewGitScanner(func(p *lfs.WrappedPointer, err error) {
		if err != nil {
			LoggedError(err, "Scanner error")
			return
		}

		if pointers.Seen(p) {
			return
		}

		// no need to download objects that exist locally already
		lfs.LinkOrCopyFromReference(p.Oid, p.Size)
		if lfs.ObjectExistsOfSize(p.Oid, p.Size) {
			singleCheckout.Run(p)
			return
		}

		meter.Add(p.Size)
		meter.StartTransfer(p.Name)
		tracerx.Printf("fetch %v [%v]", p.Name, p.Oid)
		pointers.Add(p)
		q.Add(lfs.NewDownloadable(p))
	})

	gitscanner.Filter = filter

	dlwatch := q.Watch()
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		for oid := range dlwatch {
			for _, p := range pointers.All(oid) {
				singleCheckout.Run(p)
			}
		}
		wg.Done()
	}()

	processQueue := time.Now()
	if err := gitscanner.ScanTree(ref.Sha); err != nil {
		ExitWithError(err)
	}

	meter.Start()
	gitscanner.Close()
	q.Wait()
	wg.Wait()
	tracerx.PerformanceSince("process queue", processQueue)

	singleCheckout.Close()

	for _, err := range q.Errors() {
		FullError(err)
	}
}

// tracks LFS objects being downloaded, according to their unique OIDs.
type pointerMap struct {
	pointers map[string][]*lfs.WrappedPointer
	mu       sync.Mutex
}

func newPointerMap() *pointerMap {
	return &pointerMap{pointers: make(map[string][]*lfs.WrappedPointer)}
}

func (m *pointerMap) Seen(p *lfs.WrappedPointer) bool {
	m.mu.Lock()
	existing, ok := m.pointers[p.Oid]
	if ok {
		m.pointers[p.Oid] = append(existing, p)
	}
	m.mu.Unlock()
	return ok
}

func (m *pointerMap) Add(p *lfs.WrappedPointer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pointers[p.Oid] = append(m.pointers[p.Oid], p)
}

func (m *pointerMap) All(oid string) []*lfs.WrappedPointer {
	m.mu.Lock()
	pointers := m.pointers[oid]
	delete(m.pointers, oid)
	m.mu.Unlock()

	return pointers
}

func init() {
	RegisterCommand("pull", pullCommand, func(cmd *cobra.Command) {
		cmd.Flags().StringVarP(&includeArg, "include", "I", "", "Include a list of paths")
		cmd.Flags().StringVarP(&excludeArg, "exclude", "X", "", "Exclude a list of paths")
	})
}
