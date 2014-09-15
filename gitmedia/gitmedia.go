package gitmedia

import (
	"fmt"
	"github.com/github/git-media/gitconfig"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const Version = "0.2.2"

var (
	LargeSizeThreshold = 5 * 1024 * 1024
	TempDir            = filepath.Join(os.TempDir(), "git-media")
	UserAgent          string
	LocalWorkingDir    string
	LocalGitDir        string
	LocalMediaDir      string
	LocalLogDir        string
	checkedTempDir     string
)

func TempFile(prefix string) (*os.File, error) {
	if checkedTempDir != TempDir {
		if err := os.MkdirAll(TempDir, 0774); err != nil {
			return nil, err
		}
		checkedTempDir = TempDir
	}

	return ioutil.TempFile(TempDir, prefix)
}

func ResetTempDir() error {
	checkedTempDir = ""
	return os.RemoveAll(TempDir)
}

func LocalMediaPath(sha string) (string, error) {
	path := filepath.Join(LocalMediaDir, sha[0:2], sha[2:4])
	if err := os.MkdirAll(path, 0744); err != nil {
		return "", fmt.Errorf("Error trying to create local media directory in '%s': %s", path, err)
	}

	return filepath.Join(path, sha), nil
}

func Environ() []string {
	osEnviron := os.Environ()
	env := make([]string, 4, len(osEnviron)+4)
	env[0] = fmt.Sprintf("LocalWorkingDir=%s", LocalWorkingDir)
	env[1] = fmt.Sprintf("LocalGitDir=%s", LocalGitDir)
	env[2] = fmt.Sprintf("LocalMediaDir=%s", LocalMediaDir)
	env[3] = fmt.Sprintf("TempDir=%s", TempDir)

	for _, e := range osEnviron {
		if !strings.Contains(e, "GIT_") {
			continue
		}
		env = append(env, e)
	}

	return env
}

func InRepo() bool {
	return LocalWorkingDir != ""
}

func init() {
	var err error
	LocalWorkingDir, LocalGitDir, err = resolveGitDir()
	if err == nil {
		LocalMediaDir = filepath.Join(LocalGitDir, "media")
		LocalLogDir = filepath.Join(LocalMediaDir, "logs")
		TempDir = filepath.Join(LocalMediaDir, "tmp")
		queueDir = setupQueueDir()

		if err := os.MkdirAll(TempDir, 0744); err != nil {
			panic(fmt.Errorf("Error trying to create temp directory in '%s': %s", TempDir, err))
		}
	}

	gitVersion, err := gitconfig.Version()
	if err != nil {
		gitVersion = "unknown"
	}

	UserAgent = fmt.Sprintf("git-media/%s (%s; git %s; go %s)", Version,
		runtime.GOOS,
		strings.Replace(gitVersion, "git version ", "", 1),
		strings.Replace(runtime.Version(), "go", "", 1))
}

func resolveGitDir() (string, string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", "", err
	}

	return recursiveResolveGitDir(wd)
}

func recursiveResolveGitDir(dir string) (string, string, error) {
	var cleanDir = filepath.Clean(dir)
	if cleanDir[len(cleanDir)-1] == os.PathSeparator {
		return "", "", fmt.Errorf("Git repository not found")
	}

	if filepath.Base(dir) == gitExt {
		return filepath.Dir(dir), dir, nil
	}

	gitDir := filepath.Join(dir, gitExt)
	if info, err := os.Stat(gitDir); err == nil {
		if info.IsDir() {
			return dir, gitDir, nil
		} else {
			return processDotGitFile(gitDir)
		}
	}

	return recursiveResolveGitDir(filepath.Dir(dir))
}

func processDotGitFile(file string) (string, string, error) {
	f, err := os.Open(file)
	defer f.Close()

	if err != nil {
		return "", "", err
	}

	data := make([]byte, 512)
	n, err := f.Read(data)
	if err != nil {
		return "", "", err
	}

	contents := string(data[0:n])
	wd, _ := os.Getwd()
	if strings.HasPrefix(contents, gitPtrPrefix) {
		dir := strings.TrimSpace(strings.Split(contents, gitPtrPrefix)[1])
		absDir, _ := filepath.Abs(dir)
		return wd, absDir, nil
	}

	return wd, "", nil
}

const (
	gitExt       = ".git"
	gitPtrPrefix = "gitdir: "
)
