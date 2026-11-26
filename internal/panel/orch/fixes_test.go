package orch

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/panel/engine"
	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

// The regression tests of the work of the cycle: the freeze when an exit is
// found, the repairs that never ran, the alerts that never went out, and the
// state that was written while it was saved.

// within runs something and fails the test when it does not end in time: a
// deadlock is a test that fails, and not one that hangs for ever.
func within(t *testing.T, limit time.Duration, what string, work func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		work()
	}()
	select {
	case <-done:
	case <-time.After(limit):
		t.Fatalf("%s did not end in %s: the panel is frozen", what, limit)
	}
}

// noted is what the operator was told so far.
func (w *aWorld) noted() []string {
	w.notesMu.Lock()
	defer w.notesMu.Unlock()
	return append([]string{}, w.notes...)
}

// said counts the lines of the log of the panel that hold a piece of text.
func (w *aWorld) said(piece string) int {
	w.store.RLock()
	defer w.store.RUnlock()
	count := 0
	for _, event := range w.store.Events {
		if strings.Contains(event.Message, piece) {
			count++
		}
	}
	return count
}

// notedCount counts the messages to the operator that hold a piece of text.
func (w *aWorld) notedCount(piece string) int {
	count := 0
	for _, note := range w.noted() {
		if strings.Contains(note, piece) {
			count++
		}
	}
	return count
}

// checkExitsNow runs a whole cycle, with the exits asked for again: they are
// only asked for once a minute.
func (w *aWorld) checkExitsNow(t *testing.T) {
	t.Helper()
	w.orch.setLastExits(0)
	within(t, 10*time.Second, "the check cycle", w.orch.CheckAll)
	w.orch.WaitBackground()
}

// reachOf is what the panel knows about the reachability of an exit.
func (w *aWorld) reachOf(target string) map[string]any {
	w.store.RLock()
	defer w.store.RUnlock()
	entry := w.kharej.ExitReach[target]
	out := map[string]any{}
	for key, value := range entry {
		out[key] = value
	}
	return out
}

// TestAForwardedExitDoesNotFreezeThePanel: the check of the exits held the
// lock of the state while it told the operator, and telling the operator takes
// the same lock — the panel froze for good the first time a forwarded exit was
// found.
func TestAForwardedExitDoesNotFreezeThePanel(t *testing.T) {
	w := newWorld(t)
	w.driver.exits = map[int]*engine.Exit{443: {Method: "gost", Targets: []string{"9.9.9.9:80"}}}
	w.driver.reach = map[string]bool{"9.9.9.9:80": true}

	w.checkExitsNow(t)

	if w.said("port 443 on K1 is forwarded to") != 1 {
		t.Errorf("the log does not say where port 443 goes: %v", w.store.Events)
	}
	w.store.RLock()
	hop := w.kharej.Exits["443"]
	w.store.RUnlock()
	if hop == nil {
		t.Fatal("the exit of port 443 was not written in the state")
	}
}

// TestAnExitThatDoesNotAnswerIsSaidOnce: the warning was said again in every
// cycle, with Telegram, for as long as the destination was down; a target seen
// for the first time said it was "reachable again"; and the count of failures
// was lost when the state was read back from the file, which holds every
// number as a float.
func TestAnExitThatDoesNotAnswerIsSaidOnce(t *testing.T) {
	w := newWorld(t)
	w.driver.exits = map[int]*engine.Exit{443: {Method: "gost", Targets: []string{"9.9.9.9:80"}}}

	// the first time the target is seen it answers: it was never down
	w.driver.reach = map[string]bool{"9.9.9.9:80": true}
	w.checkExitsNow(t)
	if n := w.said("reachable again"); n != 0 {
		t.Errorf("a target seen for the first time was said to be reachable again (%d times)", n)
	}
	if got := w.reachOf("9.9.9.9:80")["ok"]; got != true {
		t.Errorf("the reachability of the target is %v, it must be true", got)
	}

	// it stops answering: one failure is not enough (the threshold is two)
	w.driver.reach = map[string]bool{"9.9.9.9:80": false}
	w.checkExitsNow(t)
	if n := w.said("does not answer"); n != 0 {
		t.Errorf("one failure was said (%d times), the threshold is two", n)
	}
	w.checkExitsNow(t)
	if n := w.said("does not answer"); n != 1 {
		t.Fatalf("the failure was said %d times, it must be said once", n)
	}

	// the state is read back from the file: the numbers come back as floats
	w.store.Lock()
	for _, entry := range w.kharej.ExitReach {
		if fails, ok := entry["fails"].(int); ok {
			entry["fails"] = float64(fails)
		}
	}
	for _, hop := range w.kharej.Exits {
		if targets, ok := hop["targets"].([]string); ok {
			list := []any{}
			for _, target := range targets {
				list = append(list, target)
			}
			hop["targets"] = list
		}
	}
	w.store.Unlock()

	for i := 0; i < 4; i++ {
		w.checkExitsNow(t)
	}
	if n := w.said("does not answer"); n != 1 {
		t.Errorf("the failure was said %d times in all, it must be said once however long it lasts", n)
	}
	if n := w.notedCount("does not answer"); n != 1 {
		t.Errorf("the operator was told %d times, it must be once", n)
	}

	// it answers again: that is said once too
	w.driver.reach = map[string]bool{"9.9.9.9:80": true}
	w.checkExitsNow(t)
	w.checkExitsNow(t)
	if n := w.said("reachable again"); n != 1 {
		t.Errorf("the return of the exit was said %d times, it must be said once", n)
	}
	if got := w.reachOf("9.9.9.9:80")["ok"]; got != true {
		t.Errorf("the reachability of the target is %v, it must be true again", got)
	}
}

