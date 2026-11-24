package chat

import (
	"slices"
	"time"
)

// How long a batch waits for its sender, from replays of Eku's groups
// (2026-11-19 and 11-20). These are rules, not settings: a deployment sets
// judge.wait's min and max.
const (
	// rhythmGap: two messages further apart are two turns, not typing.
	rhythmGap = time.Minute
	// rhythmFloor: nobody types faster than a line every few seconds.
	rhythmFloor = 3 * time.Second
	// rhythmDefault is the rhythm with no run of messages to go by.
	rhythmDefault = 10 * time.Second
	// rhythmLines is how many recent messages the rhythm is read from.
	rhythmLines = 30
	// unfinishedPace: a message its sender has not finished waits up to
	// this many rhythms more, the less sure it is finished the longer.
	unfinishedPace = 1.5
)

// rhythm is how fast who types: the median of the last gaps between their
// consecutive messages, with nobody else's in between, the agent's included,
// and under rhythmGap apart. Without any run of theirs it is others';
// without any, rhythmDefault. A back-and-forth is no rhythm: it is waiting
// for an answer, and once made 61.5 seconds a line on Eku (11-20). So a gap
// in which an agent started replying is none either: replied is when agents
// of the chat started replies. Their messages in lines do not
// tell it, as one asked while the reply is written comes before it, and QQ
// groups do not deliver them at all. lines is the chat's recent messages,
// oldest first.
func rhythm(lines []line, replied []time.Time, who string) time.Duration {
	if len(lines) > rhythmLines {
		lines = lines[len(lines)-rhythmLines:]
	}
	var mine, all []time.Duration
	for i := 1; i < len(lines); i++ {
		a, b := lines[i-1], lines[i]
		if a.Outgoing || b.Outgoing || a.ActorID != b.ActorID || repliedBetween(replied, a.At, b.At) {
			continue
		}
		if d := b.At.Sub(a.At); d >= 0 && d <= rhythmGap {
			all = append(all, d)
			if b.ActorID == who {
				mine = append(mine, d)
			}
		}
	}
	switch {
	case len(mine) > 0:
		return max(rhythmFloor, median(mine, 5))
	case len(all) > 0:
		return max(rhythmFloor, median(all, 10))
	}
	return rhythmDefault
}

// repliedBetween reports whether a reply in replied started after from and
// by to.
func repliedBetween(replied []time.Time, from, to time.Time) bool {
	return slices.ContainsFunc(replied, func(t time.Time) bool { return t.After(from) && !t.After(to) })
}

// median is the median of the last n of ds.
func median(ds []time.Duration, n int) time.Duration {
	if len(ds) > n {
		ds = ds[len(ds)-n:]
	}
	s := slices.Clone(ds)
	slices.Sort(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

// waitFor is how long to wait after a message: least when its sender has
// finished, up to unfinishedPace rhythms more the less sure that is, and
// half of that when it is not known.
func waitFor(least time.Duration, done float64, known bool, r time.Duration) time.Duration {
	unfinished := 0.5
	if known {
		unfinished = 1 - min(1, max(0, done))
	}
	return least + time.Duration(unfinished*unfinishedPace*float64(r))
}
