package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

func measuredHeartbeat(user, device, report, batch string, from, until time.Time) heartbeat {
	h := heartbeat{User: user, DeviceID: device, HeartbeatID: report, SessionState: "active", Usage: &usageBatch{ID: batch}}
	if until.After(from) {
		h.Usage.Intervals = []usageInterval{{Start: from, End: until}}
	}
	return h
}

func TestUsageReconciliationChargesOnceAcrossRetriesAndSessionChanges(t *testing.T) {
	s := testApplication(t).service
	s.location = time.UTC
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	if err := s.setUserQuota("child", 1800); err != nil {
		t.Fatal(err)
	}
	initial := measuredHeartbeat("child", "pc", "start", "batch-start", now, now)
	if _, err := s.recordHeartbeat(initial); err != nil {
		t.Fatal(err)
	}
	start := now
	now = now.Add(10 * time.Minute)
	h := measuredHeartbeat("child", "pc", "report", "batch-offline", start, now)
	h.Usage.RemainingSeconds, h.Usage.QuotaSeconds = 1200, 1800
	for _, retry := range []string{"report", "report", "new-report-after-lock"} {
		h.HeartbeatID = retry
		got, err := s.recordHeartbeat(h)
		if err != nil || got.DailyTotalSeconds != 600 || got.RemainingSeconds != 1200 || got.UsageAck != "batch-offline" {
			t.Fatalf("retry %s: result=%+v err=%v", retry, got, err)
		}
		h.SessionState = "locked"
	}
	// Receipt intervals for measured clients must not add a second charge.
	now = now.Add(30 * time.Second)
	got, err := s.recordHeartbeat(measuredHeartbeat("child", "pc", "poll", "empty-batch", now, now))
	if err != nil || got.DailyTotalSeconds != 600 {
		t.Fatalf("receipt interval charged again: %+v %v", got, err)
	}
	if err := s.repository.deleteUser("child"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"usage_batches", "usage_intervals"} {
		var count int
		if err := s.repository.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("delete retained %s: %d %v", table, count, err)
		}
	}
}

func TestUsageReconciliationCombinesDevicesAndCurrentParentRules(t *testing.T) {
	s := testApplication(t).service
	s.location = time.UTC
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	if err := s.setUserQuota("child", 1800); err != nil {
		t.Fatal(err)
	}
	start := now
	now = now.Add(10 * time.Minute)
	a := measuredHeartbeat("child", "A", "a", "same-id-on-each-device", start, now)
	a.Usage.RemainingSeconds, a.Usage.QuotaSeconds = 1200, 1800
	if _, err := s.recordHeartbeat(a); err != nil {
		t.Fatal(err)
	}
	b := measuredHeartbeat("child", "B", "b", "same-id-on-each-device", start, start.Add(5*time.Minute))
	b.Usage.RemainingSeconds, b.Usage.QuotaSeconds = 1500, 1800
	got, err := s.recordHeartbeat(b)
	if err != nil || got.DailyTotalSeconds != 900 || got.RemainingSeconds != 900 {
		t.Fatalf("one device overwrote the other's use: %+v %v", got, err)
	}
	if err := s.addBonusMinutes("child", 5); err != nil {
		t.Fatal(err)
	}
	if err := s.setUserPaused("child", true); err != nil {
		t.Fatal(err)
	}
	got, err = s.recordHeartbeat(b)
	if err != nil || got.DailyTotalSeconds != 900 || got.RemainingSeconds != 1200 || got.Action != "lock" || got.Reason != "paused" {
		t.Fatalf("reconnect ignored current parent rules: %+v %v", got, err)
	}
}