// TestTheRepairsReallyRun: the cycle reserved a tunnel that was down and then
// asked the repair to start, and the repair, finding the tunnel reserved,
// returned at once: no tunnel was ever repaired by itself.
func TestTheRepairsReallyRun(t *testing.T) {
	w := newWorld(t)
	w.link.Status = model.StatusDown
	w.link.Error = "tunnel not connected"

	w.orch.ScheduleRepairs()
	w.orch.WaitBackground()

	deployed := false
	for _, call := range w.driver.callList() {
		if strings.HasPrefix(call, "deploy:l1:") {
			deployed = true
		}
	}
	if !deployed {
		t.Fatalf("the repair never tried the tunnel: %v", w.driver.callList())
	}
	if w.link.Status != model.StatusUp {
		t.Errorf("the state of the tunnel is %q, it must be up after the repair", w.link.Status)
	}
	if w.orch.Busy("l1") {
		t.Error("the tunnel is still reserved after the repair ended")
	}
}

// TestARepairThatFailsIsNotTriedAgainAtOnce: the backoff.
func TestARepairThatFailsIsNotTriedAgainAtOnce(t *testing.T) {
	w := newWorld(t)
	w.link.Status = model.StatusDown
	w.driver.waitUp = func(link *model.Link) (*engine.Probe, error) {
		return &engine.Probe{OK: false, Error: "nothing answers"}, nil
	}

	w.orch.ScheduleRepairs()
	w.orch.WaitBackground()
	first := len(w.driver.callList())
	if first == 0 {
		t.Fatal("the repair never tried the tunnel")
	}
	if w.link.Status != model.StatusDown || w.link.Error != "all transports failed" {
		t.Errorf("the tunnel is %q (%q), it must be down with the reason", w.link.Status, w.link.Error)
	}

	w.orch.ScheduleRepairs()
	w.orch.WaitBackground()
	if again := len(w.driver.callList()); again != first {
		t.Errorf("the failed repair was tried again at once (%d calls, then %d)", first, again)
	}
}

// TestNothingIsRepairedInObserveMode: the panel only looks.
func TestNothingIsRepairedInObserveMode(t *testing.T) {
	w := newWorld(t)
	w.store.Settings.ObserveOnly = true
	w.link.Status = model.StatusDown
	w.orch.ScheduleRepairs()
	w.orch.WaitBackground()
	if calls := w.driver.callList(); len(calls) != 0 {
		t.Errorf("the observe mode changed something: %v", calls)
	}
}

