package transport

import (
	"context"
	"errors"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/engine/controlwire"
	"github.com/Liwyd/bull-Tunnel/internal/engine/utils"
	"github.com/sirupsen/logrus"
)

// controlLoop serves one generation's control channel on the server side:
// it beats the heartbeat, asks the client for a tunnel connection whenever the
// forwarder queues a user, listens for the client's goodbye, and says the
// server's own goodbye when the generation ends.
//
// All seven transports had their own copy of this loop, and the copies had
// drifted: two read one byte and stopped listening, the readers of the others
// could block forever on a full channel once their loop had returned, the
// websocket pair dropped the tunnel on a signal it did not know, and every copy
// wrote to whatever control channel the transport held at the moment of
// writing — which, for a goroutine running late, is the next generation's. The
// loop is here once now, bound to the connection it was started with, and a
// transport chooses only how the signals travel (link, see controlwire),
// whether to measure the round trip, and how long its goodbye needs
// (farewellFlush).
type controlLoop struct {
	ctx      context.Context
	link     controlwire.Link
	beat     time.Duration
	requests <-chan struct{}
	log      *logrus.Logger
	// restart ends this generation and starts the next; called in its own
	// goroutine, since it waits for this one to finish.
	restart func()

	// probeRTT sends SG_RTT once the loop starts and logs the round trip
	// when the client echoes it.
	probeRTT bool
	// farewellFlush is how long the goodbye is given to leave before the
	// channel is closed, for the transports whose write only queues it.
	farewellFlush time.Duration
	// bye, when set, is said on every way out; see farewell.
	bye *farewell

	// silence is how long a client that answers heartbeats may say nothing
	// before it is presumed gone. Zero means controlSilence(beat); a test sets
	// it to something it can wait for.
	silence time.Duration

	// onAnswers, when set, is called once, the first time the client answers a
	// heartbeat. It is how a transport learns the client is of a release that
	// answers them — which is also what tells it the client understands
	// whatever else that release added (see udpRepeats).
	onAnswers func()
}

// controlSilenceFloor is the shortest silence the server will call a dead
// client, whatever the heartbeat is.
const controlSilenceFloor = 30 * time.Second

// controlSilence is how long a client that has shown it answers heartbeats may
// go without sending anything: three beats, never under controlSilenceFloor.
//
// This is the server's half of what the client has always done (beatClock):
// the client presumes the server gone after three missed beats, and the server
// had no equivalent. A client that vanished without a FIN or RST — a machine
// that lost power, a path that blackholes, a NAT that dropped the mapping —
// left the control read blocked for as long as the kernel took to give up on
// the socket (about fifteen minutes on TCP, effectively never on KCP or QUIC,
// where there is no kernel connection to give up), and for all that time the
// tunnel said "Connected" while no one was there. The client's fallback chain
// meanwhile had moved on to another carrier the server was not listening on.
//
// The same three beats the client uses is also what makes this safe: a
// healthy path delivers a heartbeat answer every beat, so three missed ones is
// the path or the client being gone, and the client's own deadline expires at
// about the same moment.
func controlSilence(beat time.Duration) time.Duration {
	return max(3*livenessBeat(beat), controlSilenceFloor)
}

