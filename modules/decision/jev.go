package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	contract "github.com/TundraWork/Ex-Otogi/pkg/decision"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// maxReply bounds how much of a reply is read.
const maxReply = 1 << 20

// jevRequest is the body of a Jev request.
type jevRequest struct {
	Model     string                 `json:"model"`
	State     json.RawMessage        `json:"state"`
	Questions map[string]jevQuestion `json:"questions"`
}

// jevQuestion is a Jev yes/no question ("noul").
type jevQuestion struct {
	Type         string       `json:"type"`
	Instructions string       `json:"instructions"`
	Criteria     *jevCriteria `json:"criteria,omitempty"`
}

// jevCriteria says what counts as yes and as no.
type jevCriteria struct {
	True  string `json:"true"`
	False string `json:"false"`
}

// jevReply is the body of a Jev answer.
type jevReply struct {
	Model   string               `json:"model"`
	Answers map[string]jevAnswer `json:"answers"`
	Usage   jevUsage             `json:"usage"`
}

// jevAnswer is one question's answer; Noul is the probability of yes.
type jevAnswer struct {
	Type string   `json:"type"`
	Noul *float64 `json:"noul"`
}

// jevUsage is what a request used.
type jevUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// jevError is the body of a Jev error.
type jevError struct {
	Detail struct {
		Message string `json:"message"`
	} `json:"detail"`
}

// jev is a TypeSafe Jev client.
type jev struct {
	url     string
	key     string
	model   string
	timeout time.Duration
	http    *http.Client
}

// exchange is what one request sent and got back, for the record.
type exchange struct {
	model     string // the model that answered, else the one asked for
	state     json.RawMessage
	questions map[string]jevQuestion
	status    int
	requestID string
	reply     []byte
	usage     jevUsage
}

// decide sends req to Jev. It does not retry.
func (j *jev) decide(ctx context.Context, req contract.Request) (contract.Answers, exchange, error) {
	ex := exchange{model: j.model}
	state, err := json.Marshal(req.State)
	if err != nil {
		return nil, ex, fmt.Errorf("jev: state: %w: %w", err, otogi.ErrInvalid)
	}
	ex.state = state
	ex.questions = make(map[string]jevQuestion, len(req.Questions))
	for id, q := range req.Questions {
		jq := jevQuestion{Type: "noul", Instructions: q.Instructions}
		if q.Criteria != nil {
			jq.Criteria = &jevCriteria{True: q.Criteria.True, False: q.Criteria.False}
		}
		ex.questions[id] = jq
	}
	body, err := json.Marshal(jevRequest{Model: j.model, State: state, Questions: ex.questions})
	if err != nil {
		return nil, ex, fmt.Errorf("jev: request: %w: %w", err, otogi.ErrInvalid)
	}

	ctx, cancel := context.WithTimeout(ctx, j.timeout)
	defer cancel()
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, j.url, bytes.NewReader(body))
	if err != nil {
		return nil, ex, fmt.Errorf("jev: request: %w: %w", err, otogi.ErrInvalid)
	}
	hreq.Header.Set("Authorization", "Bearer "+j.key)
	hreq.Header.Set("Content-Type", "application/json")
	resp, err := j.http.Do(hreq)
	if err != nil {
		return nil, ex, fmt.Errorf("jev: %w: %w", err, otogi.ErrUnavailable)
	}
	defer func() { _ = resp.Body.Close() }()
	ex.status = resp.StatusCode
	ex.requestID = resp.Header.Get("x-typesafe-request-id")
	ex.reply, err = io.ReadAll(io.LimitReader(resp.Body, maxReply))
	if err != nil {
		return nil, ex, fmt.Errorf("jev: read reply: %w: %w", err, otogi.ErrUnavailable)
	}

	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("jev: HTTP %d", resp.StatusCode)
		var e jevError
		if json.Unmarshal(ex.reply, &e) == nil && e.Detail.Message != "" {
			msg += ": " + e.Detail.Message
		}
		// Rate limits and outages may pass; any other refusal is the
		// request's.
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return nil, ex, fmt.Errorf("%s: %w", msg, otogi.ErrUnavailable)
		}
		return nil, ex, fmt.Errorf("%s: %w", msg, otogi.ErrInvalid)
	}

	var reply jevReply
	if err := json.Unmarshal(ex.reply, &reply); err != nil {
		return nil, ex, fmt.Errorf("jev: reply: %w: %w", err, otogi.ErrUnavailable)
	}
	if reply.Answers == nil {
		return nil, ex, fmt.Errorf("jev: reply has no answers: %w", otogi.ErrUnavailable)
	}
	if reply.Model != "" {
		ex.model = reply.Model
	}
	ex.usage = reply.Usage
	answers := make(contract.Answers, len(reply.Answers))
	for id, a := range reply.Answers {
		if a.Noul != nil {
			answers[id] = *a.Noul
		}
	}
	return answers, ex, nil
}