// TestAFailedRepairIsWrittenToTheTunnelThatIsInThePanel: the give-up path
// changed the tunnel it was given — which can be a copy — without the lock, and
// deployed the tunnel as it was before the tests instead of the one that was
// worked on.
func TestAFailedRepairIsWrittenToTheTunnelThatIsInThePanel(t *testing.T) {
	w := newWorld(t)
	w.link.Status = model.StatusDown
	w.link.ProbePort = 0
	w.driver.waitUp = func(link *model.Link) (*engine.Probe, error) {
		return &engine.Probe{OK: false, Error: "nothing answers"}, nil
	}

	given := w.link.Clone() // not the tunnel of the state: a copy of it
	if w.orch.Repair(given) {
		t.Fatal("the repair succeeded though nothing answers")
	}
	if w.link.Status != model.StatusDown || w.link.Error != "all transports failed" {
		t.Errorf("the tunnel of the panel is %q (%q), it must say that every protocol failed", w.link.Status, w.link.Error)
	}
	if w.link.FailCount < w.store.Settings.FailThreshold {
		t.Errorf("the tunnel of the panel has %d failures, it must have reached the threshold", w.link.FailCount)
	}
	if w.link.Transport == "" {
		t.Error("the tunnel of the panel was left without the fallback protocol")
	}
	if given.Error != "" {
		t.Errorf("the copy that was given was changed (%q), the result belongs to the stored tunnel", given.Error)
	}
	if w.link.ProbePort == 0 {
		t.Error("the tunnel was left without a probe port")
	}
	w.driver.mu.Lock()
	deployed := w.driver.deployed["l1"]
	w.driver.mu.Unlock()
	if deployed != w.link.Transport {
		t.Errorf("the fallback that was deployed is %q, the tunnel says %q", deployed, w.link.Transport)
	}
}

// TestTheProbePortSurvivesAPortEdit: a tunnel without a probe port is given one
// when its Iran side is written again, and the configuration that is written
// carries it — it was written without, and the healthy tunnel read as down.
func TestTheProbePortSurvivesAPortEdit(t *testing.T) {
	w := newWorld(t)
	w.link.ProbePort = 0
	w.link.Managed = false // an adopted tunnel

	w.orch.PortsChanged(w.link)

	if w.link.ProbePort == 0 {
		t.Fatal("the tunnel was given no probe port")
	}
	want := fmt.Sprintf("deploy-iran:l1:probe=%d", w.link.ProbePort)
	for _, call := range w.driver.callList() {
		if call == want {
			return
		}
	}
	t.Errorf("the Iran side was not written with the probe port (%s): %v", want, w.driver.callList())
}

// TestTheOperatorIsToldWhenTheEventAsksForIt: the orchestrator was built with
// no way to talk to the operator, and no alert ever went out. The message is
// sent with the lock of the state released, so the sender may read the state.
func TestTheOperatorIsToldWhenTheEventAsksForIt(t *testing.T) {
	w := newWorld(t)
	var mu sync.Mutex
	got := []string{}
	w.orch.WithNotify(func(text string) {
		// what the Telegram bot does: it reads its settings from the state
		w.store.RLock()
		w.store.RUnlock()
		mu.Lock()
		got = append(got, text)
		mu.Unlock()
	})
	within(t, 5*time.Second, "the notification", func() {
		w.orch.Emit("error", "test", "tunnel <a> & b went down", true, nil)
		w.orch.Emit("info", "test", "only for the log", false, nil)
	})
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("the operator was told %d times, it must be once: %v", len(got), got)
	}
	if got[0] != "🚨 tunnel &lt;a&gt; &amp; b went down" {
		t.Errorf("the operator was told %q", got[0])
	}
	if w.said("only for the log") != 1 {
		t.Error("an event that is not for the operator was not written in the log")
	}
}

// TestTheBotMakesABackupWhenItHasAWayTo: /backup was listed in /help and
// answered "unknown command".
func TestTheBotMakesABackupWhenItHasAWayTo(t *testing.T) {
	w := newWorld(t)
	if got := w.orch.BotCommand("/backup", nil); got != "backups are not configured" {
		t.Errorf("without a way to make one the bot said %q", got)
	}
	w.orch.WithBackup(func() (string, error) { return "backup-1.tmbk was sent <ok>", nil })
	if got := w.orch.BotCommand("/backup", nil); !strings.Contains(got, "backup-1.tmbk was sent &lt;ok&gt;") {
		t.Errorf("the bot said %q, it must say what was made", got)
	}
	w.orch.WithBackup(func() (string, error) { return "", fmt.Errorf("the disk is full") })
	if got := w.orch.BotCommand("/backup", nil); !strings.Contains(got, "the disk is full") {
		t.Errorf("the bot said %q, it must say what went wrong", got)
	}
	w2 := newWorld(t)
	w2.orch.WithBackup(func() (string, error) { return "", nil })
	if got := w2.orch.BotCommand("/backup", nil); strings.Contains(got, "unknown") {
		t.Errorf("the bot did not know /backup: %q", got)
	}
}

