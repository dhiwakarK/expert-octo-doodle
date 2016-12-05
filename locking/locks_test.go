package locking

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/git-lfs/git-lfs/api"
	"github.com/git-lfs/git-lfs/config"
	"github.com/stretchr/testify/assert"
)

func TestLockCache(t *testing.T) {
	var err error

	oldStore := config.LocalGitStorageDir
	config.LocalGitStorageDir, err = ioutil.TempDir("", "testCacheLock")
	assert.Nil(t, err)
	defer func() {
		os.RemoveAll(config.LocalGitStorageDir)
		config.LocalGitStorageDir = oldStore
	}()

	client, err := NewClient(config.NewFrom(config.Values{}))
	assert.Nil(t, err)

	testLocks := []Lock{
		Lock{Path: "folder/test1.dat", Id: "101"},
		Lock{Path: "folder/test2.dat", Id: "102"},
		Lock{Path: "root.dat", Id: "103"},
	}

	for _, l := range testLocks {
		err = client.cacheLock(l)
		assert.Nil(t, err)
	}

	locks := client.cachedLocks()
	for _, l := range testLocks {
		assert.Contains(t, locks, l)
	}
	assert.Equal(t, len(testLocks), len(locks))

	err = client.cacheUnlockByPath("folder/test2.dat")
	assert.Nil(t, err)

	locks = client.cachedLocks()
	// delete item 1 from test locls
	testLocks = append(testLocks[:1], testLocks[2:]...)
	for _, l := range testLocks {
		assert.Contains(t, locks, l)
	}
	assert.Equal(t, len(testLocks), len(locks))

	err = client.cacheUnlockById("101")
	assert.Nil(t, err)

	locks = client.cachedLocks()
	testLocks = testLocks[1:]
	for _, l := range testLocks {
		assert.Contains(t, locks, l)
	}
	assert.Equal(t, len(testLocks), len(locks))
}

type TestLifecycle struct {
}

func (l *TestLifecycle) Build(schema *api.RequestSchema) (*http.Request, error) {
	return http.NewRequest("GET", "http://dummy", nil)
}

func (l *TestLifecycle) Execute(req *http.Request, into interface{}) (api.Response, error) {
	// Return test data including other users
	locks := api.LockList{Locks: []api.Lock{
		api.Lock{Id: "99", Path: "folder/test3.dat", Committer: api.Committer{Name: "Alice", Email: "alice@wonderland.com"}},
		api.Lock{Id: "101", Path: "folder/test1.dat", Committer: api.Committer{Name: "Fred", Email: "fred@bloggs.com"}},
		api.Lock{Id: "102", Path: "folder/test2.dat", Committer: api.Committer{Name: "Fred", Email: "fred@bloggs.com"}},
		api.Lock{Id: "103", Path: "root.dat", Committer: api.Committer{Name: "Fred", Email: "fred@bloggs.com"}},
		api.Lock{Id: "199", Path: "other/test1.dat", Committer: api.Committer{Name: "Charles", Email: "charles@incharge.com"}},
	}}
	locksJson, _ := json.Marshal(locks)
	r := &http.Response{
		Status:     "200 OK",
		StatusCode: 200,
		Proto:      "HTTP/1.0",
		Body:       ioutil.NopCloser(bytes.NewReader(locksJson)),
	}
	if into != nil {
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(into); err != nil {
			return nil, err
		}
	}
	return api.WrapHttpResponse(r), nil
}
func (l *TestLifecycle) Cleanup(resp api.Response) error {
	return resp.Body().Close()
}

func TestRefreshCache(t *testing.T) {
	var err error
	oldStore := config.LocalGitStorageDir
	config.LocalGitStorageDir, err = ioutil.TempDir("", "testCacheLock")
	assert.Nil(t, err)
	defer func() {
		os.RemoveAll(config.LocalGitStorageDir)
		config.LocalGitStorageDir = oldStore
	}()

	cfg := config.NewFrom(config.Values{
		Git: map[string]string{"user.name": "Fred", "user.email": "fred@bloggs.com"}})
	client, err := NewClient(cfg)
	assert.Nil(t, err)
	// Override api client for testing
	client.apiClient = api.NewClient(&TestLifecycle{})

	// Should start with no cached items
	locks := client.cachedLocks()
	assert.Empty(t, locks)

	// Should load from test data, just Fred's
	err = client.fetchLocksToCache()
	assert.Nil(t, err)

	locks = client.cachedLocks()
	// Need to include zero time in structure for equal to work
	zeroTime := time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, []Lock{
		Lock{Path: "folder/test1.dat", Id: "101", Name: "Fred", Email: "fred@bloggs.com", LockedAt: zeroTime, UnlockedAt: zeroTime},
		Lock{Path: "folder/test2.dat", Id: "102", Name: "Fred", Email: "fred@bloggs.com", LockedAt: zeroTime, UnlockedAt: zeroTime},
		Lock{Path: "root.dat", Id: "103", Name: "Fred", Email: "fred@bloggs.com", LockedAt: zeroTime, UnlockedAt: zeroTime},
	}, locks)

}