func TestUsageIntervalsKeepOriginalDaysAndHourlyTotals(t *testing.T) {
	location := osloLocation(t)
	for _, start := range []time.Time{
		time.Date(2026, 10, 28, 23, 59, 40, 0, location),
		time.Date(2026, 3, 29, 0, 59, 45, 0, time.UTC),
		time.Date(2026, 10, 25, 0, 59, 45, 0, time.UTC),
	} {
		t.Run(start.String(), func(t *testing.T) {
			s := testApplication(t).service
			s.location = location
			until := start.Add(30 * time.Second)
			now := until.AddDate(0, 0, 2) // Delayed upload, not the accounting date.
			s.now = func() time.Time { return now }
			if _, err := s.recordHeartbeat(measuredHeartbeat("child", "pc", "report", "offline", start, until)); err != nil {
				t.Fatal(err)
			}
			history, err := s.repository.heartbeatsForUser("child")
			if err != nil {
				t.Fatal(err)
			}
			total := 0
			firstDate := start.In(location).Format("2006-01-02")
			for _, date := range []string{firstDate, start.In(location).AddDate(0, 0, 1).Format("2006-01-02")} {
				daily, err := s.dailyTotal("child", date)
				if err != nil {
					t.Fatal(err)
				}
				hours, err := calculateHourlyUsage(history, date, location)
				if err != nil {
					t.Fatal(err)
				}
				var hourly time.Duration
				for _, hour := range hours {
					hourly += hour.Duration
				}
				if int(hourly/time.Second) != daily {
					t.Fatalf("hour/day mismatch: %s %v/%d", date, hourly, daily)
				}
				total += daily
			}
			if total != 30 {
				t.Fatalf("lost or duplicated delayed usage: %d", total)
			}
		})
	}
}

func TestUsageBatchCannotChangeAfterAcknowledgement(t *testing.T) {
	s := testApplication(t).service
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	h := measuredHeartbeat("child", "pc", "report", "batch", now.Add(-time.Minute), now)
	if _, err := s.recordHeartbeat(h); err != nil {
		t.Fatal(err)
	}
	h.Usage.Intervals[0].Start = now.Add(-30 * time.Second)
	for _, id := range []string{"report", "changed-heartbeat"} {
		h.HeartbeatID = id
		if _, err := s.recordHeartbeat(h); !errors.Is(err, errInvalidHeartbeat) {
			t.Fatalf("changed batch accepted under %s: %v", id, err)
		}
	}
}

func TestUsageProtocolOnAuthenticatedHeartbeatRoute(t *testing.T) {
	a := securedApplication(t)
	d := enrolledDevice(t, a, "child", "pc")
	now := a.service.now()
	h := measuredHeartbeat(d.User, d.DeviceID, "report", "batch", now.Add(-time.Minute), now)
	encoded, _ := json.Marshal(h)
	w := serve(a, deviceRequest(d.Token, string(encoded)))
	var got response
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK || got.UsageAck != "batch" || got.DailyTotalSeconds != 60 {
		t.Fatalf("route rejected protocol: %d %+v %v", w.Code, got, err)
	}
}

func TestLegacyRestQuotaSetsFloorWithoutRechargingKnownHistory(t *testing.T) {
	s := testApplication(t).service
	s.location = time.UTC
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	if err := s.setUserQuota("child", 1800); err != nil {
		t.Fatal(err)
	}
	for _, batch := range []string{"legacy-a", "legacy-b"} {
		h := measuredHeartbeat("child", "pc", batch, batch, now, now)
		h.Usage.Legacy, h.Usage.PolicyDate = true, "2026-10-28"
		h.Usage.QuotaSeconds, h.Usage.RemainingSeconds = 1800, 1200
		got, err := s.recordHeartbeat(h)
		if err != nil || got.DailyTotalSeconds != 600 || got.RemainingSeconds != 1200 {
			t.Fatalf("legacy baseline restored or doubled time: %+v %v", got, err)
		}
	}
}

func TestReconnectionKeepsFifteenOfTwentyFiveMinutes(t *testing.T) {
	s := testApplication(t).service
	s.location = time.UTC
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	if err := s.setUserQuota("child", 1800); err != nil {
		t.Fatal(err)
	}
	bootstrap := measuredHeartbeat("child", "pc", "upgrade", "upgrade", now, now)
	bootstrap.Usage.Legacy, bootstrap.Usage.PolicyDate = true, "2026-10-28"
	bootstrap.Usage.QuotaSeconds, bootstrap.Usage.RemainingSeconds = 1800, 1500
	if got, err := s.recordHeartbeat(bootstrap); err != nil || got.RemainingSeconds != 1500 {
		t.Fatalf("saved rest quota not imported: %+v %v", got, err)
	}
	start := now
	now = now.Add(10 * time.Minute)
	h := measuredHeartbeat("child", "pc", "reconnect", "offline-use", start, now)
	h.Usage.QuotaSeconds, h.Usage.RemainingSeconds = 1800, 900
	got, err := s.recordHeartbeat(h)
	if err != nil || got.RemainingSeconds != 900 || got.DailyTotalSeconds != 900 {
		t.Fatalf("25 minutes minus 10 must leave 15: %+v %v", got, err)
	}
}

