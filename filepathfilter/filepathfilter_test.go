package filepathfilter

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/git-lfs/git-lfs/tools"
	"github.com/stretchr/testify/assert"
)

func TestPatternMatch(t *testing.T) {
	assert.True(t, patternMatch("filename.txt", "filename.txt"))
	assert.True(t, patternMatch("*.txt", "filename.txt"))
	assert.False(t, patternMatch("*.tx", "filename.txt"))
	assert.True(t, patternMatch("f*.txt", "filename.txt"))
	assert.False(t, patternMatch("g*.txt", "filename.txt"))
	assert.True(t, patternMatch("file*", "filename.txt"))
	assert.False(t, patternMatch("file", "filename.txt"))

	// With no path separators, should match in subfolders
	assert.True(t, patternMatch("*.txt", "sub/filename.txt"))
	assert.False(t, patternMatch("*.tx", "sub/filename.txt"))
	assert.True(t, patternMatch("f*.txt", "sub/filename.txt"))
	assert.False(t, patternMatch("g*.txt", "sub/filename.txt"))
	assert.True(t, patternMatch("file*", "sub/filename.txt"))
	assert.False(t, patternMatch("file", "sub/filename.txt"))
	// Needs wildcard for exact filename
	assert.True(t, patternMatch("**/filename.txt", "sub/sub/sub/filename.txt"))

	// Should not match dots to subparts
	assert.False(t, patternMatch("*.ign", "sub/shouldignoreme.txt"))

	// Path specific
	assert.True(t, patternMatch("sub", "sub/filename.txt"))
	assert.False(t, patternMatch("sub", "subfilename.txt"))

	// Absolute
	assert.True(t, patternMatch("*.dat", "/path/to/sub/.git/test.dat"))
	assert.True(t, patternMatch("**/.git", "/path/to/sub/.git"))

	// Match anything
	assert.True(t, patternMatch(".", "path.txt"))
	assert.True(t, patternMatch("./", "path.txt"))
	assert.True(t, patternMatch(".\\", "path.txt"))
}

func patternMatch(pattern, filename string) bool {
	return newPattern(pattern).Match(filename)
}

type filterTest struct {
	expectedResult bool
	includes       []string
	excludes       []string
}

