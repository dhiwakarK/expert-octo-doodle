package git_test // to avoid import cycles

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	. "github.com/git-lfs/git-lfs/git"
	"github.com/git-lfs/git-lfs/test"
	"github.com/git-lfs/git-lfs/tools/longpathos"
	"github.com/stretchr/testify/assert"
)

func TestCurrentRefAndCurrentRemoteRef(t *testing.T) {
	repo := test.NewRepo(t)
	repo.Pushd()
	defer func() {
		repo.Popd()
		repo.Cleanup()
	}()

	// test commits; we'll just modify the same file each time since we're
	// only interested in branches
	inputs := []*test.CommitInput{
		{ // 0
			Files: []*test.FileInput{
				{Filename: "file1.txt", Size: 20},
			},
		},
		{ // 1
			NewBranch: "branch2",
			Files: []*test.FileInput{
				{Filename: "file1.txt", Size: 25},
			},
		},
		{ // 2
			ParentBranches: []string{"master"}, // back on master
			Files: []*test.FileInput{
				{Filename: "file1.txt", Size: 30},
			},
		},
		{ // 3
			NewBranch: "branch3",
			Files: []*test.FileInput{
				{Filename: "file1.txt", Size: 32},
			},
		},
	}
	outputs := repo.AddCommits(inputs)
	// last commit was on branch3
	ref, err := CurrentRef()
	assert.Nil(t, err)
	assert.Equal(t, &Ref{"branch3", RefTypeLocalBranch, outputs[3].Sha}, ref)
	test.RunGitCommand(t, true, "checkout", "master")
	ref, err = CurrentRef()
	assert.Nil(t, err)
	assert.Equal(t, &Ref{"master", RefTypeLocalBranch, outputs[2].Sha}, ref)
	// Check remote
	repo.AddRemote("origin")
	test.RunGitCommand(t, true, "push", "-u", "origin", "master:someremotebranch")
	ref, err = CurrentRemoteRef()
	assert.Nil(t, err)
	assert.Equal(t, &Ref{"origin/someremotebranch", RefTypeRemoteBranch, outputs[2].Sha}, ref)

	refname, err := RemoteRefNameForCurrentBranch()
	assert.Nil(t, err)
	assert.Equal(t, "refs/remotes/origin/someremotebranch", refname)

	remote, err := RemoteForCurrentBranch()
	assert.Nil(t, err)
	assert.Equal(t, "origin", remote)

	ref, err = ResolveRef(outputs[2].Sha)
	assert.Nil(t, err)
	assert.Equal(t, &Ref{outputs[2].Sha, RefTypeOther, outputs[2].Sha}, ref)
}

