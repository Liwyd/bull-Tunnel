package api

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/panel/backup"
)

// The backup of the night. The settings have always said "send a full backup
// to Telegram every night" (a switch, a time, a zone and how many copies to
// keep), and nothing ever read them: this is the clock that does.

// retryAfter is how long the panel waits before it tries a backup again that
// could not be made (a full disk, a folder it may not write): trying every
// minute would only fill the log of events with the same line.
const retryAfter = time.Hour

// RunBackups looks at the clock once a minute, and makes the backup of the
// night when it is due: one a day, at the time of the settings, written in
// the folder of the backups, sent to Telegram when it is set up, and the
// folder kept to the number of copies the settings allow. It returns when
// stop is closed.
func (s *Server) RunBackups(stop <-chan struct{}) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case at := <-ticker.C:
			s.nightlyBackup(at)
		}
	}
}

// nightlyBackup makes the backup of the night if it is due at this moment,
// and says whether it did.
func (s *Server) nightlyBackup(at time.Time) bool {
	if s.backupsStore == nil {
		return false
	}
	s.store.RLock()
	settings := s.store.Settings
	enabled, when, zone, keep := settings.BackupEnabled, settings.BackupTime, settings.BackupTZ, settings.BackupKeep
	last, _ := s.store.Meta["last_nightly"].(float64)
	s.store.RUnlock()

	lastAt := time.Time{}
	if last > 0 {
		lastAt = time.Unix(int64(last), 0)
	}
	if !backup.Due(enabled, when, zone, at, lastAt) {
		return false
	}
	if !s.lastTry.IsZero() && at.Sub(s.lastTry) < retryAfter {
		return false // it was tried a moment ago and did not work: not again yet
	}
	s.lastTry = at

	path, blob, err := s.writeBackup("nightly")
	if err != nil {
		s.orch.Emit("error", "backup", "The backup of the night could not be made: "+err.Error(), true, nil)
		return false
	}
	// the night is made: it is written down before anything else can fail,
	// so a Telegram that is away does not make the panel try the whole backup
	// again every hour
	s.store.Lock()
	s.store.Meta["last_nightly"] = float64(at.UnixNano()) / 1e9
	s.store.Unlock()
	s.store.Save()

	file := filepath.Base(path)
	message := "The backup of the night was made (" + file + ")"
	level := "success"
	sent, configured, sendErr := s.sendBackup(file, blob)
	switch {
	case !configured:
		message += "; Telegram is not set up, so it stays on this server"
	case sendErr != nil:
		level = "warning"
		message += fmt.Sprintf("; it was not sent to Telegram: %v", sendErr)
	default:
		message += fmt.Sprintf("; sent to Telegram (%d)", sent)
	}
	s.backupsStore.Prune(keep)
	s.orch.Emit(level, "backup", message, level == "warning", nil)
	return true
}
