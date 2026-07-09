package coord

import "errors"

var (
	ErrInvalidTransition = errors.New("invalid state transition")
)

type State string

const (
	StateInit       State = "INIT"
	StatePreparing  State = "PREPARING"
	StateCommitting State = "COMMITTING"
	StateCommitted  State = "COMMITTED"
	StateAborting   State = "ABORTING"
	StateAborted    State = "ABORTED"
	StateFailed     State = "FAILED"
)

var validTransitions = map[State][]State{
	StateInit:       {StatePreparing, StateAborting},
	StatePreparing:  {StateCommitting, StateAborting},
	StateCommitting: {StateCommitted, StateAborting},
	StateCommitted:  {},
	StateAborting:   {StateAborted},
	StateAborted:    {},
	StateFailed:     {},
}

func CanTransition(from, to State) bool {
	for _, allowed := range validTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

func IsTerminal(s State) bool {
	allowed, ok := validTransitions[s]
	return ok && len(allowed) == 0
}

func TerminalStates() []State {
	return []State{StateCommitted, StateAborted, StateFailed}
}