func TestRecentBranches(t *testing.T) {
	repo := test.NewRepo(t)
	repo.Pushd()
	defer func() {
		repo.Popd()
		repo.Cleanup()
	}()

	now := time.Now()
	// test commits; we'll just modify the same file each time since we're
	// only interested in branches & dates
	inputs := []*test.CommitInput{
		{ // 0
			CommitDate: now.AddDate(0, 0, -20),
			Files: []*test.FileInput{
				{Filename: "file1.txt", Size: 20},
			},
		},
		{ // 1
			CommitDate: now.AddDate(0, 0, -15),
			NewBranch:  "excluded_branch", // new branch & tag but too old
			Tags:       []string{"excluded_tag"},
			Files: []*test.FileInput{
				{Filename: "file1.txt", Size: 25},
			},
		},
		{ // 2
			CommitDate:     now.AddDate(0, 0, -12),
			ParentBranches: []string{"master"}, // back on master
			Files: []*test.FileInput{
				{Filename: "file1.txt", Size: 30},
			},
		},
		{ // 3
			CommitDate: now.AddDate(0, 0, -6),
			NewBranch:  "included_branch", // new branch within 7 day limit
			Files: []*test.FileInput{
				{Filename: "file1.txt", Size: 32},
			},
		},
		{ // 4
			CommitDate: now.AddDate(0, 0, -3),
			NewBranch:  "included_branch_2", // new branch within 7 day limit
			Files: []*test.FileInput{
				{Filename: "file1.txt", Size: 36},
			},
		},
		{ // 5
			// Final commit, current date/time
			ParentBranches: []string{"master"}, // back on master
			Files: []*test.FileInput{
				{Filename: "file1.txt", Size: 21},
			},
		},
	}
	outputs := repo.AddCommits(inputs)

	// Add a couple of remotes and push some branches
	repo.AddRemote("origin")
	repo.AddRemote("upstream")

	test.RunGitCommand(t, true, "push", "origin", "master")
	test.RunGitCommand(t, true, "push", "origin", "excluded_branch")
	test.RunGitCommand(t, true, "push", "origin", "included_branch")
	test.RunGitCommand(t, true, "push", "upstream", "master")
	test.RunGitCommand(t, true, "push", "upstream", "included_branch_2")

	// Recent, local only
	refs, err := RecentBranches(now.AddDate(0, 0, -7), false, "")
	assert.Equal(t, nil, err)
	expectedRefs := []*Ref{
		&Ref{"master", RefTypeLocalBranch, outputs[5].Sha},
		&Ref{"included_branch_2", RefTypeLocalBranch, outputs[4].Sha},
		&Ref{"included_branch", RefTypeLocalBranch, outputs[3].Sha},
	}
	assert.Equal(t, expectedRefs, refs, "Refs should be correct")

	// Recent, remotes too (all of them)
	refs, err = RecentBranches(now.AddDate(0, 0, -7), true, "")
	assert.Equal(t, nil, err)
	expectedRefs = []*Ref{
		&Ref{"master", RefTypeLocalBranch, outputs[5].Sha},
		&Ref{"included_branch_2", RefTypeLocalBranch, outputs[4].Sha},
		&Ref{"included_branch", RefTypeLocalBranch, outputs[3].Sha},
		&Ref{"upstream/master", RefTypeRemoteBranch, outputs[5].Sha},
		&Ref{"upstream/included_branch_2", RefTypeRemoteBranch, outputs[4].Sha},
		&Ref{"origin/master", RefTypeRemoteBranch, outputs[5].Sha},
		&Ref{"origin/included_branch", RefTypeRemoteBranch, outputs[3].Sha},
	}
	// Need to sort for consistent comparison
	sort.Sort(test.RefsByName(expectedRefs))
	sort.Sort(test.RefsByName(refs))
	assert.Equal(t, expectedRefs, refs, "Refs should be correct")

	// Recent, only single remote
	refs, err = RecentBranches(now.AddDate(0, 0, -7), true, "origin")
	assert.Equal(t, nil, err)
	expectedRefs = []*Ref{
		&Ref{"master", RefTypeLocalBranch, outputs[5].Sha},
		&Ref{"origin/master", RefTypeRemoteBranch, outputs[5].Sha},
		&Ref{"included_branch_2", RefTypeLocalBranch, outputs[4].Sha},
		&Ref{"included_branch", RefTypeLocalBranch, outputs[3].Sha},
		&Ref{"origin/included_branch", RefTypeRemoteBranch, outputs[3].Sha},
	}
	// Need to sort for consistent comparison
	sort.Sort(test.RefsByName(expectedRefs))
	sort.Sort(test.RefsByName(refs))
	assert.Equal(t, expectedRefs, refs, "Refs should be correct")
}

func TestResolveEmptyCurrentRef(t *testing.T) {
	repo := test.NewRepo(t)
	repo.Pushd()
	defer func() {
		repo.Popd()
		repo.Cleanup()
	}()

	_, err := CurrentRef()
	assert.NotEqual(t, nil, err)
}

