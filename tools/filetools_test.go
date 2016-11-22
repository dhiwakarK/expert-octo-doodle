package tools

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/git-lfs/git-lfs/subprocess"

	"github.com/stretchr/testify/assert"
)

func TestCleanPathsCleansPaths(t *testing.T) {
	cleaned := CleanPaths("/foo/bar/,/foo/bar/baz", ",")

	assert.Equal(t, []string{"/foo/bar", "/foo/bar/baz"}, cleaned)
}

func TestCleanPathsReturnsNoResultsWhenGivenNoPaths(t *testing.T) {
	cleaned := CleanPaths("", ",")

	assert.Empty(t, cleaned)
}

func TestFastWalkBasic(t *testing.T) {
	rootDir, err := ioutil.TempDir(os.TempDir(), "GitLfsTestFastWalkBasic")
	if err != nil {
		assert.FailNow(t, "Unable to get temp dir: %v", err)
	}
	defer os.RemoveAll(rootDir)
	os.Chdir(rootDir)

	expectedEntries := createFastWalkInputData(10, 160)

	fchan, errchan := fastWalkWithExcludeFiles(expectedEntries[0], "", nil)
	gotEntries, gotErrors := collectFastWalkResults(fchan, errchan)

	assert.Empty(t, gotErrors)

	sort.Strings(expectedEntries)
	sort.Strings(gotEntries)
	assert.Equal(t, expectedEntries, gotEntries)

}

func BenchmarkFastWalkGitRepoChannels(b *testing.B) {
	rootDir, err := ioutil.TempDir(os.TempDir(), "GitLfsBenchFastWalkGitRepoChannels")
	if err != nil {
		assert.FailNow(b, "Unable to get temp dir: %v", err)
	}
	defer os.RemoveAll(rootDir)
	os.Chdir(rootDir)
	entries := createFastWalkInputData(1000, 5000)

	for i := 0; i < b.N; i++ {
		var files, errors int
		fchan, errchan := FastWalkGitRepoChannels(entries[0])
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			for _ = range fchan {
				files++
			}
			wg.Done()
		}()

		go func() {
			for _ = range errchan {
				errors++
			}
			wg.Done()
		}()
		wg.Wait()

		b.Logf("files: %d, errors: %d", files, errors)
	}
}

func BenchmarkFastWalkGitRepoCallback(b *testing.B) {
	rootDir, err := ioutil.TempDir(os.TempDir(), "GitLfsBenchFastWalkGitRepoCallback")
	if err != nil {
		assert.FailNow(b, "Unable to get temp dir: %v", err)
	}
	defer os.RemoveAll(rootDir)
	os.Chdir(rootDir)
	entries := createFastWalkInputData(1000, 5000)

	for i := 0; i < b.N; i++ {
		var files, errors int
		FastWalkGitRepo(entries[0], func(parentDir string, info os.FileInfo, err error) {
			if err != nil {
				errors++
			} else {
				files++
			}
		})

		b.Logf("files: %d, errors: %d", files, errors)
	}
}

