package store

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDocSnapshots(t *testing.T) {
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			u := &User{Username: "reader", Role: RoleUser}
			if err := s.CreateUser(ctx, u); err != nil {
				t.Fatal(err)
			}
			c := &Connection{Name: "db", DBType: "sqlite"}
			if err := s.CreateConnection(ctx, c); err != nil {
				t.Fatal(err)
			}
			k := DocKey{ConnectionID: c.ID, UserID: u.ID, Schema: "main"}
			if err := s.EnsureDoc(ctx, k); err != nil {
				t.Fatal(err)
			}
			if err := s.EnsureDoc(ctx, k); err != nil {
				t.Fatal(err)
			}
			past := time.Now().Add(-time.Minute).UnixMilli()
			if err := s.SetDocSchedule(ctx, k, "@hourly", past); err != nil {
				t.Fatal(err)
			}
			// Simulate competing replicas; exactly one may scan the target.
			var wins atomic.Int32
			var wg sync.WaitGroup
			for range 12 {
				wg.Go(func() {
					ok, err := s.ClaimDoc(ctx, k, "first", time.Now().Add(time.Minute).UnixMilli(), true)
					if err != nil {
						t.Error(err)
					}
					if ok {
						wins.Add(1)
					}
				})
			}
			wg.Wait()
			if wins.Load() != 1 {
				t.Fatalf("lease winners: %d", wins.Load())
			}
			d, err := s.GetDoc(ctx, k)
			if err != nil {
				t.Fatal(err)
			}
			d.Payload, d.GeneratedAt, d.SourceVersion = `{"schema":"main"}`, 123, "source-v1"
			// Editing a schedule while generation is running must win.
			if err := s.SetDocSchedule(ctx, k, "@daily", past); err != nil {
				t.Fatal(err)
			}
			if ok, err := s.FinishDoc(ctx, d, "first", true, 999); err != nil || !ok {
				t.Fatalf("finish: %v %v", ok, err)
			}
			d, _ = s.GetDoc(ctx, k)
			if d.CronSpec != "@daily" || d.NextRun != past || d.Payload == "" {
				t.Fatalf("concurrent schedule edit lost: %+v", d)
			}
			// An expired writer cannot replace its successor's result.
			if ok, err := s.ClaimDoc(ctx, k, "expired", past, false); err != nil || !ok {
				t.Fatalf("expired claim: %v %v", ok, err)
			}
			if ok, err := s.ClaimDoc(ctx, k, "new", time.Now().Add(time.Minute).UnixMilli(), false); err != nil || !ok {
				t.Fatalf("new claim: %v %v", ok, err)
			}
			if ok, err := s.FinishDoc(ctx, d, "expired", true, 0); err != nil || ok {
				t.Fatalf("stale writer accepted: %v %v", ok, err)
			}
			// Failure leaves the last good snapshot intact, including its date.
			d.Payload, d.LastError = "bad", "refresh_failed"
			next := time.Now().Add(time.Hour).UnixMilli()
			if ok, err := s.FinishDoc(ctx, d, "new", false, next); err != nil || !ok {
				t.Fatalf("failure: %v %v", ok, err)
			}
			d, _ = s.GetDoc(ctx, k)
			if d.Payload != `{"schema":"main"}` || d.GeneratedAt != 123 || d.LastError != "refresh_failed" || d.LeaseToken != "" {
				t.Fatalf("good snapshot lost: %+v", d)
			}
			if ok, err := s.ClaimDoc(ctx, k, "late-dispatcher", next, true); err != nil || ok {
				t.Fatalf("already completed occurrence claimed: %v %v", ok, err)
			}
			// The delete cascades and does not leave metadata behind.
			if err := s.DeleteConnection(ctx, c.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.GetDoc(ctx, k); !errors.Is(err, ErrNotFound) {
				t.Fatalf("orphan snapshot: %v", err)
			}
		})
	}
}