func TestFilterAllows(t *testing.T) {
	cases := []filterTest{
		// Null case
		filterTest{true, nil, nil},
		// Inclusion
		filterTest{true, []string{"*.dat"}, nil},
		filterTest{true, []string{"file*.dat"}, nil},
		filterTest{true, []string{"file*"}, nil},
		filterTest{true, []string{"*name.dat"}, nil},
		filterTest{false, []string{"/*.dat"}, nil},
		filterTest{false, []string{"otherfolder/*.dat"}, nil},
		filterTest{false, []string{"*.nam"}, nil},
		filterTest{true, []string{"test/filename.dat"}, nil},
		filterTest{true, []string{"test/filename.dat"}, nil},
		filterTest{false, []string{"blank", "something", "foo"}, nil},
		filterTest{false, []string{"test/notfilename.dat"}, nil},
		filterTest{true, []string{"test"}, nil},
		filterTest{true, []string{"test/*"}, nil},
		filterTest{false, []string{"nottest"}, nil},
		filterTest{false, []string{"nottest/*"}, nil},
		filterTest{true, []string{"test/fil*"}, nil},
		filterTest{false, []string{"test/g*"}, nil},
		filterTest{true, []string{"tes*/*"}, nil},
		// Exclusion
		filterTest{false, nil, []string{"*.dat"}},
		filterTest{false, nil, []string{"file*.dat"}},
		filterTest{false, nil, []string{"file*"}},
		filterTest{false, nil, []string{"*name.dat"}},
		filterTest{true, nil, []string{"/*.dat"}},
		filterTest{true, nil, []string{"otherfolder/*.dat"}},
		filterTest{false, nil, []string{"test/filename.dat"}},
		filterTest{false, nil, []string{"blank", "something", "test/filename.dat", "foo"}},
		filterTest{true, nil, []string{"blank", "something", "foo"}},
		filterTest{true, nil, []string{"test/notfilename.dat"}},
		filterTest{false, nil, []string{"test"}},
		filterTest{false, nil, []string{"test/*"}},
		filterTest{true, nil, []string{"nottest"}},
		filterTest{true, nil, []string{"nottest/*"}},
		filterTest{false, nil, []string{"test/fil*"}},
		filterTest{true, nil, []string{"test/g*"}},
		filterTest{false, nil, []string{"tes*/*"}},

		// Both
		filterTest{true, []string{"test/filename.dat"}, []string{"test/notfilename.dat"}},
		filterTest{false, []string{"test"}, []string{"test/filename.dat"}},
		filterTest{true, []string{"test/*"}, []string{"test/notfile*"}},
		filterTest{false, []string{"test/*"}, []string{"test/file*"}},
		filterTest{false, []string{"another/*", "test/*"}, []string{"test/notfilename.dat", "test/filename.dat"}},
	}

	for _, c := range cases {
		result := New(c.includes, c.excludes).Allows("test/filename.dat")
		assert.Equal(t, c.expectedResult, result, "includes: %v excludes: %v", c.includes, c.excludes)
		if runtime.GOOS == "windows" {
			// also test with \ path separators, tolerate mixed separators
			for i, inc := range c.includes {
				c.includes[i] = strings.Replace(inc, "/", "\\", -1)
			}
			for i, ex := range c.excludes {
				c.excludes[i] = strings.Replace(ex, "/", "\\", -1)
			}
			assert.Equal(t, c.expectedResult, New(c.includes, c.excludes).Allows("test/filename.dat"), c)
		}
	}
}

func BenchmarkToolsIncludeWildcardOnly(b *testing.B) {
	files := benchmarkTree(b)
	inc := []string{"*.go"}
	for i := 0; i < b.N; i++ {
		for _, f := range files {
			tools.FilenamePassesIncludeExcludeFilter(f, inc, nil)
		}
	}
}

func BenchmarkFilterIncludeWildcardOnly(b *testing.B) {
	files := benchmarkTree(b)
	filter := New([]string{"*.go"}, nil)
	for i := 0; i < b.N; i++ {
		for _, f := range files {
			filter.Allows(f)
		}
	}
}

func BenchmarkToolsIncludeDoubleAsterisk(b *testing.B) {
	files := benchmarkTree(b)
	inc := []string{"**/README.md"}
	for i := 0; i < b.N; i++ {
		for _, f := range files {
			tools.FilenamePassesIncludeExcludeFilter(f, inc, nil)
		}
	}
}

func BenchmarkFilterIncludeDoubleAsterisk(b *testing.B) {
	files := benchmarkTree(b)
	filter := New([]string{"**/README.md"}, nil)
	for i := 0; i < b.N; i++ {
		for _, f := range files {
			filter.Allows(f)
		}
	}
}

var (
	benchmarkFiles []string
	benchmarkMu    sync.Mutex
)

func benchmarkTree(b *testing.B) []string {
	benchmarkMu.Lock()
	defer benchmarkMu.Unlock()

	if benchmarkFiles != nil {
		return benchmarkFiles
	}

	wd, err := os.Getwd()
	if err != nil {
		b.Fatal(err)
	}

	infoCh, errCh := tools.FastWalkGitRepo(filepath.Dir(wd))

	go func() {
		for i := range infoCh {
			benchmarkFiles = append(benchmarkFiles, filepath.Join(i.ParentDir, i.Info.Name()))
		}
	}()

	hasErrors := false
	for err := range errCh {
		hasErrors = true
		b.Error(err)
	}

	if hasErrors {
		b.Fatal("has errors :(")
	}

	return benchmarkFiles
}