// TestTheCycleDoesNotWaitForARepair: a domain that has no tunnel to go to
// asks for the tunnel to be rebuilt, and the rebuild takes minutes — it tests
// the carriers one after the other. The cycle is what checks every tunnel and
// moves every domain: it must go on while the rebuild runs.
func TestTheCycleDoesNotWaitForARepair(t *testing.T) {
	w := newWorld(t)
	w.link.Status = model.StatusDown
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	w.driver.waitUp = func(link *model.Link) (*engine.Probe, error) {
		once.Do(func() { close(started) })
		<-release
		latency := 12.0
		return &engine.Probe{OK: true, Active: "active", LatencyMS: &latency, Verified: true}, nil
	}

	within(t, 5*time.Second, "the evaluation of the domains", w.orch.evaluateDomains)

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the rebuild of the tunnel never started")
	}
	if !w.orch.Busy("l1") {
		t.Error("the tunnel that is being rebuilt is not reserved: the next cycle would start it again")
	}
	// the cycle goes on while the rebuild waits for the servers
	within(t, 5*time.Second, "the cycle", w.orch.CheckAll)

	close(release)
	w.orch.WaitBackground()
	if w.link.Status != model.StatusUp {
		t.Errorf("the state of the tunnel is %q, it must be up after the rebuild", w.link.Status)
	}
	if w.orch.Busy("l1") {
		t.Error("the tunnel is still reserved after the rebuild")
	}
}

// TestAnEvaluationThatIsAskedForManyTimesRunsFewTimes: the repairs that end
// together ask for the domains to be looked at again.
func TestAnEvaluationThatIsAskedForManyTimesRunsFewTimes(t *testing.T) {
	w := newWorld(t)
	for i := 0; i < 50; i++ {
		w.orch.evaluateLater()
	}
	within(t, 5*time.Second, "the evaluations", w.orch.WaitBackground)
	w.orch.mu.Lock()
	idle := !w.orch.evaluating && !w.orch.evalAgain
	w.orch.mu.Unlock()
	if !idle {
		t.Error("the evaluation of the domains was left marked as running")
	}
}

// TestATunnelWhosePortsAreTakenIsNotBuiltInEveryCycle: the port clash of a
// tunnel that is not built yet ran the whole discovery, and sent the alert to
// Telegram, in every cycle.
func TestATunnelWhosePortsAreTakenIsNotBuiltInEveryCycle(t *testing.T) {
	w := newWorld(t)
	w.link.Status = model.StatusPending
	w.link.Built = false
	w.link.Transport = ""
	w.driver.listening = map[int]bool{3080: true}

	for i := 0; i < 3; i++ {
		within(t, 10*time.Second, "the check cycle", w.orch.CheckAll)
		w.orch.WaitBackground()
	}
	if n := w.notedCount("was not built"); n != 1 {
		t.Errorf("the operator was told %d times that the tunnel was not built, it must be once: %v", n, w.noted())
	}
	if w.link.Status != model.StatusPending {
		t.Errorf("the state of the tunnel is %q, it must wait as pending", w.link.Status)
	}
	w.driver.mu.Lock()
	asked := w.driver.listens
	w.driver.mu.Unlock()
	if asked != 1 {
		t.Errorf("the ports of the server were looked at %d times in three cycles, they must be looked at once and then after the backoff", asked)
	}

	// the operator asks for it by hand: the answer is the same, and it is not
	// said a second time
	w.orch.Discover(w.link, nil)
	if n := w.notedCount("was not built"); n != 1 {
		t.Errorf("the same reason was said again by hand: %d times", n)
	}

	// the ports are free now: the next try, after the backoff, builds it
	w.driver.mu.Lock()
	w.driver.listening = map[int]bool{}
	w.driver.mu.Unlock()
	w.orch.clearRepairAfter(w.link.ID)
	if !w.orch.Discover(w.link, nil) {
		t.Error("the tunnel was not built once its ports were free")
	}
}

