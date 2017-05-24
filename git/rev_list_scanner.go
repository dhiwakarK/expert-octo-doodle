package git

import (
	"bufio"
	"encoding/hex"
	"io"
	"io/ioutil"
	"os/exec"
	"regexp"
	"strings"
	"sync"

	"github.com/git-lfs/git-lfs/errors"
	"github.com/rubyist/tracerx"
)

// ScanningMode is a constant type that allows for variation in the range of
// commits to scan when given to the `*git.RevListScanner` type.
type ScanningMode int

const (
	// ScanRefsMode will scan between two refspecs.
	ScanRefsMode ScanningMode = iota
	// ScanAllMode will scan all history.
	ScanAllMode
	// ScanLeftToRemoteMode will scan the difference between "left" and a
	// remote tracking ref.
	ScanLeftToRemoteMode
)

// RevListOrder is a constant type that allows for variation in the ordering of
// revisions given by the *RevListScanner below.
type RevListOrder int

const (
	// DefaultRevListOrder is the zero-value for this type and yields the
	// results as given by git-rev-list(1) without any `--<t>-order`
	// argument given. By default: reverse chronological order.
	DefaultRevListOrder RevListOrder = iota
	// DateRevListOrder gives the revisions such that no parents are shown
	// before children, and otherwise in commit timestamp order.
	DateRevListOrder
	// AuthorDateRevListOrder gives the revisions such that no parents are
	// shown before children, and otherwise in author date timestamp order.
	AuthorDateRevListOrder
	// TopoRevListOrder gives the revisions such that they appear in
	// topological order.
	TopoRevListOrder
)

// ScanRefsOptions is an "options" type that is used to configure a scan
// operation on the `*git.RevListScanner` instance when given to the function
// `NewRevListScanner()`.
type ScanRefsOptions struct {
	// Mode is the scan mode to apply, see above.
	Mode ScanningMode
	// Remote is the current remote to scan against, if using
	// ScanLeftToRemoveMode.
	Remote string
	// SkipDeletedBlobs specifies whether or not to traverse into commit
	// ancestry (revealing potentially deleted (unreferenced) blobs, trees,
	// or commits.
	SkipDeletedBlobs bool
	// Order specifies the order in which revisions are yielded from the
	// output of `git-rev-list(1)`. For more information, see the above
	// documentation on the RevListOrder type.
	Order RevListOrder

	// SkippedRefs provides a list of refs to ignore.
	SkippedRefs []string
	// Mutex guards names.
	Mutex *sync.Mutex
	// Names maps Git object IDs (encoded as hex using
	// hex.EncodeString()) to their names, i.e., a directory name
	// (fully-qualified) for trees, or a pathspec for blob tree entries.
	Names map[string]string
}

// GetName returns the name associated with a given blob/tree sha and "true" if
// it exists, or ("", false) if it doesn't.
//
// GetName is guarded by a use of o.Mutex, and is goroutine safe.
func (o *ScanRefsOptions) GetName(sha string) (string, bool) {
	o.Mutex.Lock()
	defer o.Mutex.Unlock()

	name, ok := o.Names[sha]
	return name, ok
}

// SetName sets the name associated with a given blob/tree sha.
//
// SetName is guarded by a use of o.Mutex, and is therefore goroutine safe.
func (o *ScanRefsOptions) SetName(sha, name string) {
	o.Mutex.Lock()
	defer o.Mutex.Unlock()

	o.Names[sha] = name
}

// RevListScanner is a Scanner type that parses through results of the `git
// rev-list` command.
type RevListScanner struct {
	// s is a buffered scanner feeding from the output (stdout) of
	// git-rev-list(1) invocation.
	s *bufio.Scanner
	// closeFn is an optional type returning an error yielded by closing any
	// resources held by an open (running) instance of the *RevListScanner
	// type.
	closeFn func() error

	// name is the name of the most recently read object.
	name string
	// oid is the oid of the most recently read object.
	oid []byte
	// err is the most recently encountered error.
	err error
}

var (
	// ambiguousRegex is a regular expression matching the output of stderr
	// when ambiguous refnames are encountered.
	ambiguousRegex = regexp.MustCompile(`warning: refname (.*) is ambiguous`)

	// z40 is a regular expression matching the empty blob/commit/tree
	// SHA: "0000000000000000000000000000000000000000".
	z40 = regexp.MustCompile(`\^?0{40}`)
)

