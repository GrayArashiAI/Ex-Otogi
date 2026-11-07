package management

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Running is a delivery a module is handling now.
type Running struct {
	Module       string    `json:"module"`
	Origin       string    `json:"origin" doc:"The event ID, or the run ID of a job or timer."`
	Event        string    `json:"event,omitempty"`
	Platform     string    `json:"platform,omitempty"`
	Account      string    `json:"account,omitempty"`
	Conversation string    `json:"conversation,omitempty"`
	Started      time.Time `json:"started"`
}

// Task is one event some module did something about, beyond receiving it
// and finishing without a word: its records taken together.
type Task struct {
	Event        string    `json:"event"`
	Platform     string    `json:"platform,omitempty"`
	Account      string    `json:"account,omitempty"`
	Conversation string    `json:"conversation,omitempty"`
	Action       string    `json:"action,omitempty" doc:"The event's action, such as message.created."`
	Actor        string    `json:"actor,omitempty" doc:"Who the event is from, by name."`
	Text         string    `json:"text,omitempty" doc:"The event's message as plain text."`
	Started      time.Time `json:"started" doc:"When the work of its first record began."`
	Ended        time.Time `json:"ended" doc:"When its last record was written."`
	Records      int       `json:"records"`
	Outcome      string    `json:"outcome" enum:"running,error,slow,degraded,ok" doc:"running while a module handles it; otherwise the worst outcome among its records."`
	Components   []string  `json:"components" nullable:"false" doc:"The components that did something for it."`
	Running      []Running `json:"running" nullable:"false" doc:"Its deliveries running now."`
	Cursor       int64     `json:"cursor" doc:"Pass as before for the tasks older than this one."`
}

// TaskQuery selects tasks. Zero fields do not filter.
type TaskQuery struct {
	Platform, Conversation string
	// Outcome is running, or a worst outcome: error, slow, degraded, or ok.
	Outcome string
	// Text matches tasks with a record whose payload contains it.
	Text string
	// Before pages to tasks older than this cursor.
	Before int64
	Limit  int
}

// TaskPage is one page of tasks, newest first.
type TaskPage struct {
	Tasks []Task `json:"tasks" nullable:"false"`
	More  bool   `json:"more"`
}

// work is the records that say a module did something: every record but
// an event's receipt and the plain finish of a delivery.
const work = "event != '' AND NOT (kind = 'event.received' OR (kind = 'module.completed' AND outcome = 'ok'))"

// worst ranks outcomes; outcomes are named by their rank.
const worst = "MAX(CASE outcome WHEN 'error' THEN 3 WHEN 'slow' THEN 2 WHEN 'degraded' THEN 1 ELSE 0 END)"

var outcomeRanks = []string{"ok", "degraded", "slow", "error"}

