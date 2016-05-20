package api

import (
	"fmt"
	"net/http"
	"time"
)

type LockService struct {
}

func (s *LockService) Lock(req *LockRequest) (*RequestSchema, *LockResponse) {
	var resp LockResponse

	return &RequestSchema{
		Method: http.MethodPost,
		Path:   "/locks",
		Body:   req,
		Into:   &resp,
	}, &resp
}

func (s *LockService) Unlock(l *Lock) (*RequestSchema, UnlockResponse) {
	var resp UnlockResponse

	return &RequestSchema{
		Method: http.MethodPost,
		Path:   fmt.Sprintf("/locks/%s/unlock", l.Id),
		Into:   resp,
	}, resp
}

// Lock represents a single lock that against a particular path.
//
// Locks returned from the API may or may not be currently active, according to
// the Expired flag.
type Lock struct {
	// Id is the unique identifier corresponding to this particular Lock. It
	// must be consistent with the local copy, and the server's copy.
	Id string `json:"id"`
	// Path is an absolute path to the file that is locked as a part of this
	// lock.
	Path string `json:"path"`
	// Committer is the author who initiated this lock.
	Committer Committer `json:"committer"`
	// CommitSHA is the commit that this Lock was created against. It is
	// strictly equal to the SHA of the minimum commit negotiated in order
	// to create this lock.
	CommitSHA string `json:"commit_sha"`
	// LockedAt is a required parameter that represents the instant in time
	// that this lock was created. For most server implementations, this
	// should be set to the instant at which the lock was initially
	// received.
	LockedAt time.Time `json:"locked_at"`
	// ExpiresAt is an optional parameter that represents the instant in
	// time that the lock stopped being active. If the lock is still active,
	// the server can either a) not send this field, or b) send the
	// zero-value of time.Time.
	UnlockedAt time.Time `json:"unlocked_at,omitempty"`
}

// Active returns whether or not the given lock is still active against the file
// that it is protecting.
func (l *Lock) Active() bool {
	return l.UnlockedAt.IsZero()
}

type Committer struct {
	// Name is the name of the individual who would like to obtain the
	// lock, for instance: "Rick Olson".
	Name string `json:"name"`
	// Email is the email assopsicated with the individual who would
	// like to obtain the lock, for instance: "rick@github.com".
	Email string `json:"email"`
}

// LockRequest encapsulates the payload sent across the API when a client would
// like to obtain a lock against a particular path on a given remote.
type LockRequest struct {
	// Path is the path that the client would like to obtain a lock against.
	Path string `json:"path"`
	// LatestRemoteCommit is the SHA of the last known commit from the
	// remote that we are trying to create the lock against, as found in
	// `.git/refs/origin/<name>`.
	LatestRemoteCommit string `json:"latest_remote_commit"`
	// Committer is the individual that wishes to obtain the lock.
	Committer Committer `json:"committer"`
}

// LockResponse encapsulates the information sent over the API in response to
// a `LockRequest`.
type LockResponse struct {
	// Lock is the Lock that was optionally created in response to the
	// payload that was sent (see above). If the lock already exists, then
	// the existing lock is sent in this field instead, and the author of
	// that lock remains the same, meaning that the client failed to obtain
	// that lock. An HTTP status of "409 - Conflict" is used here.
	//
	// If the lock was unable to be created, this field will hold the
	// zero-value of Lock and the Err field will provide a more detailed set
	// of information.
	//
	// If an error was experienced in creating this lock, then the
	// zero-value of Lock should be sent here instead.
	Lock *Lock `json:"lock"`
	// CommitNeeded holds the minimum commit SHA that client must have to
	// obtain the lock.
	CommitNeeded string `json:"commit_needed,omitempty"`
	// Err is the optional error that was encountered while trying to create
	// the above lock.
	Err string `json:"error,omitempty"`
}

// UnlockResponse is the result sent back from the API when asked to remove a
// lock.
type UnlockResponse struct {
	// Lock is the lock corresponding to the asked-about lock in the
	// `UnlockPayload` (see above). If no matching lock was found, this
	// field will take the zero-value of Lock, and Err will be non-nil.
	Lock *Lock `json:"lock"`
	// Err is an optional field which holds any error that was experienced
	// while removing the lock.
	Err string `json:"error,omitempty"`
}