func TestWorkTrees(t *testing.T) {

	// Only git 2.5+
	if !Config.IsGitVersionAtLeast("2.5.0") {
		return
	}

	repo := test.NewRepo(t)
	repo.Pushd()
	defer func() {
		repo.Popd()
		repo.Cleanup()
	}()

	// test commits; we'll just modify the same file each time since we're
	// only interested in branches & dates
	inputs := []*test.CommitInput{
		{ // 0
			Files: []*test.FileInput{
				{Filename: "file1.txt", Size: 20},
			},
		},
		{ // 1
			NewBranch: "branch2",
			Files: []*test.FileInput{
				{Filename: "file1.txt", Size: 25},
			},
		},
		{ // 2
			NewBranch:      "branch3",
			ParentBranches: []string{"master"}, // back on master
			Files: []*test.FileInput{
				{Filename: "file1.txt", Size: 30},
			},
		},
		{ // 3
			NewBranch:      "branch4",
			ParentBranches: []string{"master"}, // back on master
			Files: []*test.FileInput{
				{Filename: "file1.txt", Size: 40},
			},
		},
	}
	outputs := repo.AddCommits(inputs)
	// Checkout master again otherwise can't create a worktree from branch4 if we're on it here
	test.RunGitCommand(t, true, "checkout", "master")

	// We can create worktrees as subfolders for convenience
	// Each one is checked out to a different branch
	// Note that we *won't* create one for branch3
	test.RunGitCommand(t, true, "worktree", "add", "branch2_wt", "branch2")
	test.RunGitCommand(t, true, "worktree", "add", "branch4_wt", "branch4")

	refs, err := GetAllWorkTreeHEADs(filepath.Join(repo.Path, ".git"))
	assert.Equal(t, nil, err)
	expectedRefs := []*Ref{
		&Ref{"master", RefTypeLocalBranch, outputs[0].Sha},
		&Ref{"branch2", RefTypeLocalBranch, outputs[1].Sha},
		&Ref{"branch4", RefTypeLocalBranch, outputs[3].Sha},
	}
	// Need to sort for consistent comparison
	sort.Sort(test.RefsByName(expectedRefs))
	sort.Sort(test.RefsByName(refs))
	assert.Equal(t, expectedRefs, refs, "Refs should be correct")
}

func TestVersionCompare(t *testing.T) {
	assert.True(t, IsVersionAtLeast("2.6.0", "2.6.0"))
	assert.True(t, IsVersionAtLeast("2.6.0", "2.6"))
	assert.True(t, IsVersionAtLeast("2.6.0", "2"))
	assert.True(t, IsVersionAtLeast("2.6.10", "2.6.5"))
	assert.True(t, IsVersionAtLeast("2.8.1", "2.7.2"))

	assert.False(t, IsVersionAtLeast("1.6.0", "2"))
	assert.False(t, IsVersionAtLeast("2.5.0", "2.6"))
	assert.False(t, IsVersionAtLeast("2.5.0", "2.5.1"))
	assert.False(t, IsVersionAtLeast("2.5.2", "2.5.10"))
}

func TestGitAndRootDirs(t *testing.T) {
	repo := test.NewRepo(t)
	repo.Pushd()
	defer func() {
		repo.Popd()
		repo.Cleanup()
	}()

	git, root, err := GitAndRootDirs()
	if err != nil {
		t.Fatal(err)
	}

	expected, err := longpathos.Stat(git)
	if err != nil {
		t.Fatal(err)
	}

	actual, err := longpathos.Stat(filepath.Join(root, ".git"))
	if err != nil {
		t.Fatal(err)
	}

	assert.True(t, os.SameFile(expected, actual))
}