// NewRevListScanner instantiates a new RevListScanner instance scanning between
// the "left" and "right" commitish (commit, refspec) and scanning using the
// *ScanRefsOptions "opt" configuration.
//
// It returns a new *RevListScanner instance, or an error if one was
// encountered. Upon returning, the `git-rev-list(1)` instance is already
// running, and Scan() may be called immediately.
func NewRevListScanner(left, right string, opt *ScanRefsOptions) (*RevListScanner, error) {
	stdin, args, err := revListArgs(left, right, opt)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command("git", args...)

	cmd.Stdin = stdin
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	tracerx.Printf("run_command: git %s", strings.Join(args, " "))
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	return &RevListScanner{
		s: bufio.NewScanner(stdout),
		closeFn: func() error {
			msg, _ := ioutil.ReadAll(stderr)

			// First check if there was a non-zero exit code given
			// when Wait()-ing on the command execution.
			if err := cmd.Wait(); err != nil {
				return errors.Errorf("Error in git %s: %v %s",
					strings.Join(args, " "), err, msg)
			}

			// If the command exited cleanly, but found an ambiguous
			// refname, promote that to an error and return it.
			//
			// `git-rev-list(1)` does not treat ambiguous refnames
			// as fatal (non-zero exit status), but we do.
			if am := ambiguousRegex.FindSubmatch(msg); len(am) > 1 {
				return errors.Errorf("ref %s is ambiguous", am[1])
			}
			return nil
		},
	}, nil
}

// revListArgs returns the arguments for a given left, right, and
// ScanRefsOptions instance.
//
// In order, it returns the contents of stdin as an io.Reader, the args passed
// to git as a []string, and any error encountered in generating those if one
// occurred.
func revListArgs(l, r string, opt *ScanRefsOptions) (io.Reader, []string, error) {
	var stdin io.Reader
	args := []string{"rev-list", "--objects"}

	switch opt.Order {
	case DateRevListOrder:
		args = append(args, "--date-order")
	case AuthorDateRevListOrder:
		args = append(args, "--author-date-order")
	case TopoRevListOrder:
		args = append(args, "--topo-order")
	}

	switch opt.Mode {
	case ScanRefsMode:
		if opt.SkipDeletedBlobs {
			args = append(args, "--no-walk")
		} else {
			args = append(args, "--do-walk")
		}

		args = append(args, l)
		if len(r) > 0 && !z40.MatchString(r) {
			args = append(args, r)
		}
	case ScanAllMode:
		args = append(args, "--all")
	case ScanLeftToRemoteMode:
		if len(opt.SkippedRefs) == 0 {
			args = append(args, l, "--not", "--remotes="+opt.Remote)
		} else {
			args = append(args, "--stdin")
			stdin = strings.NewReader(strings.Join(
				append([]string{l}, opt.SkippedRefs...), "\n"),
			)
		}
	default:
		return nil, nil, errors.Errorf("unknown scan type: %d", opt.Mode)
	}
	return stdin, append(args, "--"), nil
}

// Name is an optional field that gives the name of the object (if the object is
// a tree, blob).
//
// It can be called before or after Scan(), but will return "" if called
// before.
func (s *RevListScanner) Name() string { return s.name }

// OID is the hex-decoded bytes of the object's ID.
//
// It can be called before or after Scan(), but will return "" if called
// before.
func (s *RevListScanner) OID() []byte { return s.oid }

// Err returns the last encountered error (or nil) after a call to Scan().
//
// It SHOULD be called, checked and handled after a call to Scan().
func (s *RevListScanner) Err() error { return s.err }

// Scan scans the next entry given by git-rev-list(1), and returns true/false
// indicating if there are more results to scan.
func (s *RevListScanner) Scan() bool {
	var err error
	s.oid, s.name, err = s.scan()

	if err != nil {
		if err != io.EOF {
			s.err = err
		}
		return false
	}
	return len(s.oid) > 0
}

// Close closes the RevListScanner by freeing any resources held by the
// instance while running, and returns any error encountered while doing so.
func (s *RevListScanner) Close() error {
	if s.closeFn == nil {
		return nil
	}
	return s.closeFn()
}

// scan provides the internal implementation of scanning a line of text from the
// output of `git-rev-list(1)`.
func (s *RevListScanner) scan() ([]byte, string, error) {
	if !s.s.Scan() {
		return nil, "", s.s.Err()
	}

	line := strings.TrimSpace(s.s.Text())
	if len(line) < 40 {
		return nil, "", nil
	}

	sha1, err := hex.DecodeString(line[:40])
	if err != nil {
		return nil, "", err
	}

	var name string
	if len(line) > 40 {
		name = line[41:]
	}

	return sha1, name, nil
}