// aFoundTunnel puts a tunnel that already runs on the servers where the scan
// finds it.
func aFoundTunnel(w *aWorld) {
	w.store.Settings.AutoAdopt = true
	w.store.Links.Remove("l1")
	data := model.NewOrdered()
	data.Set("bind_addr", "0.0.0.0:3080")
	data.Set("transport", "tcpmux")
	data.Set("token", "abc")
	data.Set("ports", []any{"443"})
	w.driver.found["si"] = []*engine.Found{{Name: "old-tunnel", Role: "server", Data: data, Active: "active", Unit: "old-tunnel.service"}}
	client := model.NewOrdered()
	client.Set("remote_addr", "10.0.2.1:3080")
	client.Set("transport", "tcpmux")
	client.Set("token", "abc")
	w.driver.found["sk"] = []*engine.Found{{Name: "old-tunnel", Role: "client", Data: client, Active: "active", Unit: "old-tunnel.service"}}
}

// TestTwoScansAtOnceAdoptATunnelOnce: the monitor, the API and the bot can all
// ask for a scan, and two scans that ran together added the same tunnel twice.
func TestTwoScansAtOnceAdoptATunnelOnce(t *testing.T) {
	w := newWorld(t)
	aFoundTunnel(w)

	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			added := w.orch.ScanAll(nil)
			mu.Lock()
			total += added
			mu.Unlock()
		}()
	}
	wg.Wait()

	if total != 1 {
		t.Errorf("the scans added %d tunnels in all, they must add one", total)
	}
	count := 0
	for _, link := range w.store.Links.List() {
		if link.IranName == "old-tunnel" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("the panel holds %d copies of the tunnel, it must hold one", count)
	}
	if w.store.Servers.Get("si").LastScan == 0 {
		t.Error("the time of the scan was not written on the server")
	}
}

// TestATunnelFoundWithoutATokenGetsTheTokenOfTheEngine: both ends were written
// back with a word the engine does not use, and the two stopped agreeing.
func TestATunnelFoundWithoutATokenGetsTheTokenOfTheEngine(t *testing.T) {
	w := newWorld(t)
	aFoundTunnel(w)
	for _, side := range []string{"si", "sk"} {
		for _, found := range w.driver.found[side] {
			found.Data.Delete("token")
		}
	}
	if added := w.orch.ScanAll(nil); added != 1 {
		t.Fatalf("the scan added %d tunnels, it must add one", added)
	}
	for _, link := range w.store.Links.List() {
		if link.IranName == "old-tunnel" && link.Token != engine.DefaultToken {
			t.Errorf("the token of the tunnel is %q, it must be the default of the engine, %q", link.Token, engine.DefaultToken)
		}
	}
}

// TestTheStateIsSavedWhileTheCycleWritesIt: the cycle, the repairs, the
// discoveries and the moves of the domains write the state while every event
// saves it. A map that is written while it is walked stops the whole program;
// run with the race detector.
func TestTheStateIsSavedWhileTheCycleWritesIt(t *testing.T) {
	w := newWorldAt(t, filepath.Join(t.TempDir(), "state.json"))
	w.link.Status = model.StatusDown
	iran2 := &model.Server{ID: "si2", Name: "I2", Role: model.Iran, IP: "10.0.2.2", Status: model.Online, Enabled: true}
	w.store.Servers.Put(iran2.ID, iran2)
	w.driver.exits = map[int]*engine.Exit{443: {Method: "gost", Targets: []string{"9.9.9.9:80"}}}
	w.driver.reach = map[string]bool{"9.9.9.9:80": false}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	loop := func(work func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				work()
			}
		}()
	}
	loop(func() {
		w.orch.setLastExits(0)
		w.orch.CheckAll()
	})
	loop(func() { w.orch.Discover(w.link, nil) })
	loop(func() { w.orch.Repair(w.link) })
	loop(func() { w.orch.ScanAll(nil) })
	loop(func() { w.store.Save() })
	target := w.iran.ID
	loop(func() {
		w.orch.MoveDomain(w.domain, target, "the operator asked for it", false)
		if target == w.iran.ID {
			target = iran2.ID
		} else {
			target = w.iran.ID
		}
	})
	time.Sleep(600 * time.Millisecond)
	close(stop)
	wg.Wait()
	w.orch.WaitBackground()
}

// TestATestWithoutADriverIsAnAnswerAndNotACrash: the panel built without a way
// to reach the servers dereferenced the nil result of the test.
func TestATestWithoutADriverIsAnAnswerAndNotACrash(t *testing.T) {
	w := newWorld(t)
	w.orch.WithDriver(nil)
	result := w.orch.try(w.link, "tcp", []string{})
	if result == nil || result.OK || result.Error == "" {
		t.Errorf("the test said %+v, it must fail with a reason", result)
	}
}
