package groovynet

import "time"

// paceBatch is how many datagrams SendPayload sends back-to-back between
// pacing waits (~47 KB). The average rate is unchanged from per-datagram
// pacing; only the burst shape changes, and 47 KB bursts are far gentler
// than the reference sender (MiSTerCast), which sends a whole field at line
// rate with no pacing at all.
const paceBatch = 32

// pacer spaces a payload's datagrams to an average of one per interval.
// Per-datagram waits (~20 µs) are far below what a sleep can hit, so
// pacing used to busy-wait for the whole paced send: ~7 ms of a core per
// RAW field. Waiting once per batch makes the wait long enough to sleep
// most of it; only the last margin is spun, so an oversleeping timer
// cannot push the batch late. Deadlines are cumulative from the payload
// start, so a late wake-up shortens the next wait instead of accumulating.
type pacer struct {
	interval time.Duration // per-datagram budget; 0 disables pacing
	batch    int           // datagrams per burst
	margin   time.Duration // tail of each wait that is spun, not slept
	now      func() time.Time
	sleep    func(time.Duration)
}

// afterDatagram is called after the sent-th datagram (1-based) of a
// total-datagram payload whose first datagram went out at start, when more
// datagrams follow. At a batch boundary it blocks until the next datagram's
// deadline, start + sent*interval. The wait before the final batch is spun,
// never slept: no later batch could absorb an oversleep there, so it would
// land directly on the send's end time (+0.5 ms per field under WSL2).
func (p *pacer) afterDatagram(start time.Time, sent, total int) {
	if p.interval <= 0 || p.batch <= 0 || sent%p.batch != 0 {
		return
	}
	deadline := start.Add(time.Duration(sent) * p.interval)
	remaining := deadline.Sub(p.now())
	if remaining <= 0 {
		return
	}
	if remaining > p.margin && total-sent > p.batch {
		p.sleep(remaining - p.margin)
	}
	for p.now().Before(deadline) {
		// Spin the final margin: sub-ms sleeps overshoot by 50 µs (Linux)
		// to ~1 ms (Windows), so the tail is only safe to busy-wait.
	}
}