func TestUsageAndHeartbeatCommitTogether(t *testing.T) {
	s := testApplication(t).service
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	if _, err := s.repository.db.Exec(`CREATE TRIGGER fail_heartbeat BEFORE INSERT ON heartbeats BEGIN SELECT RAISE(ABORT, 'simulated write failure'); END`); err != nil {
		t.Fatal(err)
	}
	h := measuredHeartbeat("child", "pc", "report", "batch", now.Add(-time.Minute), now)
	if _, err := s.recordHeartbeat(h); err == nil {
		t.Fatal("simulated heartbeat failure was ignored")
	}
	for _, table := range []string{"usage_batches", "usage_intervals", "user_presence"} {
		var count int
		if err := s.repository.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("failed transaction retained %s: %d %v", table, count, err)
		}
	}
	if _, err := s.repository.db.Exec(`DROP TRIGGER fail_heartbeat`); err != nil {
		t.Fatal(err)
	}
	got, err := s.recordHeartbeat(h)
	if err != nil || got.DailyTotalSeconds != 60 || got.UsageAck != "batch" {
		t.Fatalf("retry after rollback lost usage: %+v %v", got, err)
	}
}

func TestUsageRejectsImpossibleIntervalsBeforeWriting(t *testing.T) {
	s := testApplication(t).service
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	for _, interval := range []usageInterval{
		{Start: now, End: now.Add(-time.Second)},
		{Start: now.Add(-2 * time.Hour), End: now},
		{Start: now.Add(time.Hour), End: now.Add(time.Hour + time.Second)},
	} {
		h := measuredHeartbeat("child", "pc", "report", "batch", now, now)
		h.Usage.Intervals = []usageInterval{interval}
		if _, err := s.recordHeartbeat(h); !errors.Is(err, errInvalidHeartbeat) {
			t.Fatalf("invalid interval accepted: %v", err)
		}
	}
	var count int
	if err := s.repository.db.QueryRow(`SELECT COUNT(*) FROM usage_batches`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid requests changed accounting: %d %v", count, err)
	}
}

func TestConcurrentUsageBatchesAreChargedOnce(t *testing.T) {
	s := testApplication(t).service
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	var workers sync.WaitGroup
	errors := make(chan error, 12)
	for i := 0; i < 12; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			h := measuredHeartbeat("child", "pc", fmt.Sprintf("request-%d", i), "same-batch", now.Add(-time.Minute), now)
			_, err := s.recordHeartbeat(h)
			errors <- err
		}(i)
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	total, err := s.dailyTotal("child", now.In(s.location).Format("2006-01-02"))
	if err != nil || total != 60 {
		t.Fatalf("concurrent retries charged %d seconds: %v", total, err)
	}
}

func TestUsageMigrationPreservesExistingHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	r, err := openRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.close()
	// Only this disposable database is downgraded to the pre-feature schema.
	if err := goose.DownTo(r.db, "migrations", 6); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	if _, err := r.db.Exec(`INSERT INTO heartbeats(reported_at,reported_unix,accounting_unix,device_id,user,heartbeat_id,credited_milliseconds,credited_until,session_state) VALUES(?,?,?,?,?,?,?,?,?)`, now.Format(time.RFC3339Nano), now.Unix(), now.Unix(), "pc", "child", "legacy", 30000, now.Format(time.RFC3339Nano), "active"); err != nil {
		t.Fatal(err)
	}
	r.close()
	upgraded, err := openRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.close()
	s := newScreenTimeService(upgraded)
	s.location = time.UTC
	s.now = func() time.Time { return now.Add(time.Minute) }
	got, err := s.recordHeartbeat(measuredHeartbeat("child", "pc", "modern", "modern-batch", now, now.Add(30*time.Second)))
	if err != nil || got.DailyTotalSeconds != 60 {
		t.Fatalf("migration lost legacy history or doubled the new interval: %+v %v", got, err)
	}
}
