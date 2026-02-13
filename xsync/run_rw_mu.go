package xsync

import (
	"sync"

	"github.com/ballastworks/xs/xsync/xrwm"
)

// RunRW acquires a read lock on the provided RWMutex, then calls canRun with
// writeLocked set to false. If canRun returns true, the read lock is released
// and a write lock is acquired. Then canRun is called again with writeLocked
// set to true. If it returns true again, the run function is executed while
// holding the write lock. The function returns true if run was executed,
// otherwise false. The locks are properly released in all cases.
//
// This function is useful for scenarios where you want to perform an operation
// that may require upgrading from a read lock to a write lock based on certain
// conditions checked in canRun where it is more optimal to refrain from first
// acquiring a write lock unless necessary. So, if you need to confirm that a
// concurrent structure is in a certain state before modifying it, you can do so
// without blocking other readers until you are sure you need to write.
//
// If using this pattern in a hot path, you may want to instead implement a copy
// of this function specialized for your use case to avoid the overhead of
// function calls. The xrwm.State type and its constants
// ( Unlocked, RLocked, WLocked ) are exported for this purpose.
func RunRW(mu *sync.RWMutex, canRun func(writeLocked bool) bool, run func()) (didRun bool) {
	mu.RLock()
	st := xrwm.RLocked

	defer func() {
		switch st {
		case xrwm.RLocked:
			mu.RUnlock()
		case xrwm.WLocked:
			mu.Unlock()
		}
	}()

	if !canRun(false) {
		return false
	}

	mu.RUnlock()
	st = xrwm.Unlocked

	mu.Lock()
	st = xrwm.WLocked

	if !canRun(true) {
		return false
	}

	run()
	return true
}

//
//
// Note that for lazy initialization and synchronous use, you might be better served by
// this pattern below.
//
// Note that the mutex and load operation can be replaced with a channel and a COMPARE_AND_SWAP operation
// to quickly unlock all callers that could be blocking with little ceremony if the extra heap allocations
// of the channel are acceptable.
//
//

// type lazyInitThing struct {
// 	initDone atomic.Bool
// 	mu       sync.Mutex
// 	thing    *thingToInitAndDo
// }

// func (z *lazyInitThing) Do() {
// 	if z.initDone.Load() {
// 		z.thing.Do()
// 		return
// 	}

// 	z.mu.Lock()
// 	defer z.mu.Unlock()

// 	if z.initDone.Load() {
// 		z.thing.Do()
// 		return
// 	}

// 	// lazy initialize the thing here for potentially multiple calls...
// 	z.thing = &thingToInitAndDo{}

// 	// signal that the initialization is done
// 	z.initDone.Store(true)
// }

//
//
// Note that the above is great for fast initialization times and low contention. In such cases the mutex convoys of a routine acquiring an exclusive write lock, checking the atomic init boolean, and then unlocking are not going to be long nor will the delay be significantly observable. You'll really want to use a channel-like mechanism to unlock faster (as fast as the go scheduler can go) but channels will require allocations up front for something that may not even be used.
//
//

//
//
// Should the initialization take considerable time and the number of concurrent callers be non-trivial in that timeframe the mutex convoy can be considerably long after initialization completes.
//
// In those such cases, instead utilize a wait group with atomic gate initialization logic.
// This pattern is great because it does not require any additional allocations on the heap and can be pooled
// fairly easily.
//
// At first glance it feels kinda verbose and hectic; maybe even a bit counter intuitive given a mutex is still
// being used. However that mutex is used only in a fast timeframe to initialize the wait group properly. You may be
// tempted to initialize the lazy resolver with the wait group pre-called with .Add(1) but don't. You'll need to account
// for restoring the lazy initializer to a pool-able reset-state should you wish to pool it. If pooling is not important
// and allocations are not a concern then just use a channel to keep things simplest.
//
//

// type lazyInitThing struct {
// 	// 0 = uninitialized (no barrier), 1 = barrier up and initializing, 2 = barrier down
// 	initState atomic.Uint32

// 	mu sync.Mutex     // only taken on the slow-path (first wave ideally)
// 	wg sync.WaitGroup // wg is a semaphore barrier: Add(1) once, Done() once

// 	thing *thingToInitAndDo
// }

// func (z *lazyInitThing) Do() {
// 	// Fast path: already initialized.
// 	if z.initState.Load() == 2 {
// 		z.thing.Do()
// 		return
// 	}

// 	// Ensure the barrier is armed before anyone can Wait on it.
// 	if z.initState.Load() == 0 {
// 		z.mu.Lock()
// 		st := xrwm.WLocked
// 		defer func() {
// 			switch st {
// 			case xrwm.WLocked:
// 				z.mu.Unlock()
// 			}
// 		}()

// 		if z.initState.Load() == 0 {
// 			z.wg.Add(1)          // raise the initializing barrier
// 			z.initState.Store(1) // publish "initializing"
// 			z.mu.Unlock()
// 			st = xrwm.Unlocked

// 			// Perform the initialization of thing
// 			{
// 				z.thing = &thingToInitAndDo{}
// 			}

// 			// Publish readiness and release all waiters.
// 			z.initState.Store(2)
// 			z.wg.Done() // could be deferred if the Do is SUPER QUICK

// 			z.thing.Do()
// 			return
// 		}

// 		z.mu.Unlock()
// 		st = xrwm.Unlocked
// 	}

// 	// Any concurrent callers during init block here; all are released on Done().
// 	z.wg.Wait()
// 	z.thing.Do()
// }