func TestFastWalkGitRepo(t *testing.T) {
	rootDir, err := ioutil.TempDir(os.TempDir(), "GitLfsTestFastWalkGitRepo")
	if err != nil {
		assert.FailNow(t, "Unable to get temp dir: %v", err)
	}
	defer os.RemoveAll(rootDir)
	os.Chdir(rootDir)

	expectedEntries := createFastWalkInputData(3, 3)

	mainDir := expectedEntries[0]

	// Set up a git repo and add some ignored files / dirs
	subprocess.SimpleExec("git", "init", mainDir)
	ignored := []string{
		"filethatweignore.ign",
		"foldercontainingignored",
		"foldercontainingignored/notthisone.ign",
		"ignoredfolder",
		"ignoredfolder/file1.txt",
		"ignoredfolder/file2.txt",
		"ignoredfrominside",
		"ignoredfrominside/thisisok.txt",
		"ignoredfrominside/thisisnot.txt",
		"ignoredfrominside/thisone",
		"ignoredfrominside/thisone/file1.txt",
	}
	for _, f := range ignored {
		fullPath := filepath.Join(mainDir, f)
		if len(filepath.Ext(f)) > 0 {
			ioutil.WriteFile(fullPath, []byte("TEST"), 0644)
		} else {
			os.MkdirAll(fullPath, 0755)
		}
	}
	// write root .gitignore
	rootGitIgnore := `
# ignore *.ign everywhere
*.ign
# ignore folder
ignoredfolder
`
	ioutil.WriteFile(filepath.Join(mainDir, ".gitignore"), []byte(rootGitIgnore), 0644)
	// Subfolder ignore; folder will show up but but subfolder 'thisone' won't
	subFolderIgnore := `
thisone
thisisnot.txt
`
	ioutil.WriteFile(filepath.Join(mainDir, "ignoredfrominside", ".gitignore"), []byte(subFolderIgnore), 0644)

	// This dir will be walked but content won't be
	expectedEntries = append(expectedEntries, filepath.Join(mainDir, "foldercontainingignored"))
	// This dir will be walked and some of its content but has its own gitignore
	expectedEntries = append(expectedEntries, filepath.Join(mainDir, "ignoredfrominside"))
	expectedEntries = append(expectedEntries, filepath.Join(mainDir, "ignoredfrominside", "thisisok.txt"))
	// Also gitignores
	expectedEntries = append(expectedEntries, filepath.Join(mainDir, ".gitignore"))
	expectedEntries = append(expectedEntries, filepath.Join(mainDir, "ignoredfrominside", ".gitignore"))
	// nothing else should be there

	fchan, errchan := FastWalkGitRepoChannels(mainDir)
	gotEntries, gotErrors := collectFastWalkResults(fchan, errchan)

	assert.Empty(t, gotErrors)

	sort.Strings(expectedEntries)
	sort.Strings(gotEntries)
	assert.Equal(t, expectedEntries, gotEntries)

}

// Make test data - ensure you've Chdir'ed into a temp dir first
// Returns list of files/dirs that are created
// First entry is the parent dir of all others
func createFastWalkInputData(smallFolder, largeFolder int) []string {
	dirs := []string{
		"testroot",
		"testroot/folder1",
		"testroot/folder2",
		"testroot/folder2/subfolder1",
		"testroot/folder2/subfolder2",
		"testroot/folder2/subfolder3",
		"testroot/folder2/subfolder4",
		"testroot/folder2/subfolder4/subsub",
	}
	expectedEntries := make([]string, 0, 250)

	for i, dir := range dirs {
		os.MkdirAll(dir, 0755)
		numFiles := smallFolder
		expectedEntries = append(expectedEntries, filepath.Clean(dir))
		if i >= 3 && i <= 5 {
			// Bulk test to ensure works with > 1 batch
			numFiles = largeFolder
		}
		for f := 0; f < numFiles; f++ {
			filename := filepath.Join(dir, fmt.Sprintf("file%d.txt", f))
			ioutil.WriteFile(filename, []byte("TEST"), 0644)
			expectedEntries = append(expectedEntries, filepath.Clean(filename))
		}
	}

	return expectedEntries
}

func collectFastWalkResults(fchan <-chan FastWalkInfo, errchan <-chan error) ([]string, []error) {
	gotEntries := make([]string, 0, 1000)
	gotErrors := make([]error, 0, 5)
	var waitg sync.WaitGroup
	waitg.Add(2)
	go func() {
		for o := range fchan {
			gotEntries = append(gotEntries, filepath.Join(o.ParentDir, o.Info.Name()))
		}
		waitg.Done()
	}()
	go func() {
		for err := range errchan {
			gotErrors = append(gotErrors, err)
		}
		waitg.Done()
	}()
	waitg.Wait()

	return gotEntries, gotErrors
}
