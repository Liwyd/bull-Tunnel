package tunnel

import (
	"context"
	"fmt"

	"log/slog"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Liwyd/Bull-Tunn/engine/internal/carrier"
	"github.com/Liwyd/Bull-Tunn/engine/internal/config"
	"github.com/Liwyd/Bull-Tunn/engine/internal/metrics"
	"github.com/Liwyd/Bull-Tunn/engine/internal/proto"
)

// Client is the side that dials out.
type Client struct {
	cfg     *config.Client
	log     *slog.Logger
	carrier carrier.Carrier
	metrics *metrics.Writer
	started time.Time

	connected atomic.Int64
	rx        atomic.Uint64
	tx        atomic.Uint64
	streams   atomic.Int64
	current   atomic.Pointer[proto.Session]
	lastError atomic.Value
}

// NewClient builds the client side.
func NewClient(cfg *config.Client, log *slog.Logger, mw *metrics.Writer) (*Client, error) {
	c, err := carrier.For(cfg.Transport)
	if err != nil {
		return nil, err
	}
	cl := &Client{
		cfg:     cfg,
		log:     log,
		carrier: c,
		metrics: mw,
		started: time.Now(),
	}
	cl.lastError.Store("")
	return cl, nil
}

// Run keeps the sessions open until the context is done.
func (c *Client) Run(ctx context.Context) error {
	pool := c.cfg.ConnectionPool
	if pool < 1 {
		pool = 1
	}
	var wg sync.WaitGroup
	for i := 0; i < pool; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			c.sessionLoop(ctx, n)
		}(i)
	}
	go c.writeMetrics(ctx)
	<-ctx.Done()
	wg.Wait()
	return nil
}

// sessionLoop keeps one session open, reconnecting when it breaks.
func (c *Client) sessionLoop(ctx context.Context, n int) {
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		err := c.runSession(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			c.lastError.Store(err.Error())
			c.log.Warn("the session is down", "session", n, "error", err)
		}
		// a random wait keeps the sessions of a pool from reconnecting in
		// the same instant
		wait := backoff + time.Duration(rand.Int63n(int64(backoff/2)+1))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// runSession opens one session and serves it until it ends.
func (c *Client) runSession(ctx context.Context) error {
	opts := carrier.Options{
		Path:          c.cfg.Path,
		StealthPrefix: c.cfg.StealthPrefix,
		TLSCertFile:   c.cfg.TLSCertFile,
		TLSKeyFile:    c.cfg.TLSKeyFile,
		TLSServerName: c.cfg.TLSServerName,
	}
	conn, err := c.carrier.Dial(c.cfg.RemoteAddr, opts)
	if err != nil {
		return fmt.Errorf("dial %s: %w", c.cfg.RemoteAddr, err)
	}
	defer conn.Close()
	sopts := proto.SessionOptions{MaxStreams: c.cfg.MuxStreams, Heartbeat: c.cfg.Heartbeat}
	sess, err := proto.ClientHandshake(conn, c.cfg.Token, sopts)
	if err != nil {
		return fmt.Errorf("handshake: %w", err)
	}
	c.connected.Add(1)
	c.lastError.Store("")
	c.current.Store(sess)
	c.log.Info("connected", "remote", c.cfg.RemoteAddr, "transport", c.cfg.Transport)
	defer func() {
		c.current.Store(nil)
		c.connected.Add(-1)
		sess.Close()
		c.log.Info("disconnected", "remote", c.cfg.RemoteAddr)
	}()

	go c.serveOpens(ctx, sess)
	<-sess.Done()
	if err := sess.Err(); err != nil {
		return err
	}
	return nil
}

// serveOpens takes the streams the server asks for.
func (c *Client) serveOpens(ctx context.Context, sess *proto.Session) {
	for {
		req, err := sess.AcceptOpen()
		if err != nil {
			return
		}
		go c.handleOpen(ctx, sess, req)
	}
}

// handleOpen connects to the destination the server asked for and pipes it.
func (c *Client) handleOpen(ctx context.Context, sess *proto.Session, req *proto.OpenRequest) {
	st := req.Stream
	defer st.Close()
	var conn net.Conn
	var err error
	if req.Mode == proto.ModeUDP {
		conn, err = net.Dial("udp", req.Dest)
	} else {
		conn, err = net.DialTimeout("tcp", req.Dest, 15*time.Second)
	}
	if err != nil {
		sess.ReplyOpen(req.ID, err)
		return
	}
	defer conn.Close()
	if err := sess.ReplyOpen(req.ID, nil); err != nil {
		return
	}
	if req.Mode == proto.ModeUDP {
		c.pipeUDP(st, conn)
		return
	}
	pipe(conn, st)
}

// pipeUDP moves the datagrams in both directions.
func (c *Client) pipeUDP(st *proto.Stream, conn net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		buf := make([]byte, 64*1024)
		for {
			conn.SetReadDeadline(time.Now().Add(120 * time.Second))
			n, err := conn.Read(buf)
			if err != nil {
				return
			}
			payload := make([]byte, n)
			copy(payload, buf[:n])
			if _, err := st.Write(payload); err != nil {
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		buf := make([]byte, 64*1024)
		for {
			n, err := st.Read(buf)
			if err != nil {
				return
			}
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := conn.Write(buf[:n]); err != nil {
				return
			}
		}
	}()
	wg.Wait()
}

// writeMetrics keeps the metrics file up to date.
func (c *Client) writeMetrics(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			var rx, tx uint64
			var streams int64
			if sess := c.current.Load(); sess != nil {
				rx, tx, streams = sess.Stats()
			}
			snap := metrics.Snapshot{
				Connected:  c.connected.Load() > 0,
				Transports: config.Transports(),
				Uptime:     time.Since(c.started).Seconds(),
				RxBytes:    rx,
				TxBytes:    tx,
				Sessions:   int(c.connected.Load()),
				Streams:    streams,
				LastError:  c.lastError.Load().(string),
			}
			if err := c.metrics.Write(snap); err != nil {
				c.log.Warn("the metrics file could not be written", "error", err)
			}
		}
	}
}
