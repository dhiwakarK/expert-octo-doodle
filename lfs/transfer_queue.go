package lfs

import (
	"fmt"
	"github.com/cheggaaa/pb"
	"sync"
	"sync/atomic"
)

type Transferable interface {
	Check() (*objectResource, *WrappedError)
	Transfer(CopyCallback) *WrappedError
	Object() *objectResource
	Oid() string
	Size() int64
	SetObject(*objectResource)
}

// TransferQueue provides a queue that will allow concurrent transfers.
type TransferQueue struct {
	transferc        chan Transferable
	errorc           chan *WrappedError
	errors           []*WrappedError
	wg               sync.WaitGroup
	workers          int
	files            int
	finished         int64
	size             int64
	authCond         *sync.Cond
	transferables    map[string]Transferable
	bar              *pb.ProgressBar
	clientAuthorized int32
	transferKind     string
}

// newTransferQueue builds a TransferQueue, allowing `workers` concurrent transfers.
func newTransferQueue(workers, files int) *TransferQueue {
	return &TransferQueue{
		transferc:     make(chan Transferable, files),
		errorc:        make(chan *WrappedError),
		workers:       workers,
		files:         files,
		authCond:      sync.NewCond(&sync.Mutex{}),
		transferables: make(map[string]Transferable),
	}
}

// Add adds an Uploadable to the upload queue.
func (q *TransferQueue) Add(t Transferable) {
	q.transferables[t.Oid()] = t
}

// apiWorker processes the queue, making the POST calls and
// feeding the results to uploadWorkers
func (q *TransferQueue) processIndividual() {
	apic := make(chan Transferable, q.files)
	workersReady := make(chan int, q.workers)
	var wg sync.WaitGroup

	for i := 0; i < q.workers; i++ {
		go func() {
			workersReady <- 1
			for t := range apic {
				// If an API authorization has not occured, we wait until we're woken up.
				q.authCond.L.Lock()
				if atomic.LoadInt32(&q.clientAuthorized) == 0 {
					q.authCond.Wait()
				}
				q.authCond.L.Unlock()

				obj, err := t.Check()
				if err != nil {
					q.errorc <- err
					wg.Done()
					continue
				}
				if obj != nil {
					q.wg.Add(1)
					t.SetObject(obj)
					q.transferc <- t
				}
				wg.Done()
			}
		}()
	}

	q.bar.Prefix(fmt.Sprintf("(%d of %d files) ", q.finished, len(q.transferables)))
	q.bar.Start()

	for _, t := range q.transferables {
		wg.Add(1)
		apic <- t
	}

	<-workersReady
	q.authCond.Signal() // Signal the first goroutine to run
	close(apic)
	wg.Wait()

	close(q.transferc)
}

// batchWorker makes the batch POST call, feeding the results
// to the transfer workers
func (q *TransferQueue) processBatch() {
	q.files = 0
	transfers := make([]*objectResource, 0, len(q.transferables))
	for _, t := range q.transferables {
		transfers = append(transfers, &objectResource{Oid: t.Oid(), Size: t.Size()})
	}

	objects, err := Batch(transfers)
	if err != nil {
		q.errorc <- err
		sendApiEvent(apiEventFail)
		return
	}

	for _, o := range objects {
		if _, ok := o.Links[q.transferKind]; ok {
			// This object needs to be transfered
			if transfer, ok := q.transferables[o.Oid]; ok {
				q.files++
				q.wg.Add(1)
				transfer.SetObject(o)
				q.transferc <- transfer
			}
		}
	}

	close(q.transferc)
	q.bar.Prefix(fmt.Sprintf("(%d of %d files) ", q.finished, q.files))
	q.bar.Start()
	sendApiEvent(apiEventSuccess) // Wake up upload workers
}

// Process starts the upload queue and displays a progress bar.
func (q *TransferQueue) Process() {
	q.bar = pb.New64(q.size)
	q.bar.SetUnits(pb.U_BYTES)
	q.bar.ShowBar = false

	// This goroutine collects errors returned from uploads
	go func() {
		for err := range q.errorc {
			q.errors = append(q.errors, err)
		}
	}()

	// This goroutine watches for apiEvents. In order to prevent multiple
	// credential requests from happening, the queue is processed sequentially
	// until an API request succeeds (meaning authenication has happened successfully).
	// Once the an API request succeeds, all worker goroutines are woken up and allowed
	// to process uploads. Once a success happens, this goroutine exits.
	go func() {
		for {
			event := <-apiEvent
			switch event {
			case apiEventSuccess:
				atomic.StoreInt32(&q.clientAuthorized, 1)
				q.authCond.Broadcast() // Wake all remaining goroutines
				return
			case apiEventFail:
				q.authCond.Signal() // Wake the next goroutine
			}
		}
	}()

	for i := 0; i < q.workers; i++ {
		// These are the worker goroutines that process uploads
		go func(n int) {

			for transfer := range q.transferc {
				cb := func(total, read int64, current int) error {
					q.bar.Add(current)
					return nil
				}

				if err := transfer.Transfer(cb); err != nil {
					q.errorc <- err
				}

				f := atomic.AddInt64(&q.finished, 1)
				q.bar.Prefix(fmt.Sprintf("(%d of %d files) ", f, q.files))
				q.wg.Done()
			}
		}(i)
	}

	if Config.BatchTransfer() {
		q.processBatch()
	} else {
		q.processIndividual()
	}

	q.wg.Wait()
	close(q.errorc)

	q.bar.Finish()
}

// Errors returns any errors encountered during uploading.
func (q *TransferQueue) Errors() []*WrappedError {
	return q.errors
}