func TestGetTrackedFiles(t *testing.T) {
	repo := test.NewRepo(t)
	repo.Pushd()
	defer func() {
		repo.Popd()
		repo.Cleanup()
	}()

	// test commits; we'll just modify the same file each time since we're
	// only interested in branches
	inputs := []*test.CommitInput{
		{ // 0
			Files: []*test.FileInput{
				{Filename: "file1.txt", Size: 20},
				{Filename: "file2.txt", Size: 20},
				{Filename: "folder1/file10.txt", Size: 20},
				{Filename: "folder1/anotherfile.txt", Size: 20},
			},
		},
		{ // 1
			Files: []*test.FileInput{
				{Filename: "file3.txt", Size: 20},
				{Filename: "file4.txt", Size: 20},
				{Filename: "folder2/something.txt", Size: 20},
				{Filename: "folder2/folder3/deep.txt", Size: 20},
			},
		},
	}
	repo.AddCommits(inputs)

	tracked, err := GetTrackedFiles("*.txt")
	assert.Nil(t, err)
	sort.Strings(tracked) // for direct comparison
	fulllist := []string{"file1.txt", "file2.txt", "file3.txt", "file4.txt", "folder1/anotherfile.txt", "folder1/file10.txt", "folder2/folder3/deep.txt", "folder2/something.txt"}
	assert.Equal(t, fulllist, tracked)

	tracked, err = GetTrackedFiles("*file*.txt")
	assert.Nil(t, err)
	sort.Strings(tracked)
	sublist := []string{"file1.txt", "file2.txt", "file3.txt", "file4.txt", "folder1/anotherfile.txt", "folder1/file10.txt"}
	assert.Equal(t, sublist, tracked)

	tracked, err = GetTrackedFiles("folder1/*")
	assert.Nil(t, err)
	sort.Strings(tracked)
	sublist = []string{"folder1/anotherfile.txt", "folder1/file10.txt"}
	assert.Equal(t, sublist, tracked)

	tracked, err = GetTrackedFiles("folder2/*")
	assert.Nil(t, err)
	sort.Strings(tracked)
	sublist = []string{"folder2/folder3/deep.txt", "folder2/something.txt"}
	assert.Equal(t, sublist, tracked)

	// relative dir
	longpathos.Chdir("folder1")
	tracked, err = GetTrackedFiles("*.txt")
	assert.Nil(t, err)
	sort.Strings(tracked)
	sublist = []string{"anotherfile.txt", "file10.txt"}
	assert.Equal(t, sublist, tracked)
	longpathos.Chdir("..")

	// absolute paths only includes matches in repo root
	tracked, err = GetTrackedFiles("/*.txt")
	assert.Nil(t, err)
	sort.Strings(tracked)
	assert.Equal(t, []string{"file1.txt", "file2.txt", "file3.txt", "file4.txt"}, tracked)

	// Test includes staged but uncommitted files
	ioutil.WriteFile("z_newfile.txt", []byte("Hello world"), 0644)
	test.RunGitCommand(t, true, "add", "z_newfile.txt")
	tracked, err = GetTrackedFiles("*.txt")
	assert.Nil(t, err)
	sort.Strings(tracked)
	fulllist = append(fulllist, "z_newfile.txt")
	assert.Equal(t, fulllist, tracked)

	// Test includes modified files (not staged)
	ioutil.WriteFile("file1.txt", []byte("Modifications"), 0644)
	tracked, err = GetTrackedFiles("*.txt")
	assert.Nil(t, err)
	sort.Strings(tracked)
	assert.Equal(t, fulllist, tracked)

	// Test includes modified files (staged)
	test.RunGitCommand(t, true, "add", "file1.txt")
	tracked, err = GetTrackedFiles("*.txt")
	assert.Nil(t, err)
	sort.Strings(tracked)
	assert.Equal(t, fulllist, tracked)

	// Test excludes deleted files (not committed)
	test.RunGitCommand(t, true, "rm", "file2.txt")
	tracked, err = GetTrackedFiles("*.txt")
	assert.Nil(t, err)
	sort.Strings(tracked)
	deletedlist := []string{"file1.txt", "file3.txt", "file4.txt", "folder1/anotherfile.txt", "folder1/file10.txt", "folder2/folder3/deep.txt", "folder2/something.txt", "z_newfile.txt"}
	assert.Equal(t, deletedlist, tracked)

}

func TestLocalRefs(t *testing.T) {
	repo := test.NewRepo(t)
	repo.Pushd()
	defer func() {
		repo.Popd()
		repo.Cleanup()
	}()

	repo.AddCommits([]*test.CommitInput{
		{
			Files: []*test.FileInput{
				{Filename: "file1.txt", Size: 20},
			},
		},
		{
			NewBranch:      "branch",
			ParentBranches: []string{"master"},
			Files: []*test.FileInput{
				{Filename: "file1.txt", Size: 20},
			},
		},
	})

	test.RunGitCommand(t, true, "tag", "v1")

	refs, err := LocalRefs()
	if err != nil {
		t.Fatal(err)
	}

	actual := make(map[string]bool)
	for _, r := range refs {
		t.Logf("REF: %s", r.Name)
		switch r.Type {
		case RefTypeHEAD:
			t.Errorf("Local HEAD ref: %v", r)
		case RefTypeOther:
			t.Errorf("Stash or unknown ref: %v", r)
		case RefTypeRemoteBranch, RefTypeRemoteTag:
			t.Errorf("Remote ref: %v", r)
		default:
			actual[r.Name] = true
		}
	}

	expected := []string{"master", "branch", "v1"}
	found := 0
	for _, refname := range expected {
		if actual[refname] {
			found += 1
		} else {
			t.Errorf("could not find ref %q", refname)
		}
	}

	if found != len(expected) {
		t.Errorf("Unexpected local refs: %v", actual)
	}
}