// Tasks returns matching tasks, newest first by their first work record.
// running is what modules handle now: those tasks are marked running, and
// on the first page those with no work recorded yet come too.
func (l *EventLog) Tasks(ctx context.Context, q TaskQuery, running []Running) (TaskPage, error) {
	if q.Limit <= 0 {
		q.Limit = 50
	}
	q.Limit = min(q.Limit, MaxPageSize)
	now := map[string][]Running{}
	for _, r := range running {
		if r.Event != "" && (q.Platform == "" || r.Platform == q.Platform) && (q.Conversation == "" || r.Conversation == q.Conversation) {
			now[r.Event] = append(now[r.Event], r)
		}
	}
	var page TaskPage
	var events []string
	cursors := map[string]int64{}
	if q.Outcome == "running" {
		for event := range now {
			events = append(events, event)
		}
	} else {
		where, args := []string{work}, []any{}
		for _, f := range []struct{ column, value string }{{"platform", q.Platform}, {"conversation", q.Conversation}} {
			if f.value != "" {
				where, args = append(where, f.column+" = ?"), append(args, f.value)
			}
		}
		if q.Text != "" {
			where, args = append(where, "event IN (SELECT event FROM records WHERE payload LIKE ? ESCAPE '\\')"), append(args, "%"+likeEscaper.Replace(q.Text)+"%")
		}
		var having []string
		if q.Before != 0 {
			having, args = append(having, "first < ?"), append(args, q.Before)
		}
		if rank := slices.Index(outcomeRanks, q.Outcome); rank >= 0 {
			having, args = append(having, worst+" = ?"), append(args, rank)
		}
		query := "SELECT event, MIN(id) AS first FROM records WHERE " + strings.Join(where, " AND ") + " GROUP BY event"
		if len(having) > 0 {
			query += " HAVING " + strings.Join(having, " AND ")
		}
		rows, err := l.db.QueryContext(ctx, query+" ORDER BY first DESC LIMIT ?", append(args, q.Limit+1)...)
		if err != nil {
			return TaskPage{}, fmt.Errorf("event log: tasks: %w", err)
		}
		for rows.Next() {
			var event string
			var first int64
			if err := rows.Scan(&event, &first); err != nil {
				_ = rows.Close()
				return TaskPage{}, fmt.Errorf("event log: tasks: %w", err)
			}
			events, cursors[event] = append(events, event), first
		}
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			return TaskPage{}, fmt.Errorf("event log: tasks: %w", err)
		}
		if page.More = len(events) > q.Limit; page.More {
			events = events[:q.Limit]
		}
		if q.Before == 0 && q.Outcome == "" && q.Text == "" {
			for event := range now {
				if _, listed := cursors[event]; !listed {
					events = append(events, event)
				}
			}
		}
	}
	var err error
	if page.Tasks, err = l.tasks(ctx, events, now); err != nil {
		return TaskPage{}, err
	}
	for i := range page.Tasks {
		if c, ok := cursors[page.Tasks[i].Event]; ok {
			page.Tasks[i].Cursor = c
		}
	}
	slices.SortFunc(page.Tasks, func(a, b Task) int { return cmp.Compare(b.Cursor, a.Cursor) })
	return page, nil
}

// tasks sums up the records of events.
func (l *EventLog) tasks(ctx context.Context, events []string, running map[string][]Running) ([]Task, error) {
	out := []Task{}
	if len(events) == 0 {
		return out, nil
	}
	args := make([]any, len(events))
	for i, e := range events {
		args[i] = e
	}
	rows, err := l.db.QueryContext(ctx, `SELECT event, MIN(id), MIN(at - duration_ms), MAX(at), COUNT(*), `+worst+`,
		MAX(platform), MAX(account), MAX(conversation),
		COALESCE(GROUP_CONCAT(DISTINCT CASE WHEN `+work+` THEN component END), ''),
		COALESCE(MAX(CASE WHEN kind = 'event.received' THEN payload END), 'null')
		FROM records WHERE event IN (?`+strings.Repeat(",?", len(events)-1)+`) GROUP BY event`, args...)
	if err != nil {
		return nil, fmt.Errorf("event log: tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var t Task
		var started, ended int64
		var rank int
		var components, received string
		if err := rows.Scan(&t.Event, &t.Cursor, &started, &ended, &t.Records, &rank, &t.Platform, &t.Account, &t.Conversation, &components, &received); err != nil {
			return nil, fmt.Errorf("event log: tasks: %w", err)
		}
		t.Started, t.Ended, t.Outcome = time.UnixMilli(started).UTC(), time.UnixMilli(ended).UTC(), outcomeRanks[rank]
		t.Components = []string{}
		if components != "" {
			t.Components = strings.Split(components, ",")
			slices.Sort(t.Components)
		}
		var e otogi.Event
		if json.Unmarshal([]byte(received), &e) == nil {
			t.Action, t.Actor, t.Text = string(e.Action), e.Actor.Name, e.Message.PlainText()
		}
		t.Running = append([]Running{}, running[t.Event]...)
		if len(t.Running) > 0 {
			t.Outcome = "running"
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("event log: tasks: %w", err)
	}
	return out, nil
}