// run serves the channel until the generation ends or the channel fails; a
// failure asks for a restart.
func (c controlLoop) run() {
	stop := context.AfterFunc(c.ctx, func() {
		time.AfterFunc(c.farewellFlush+100*time.Millisecond, c.link.Close)
	})
	defer stop()
	defer c.link.Close()
	if c.bye != nil {
		defer c.bye.said()
	}
	done := make(chan struct{})
	defer close(done)

	ticker := newLivenessTicker(c.beat)
	defer ticker.Stop()

	signals := make(chan byte, 1)
	go c.read(signals, done)

	var probed time.Time
	if c.probeRTT {
		if err := c.link.Send(utils.SG_RTT); err != nil {
			c.log.Errorf("failed to send the RTT probe: %v", err)
			go c.restart()
			return
		}
		probed = time.Now()
	}

	for {
		select {
		case <-c.ctx.Done():
			c.farewell()
			return

		case <-c.requests:
			if err := c.link.Send(utils.SG_Chan); err != nil {
				c.log.Errorf("failed to ask the client for a connection: %v", err)
				go c.restart()
				return
			}

		case <-ticker.C:
			if err := c.link.Send(utils.SG_HB); err != nil {
				c.log.Errorf("failed to send the heartbeat: %v", err)
				go c.restart()
				return
			}
			c.log.Trace("heartbeat signal sent successfully")

		case signal := <-signals:
			switch signal {
			case utils.SG_Closed:
				c.log.Warn("control channel has been closed by the client")
				go c.restart()
				return
			case utils.SG_HB:
				c.log.Trace("heartbeat signal received successfully")
			case utils.SG_RTT:
				if !probed.IsZero() {
					c.log.Infof("Round Trip Time (RTT): %d ms", time.Since(probed).Milliseconds())
					probed = time.Time{}
				}
			default:
				// A client newer than this server may say things this one has
				// no word for; that is no reason to drop its tunnel.
				c.log.Warnf("ignoring an unknown control signal %d", signal)
			}
		}
	}
}

// farewell tells the client this generation is over — SG_Closed is what lets
// it redial at once instead of waiting out its deadline — and closes the
// channel.
//
// Over a datagram transport the write only hands the signal to a sender that
// flushes on its own schedule, and closing straight after drops the very
// signal the close was meant to follow; farewellFlush is the pause between.
// That was measured, not guessed: with the close right behind the write the
// KCP client still waited out its full deadline.
func (c controlLoop) farewell() {
	if c.link.Send(utils.SG_Closed) == nil && c.farewellFlush > 0 {
		time.Sleep(c.farewellFlush)
	}
	c.link.Close()
}

// read hands every signal the client sends to the loop until the channel
// fails or the loop has returned.
func (c controlLoop) read(signals chan<- byte, done <-chan struct{}) {
	silence := c.silence
	if silence <= 0 {
		silence = controlSilence(c.beat)
	}
	// answers is set when the client first answers a heartbeat, and only then
	// does a silence mean anything.
	//
	// Whether a client answers is not announced anywhere: websocket clients
	// always did, every client of this release does once the server has shown it
	// listens, and the rest — older clients, on the stream and datagram
	// carriers — send nothing at all, and are as quiet when healthy as when
	// dead. So the first answer is the negotiation. A client that never sends
	// one keeps exactly the behaviour it had, with no deadline, and one that
	// does is held to the silence above from then on. Nothing is added to the
	// wire and nothing about an old client changes.
	answers := false
	for {
		if answers {
			// Re-armed before every read, so the deadline is "silence since the
			// last thing the client said" and not since the channel opened.
			_ = c.link.SetReadDeadline(time.Now().Add(silence))
		}
		signal, err := c.link.Receive()
		if errors.Is(err, controlwire.ErrNoSignal) {
			c.log.Warnf("ignoring a malformed control frame: %v", err)
			continue
		}
		if err != nil {
			// A generation that has already ended — or whose loop has already
			// asked for the restart — must not ask again: every goroutine
			// dying in a teardown used to queue one more restart of a tunnel
			// that was on its way down.
			select {
			case <-done:
				return
			default:
			}
			if c.ctx.Err() == nil {
				var timeout interface{ Timeout() bool }
				if answers && errors.As(err, &timeout) && timeout.Timeout() {
					c.log.Warnf("the client has sent nothing for %s although it answers heartbeats; "+
						"presuming it gone and freeing the seat for the next one", silence.Round(time.Second))
				} else {
					c.log.Errorf("failed to read from the control channel: %v", err)
				}
				go c.restart()
			}
			return
		}
		if signal == utils.SG_HB && !answers {
			answers = true
			if c.onAnswers != nil {
				c.onAnswers()
			}
		}
		select {
		case signals <- signal:
		case <-done:
			return
		}
	}
}
