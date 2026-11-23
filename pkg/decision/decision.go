// Package decision is the contract between modules and the decision
// service: yes/no questions about a state, answered with calibrated
// probabilities. The decision module provides it as Service; a module that
// asks lists Service in its descriptor's Requires and looks it up with
// otogi.Lookup[Decider].
package decision

import "context"

// Service is the name the decision service is provided under.
const Service = "decision"

// Decider answers yes/no questions about a state. It is safe for concurrent
// use and records every request in the event log.
type Decider interface {
	// Decide sends one request. Errors wrap otogi.ErrInvalid when the
	// request is malformed or rejected, and otogi.ErrUnavailable for
	// timeouts, rate limits, and outages; it does not retry. Callers treat
	// an error as "no decision", never as "no".
	Decide(ctx context.Context, req Request) (Answers, error)
}

// Request is the state and the questions asked about it.
type Request struct {
	// State is sent as JSON; questions refer to its fields.
	State any
	// Questions are keyed by IDs the caller chooses; at least one.
	Questions map[string]Question
}

// Question is a yes/no question.
type Question struct {
	// Instructions is the whole question, as text. It names what in the
	// state it is about, such as which message to judge.
	Instructions string
	// Criteria, if set, says what counts as yes and as no.
	Criteria *Criteria
}

// Criteria describes the two answers.
type Criteria struct {
	True  string
	False string
}

// Answers maps question IDs to the probability that the answer is yes. A
// question the service did not answer is missing.
type Answers map[string]float64
