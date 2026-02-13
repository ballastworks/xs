package xrwm

// State represents the state of a RWMutex lock specifically in the
// context of the pattern the RunRW function implements in xsync.
//
// If using the RunRW function in a hot path, you may want to implement
// a localized version of it to avoid the possible overhead of
// function calls and lack of compile-time-chosen inlining.
//
// The State type and its constants are exported for this purpose.
type State uint8

const (
	Unlocked State = iota
	RLocked
	WLocked
)
