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
			if due, err := s.DueDocs(ctx, time.Now().UnixMilli()+1); err != nil || len(due) != 1 {
				t.Fatalf("due: %v %v", due, err)
			}
			// Simulate competing replicas; exactly one may scan the target.
			var wins atomic.Int32
			var wg sync.WaitGroup
			for range 12 {
				wg.Go(func() {
					ok, err := s.ClaimDoc(ctx, k, "first", time.Now().Add(time.Minute).UnixMilli(), time.Now().UnixMilli()+1)
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
			if ok, err := s.FinishDoc(ctx, d, "first", true); err != nil || !ok {
				t.Fatalf("finish: %v %v", ok, err)
			}
			// An expired writer cannot replace its successor's result.
			if ok, err := s.ClaimDoc(ctx, k, "expired", past, 0); err != nil || !ok {
				t.Fatalf("expired claim: %v %v", ok, err)
			}
			if ok, err := s.ClaimDoc(ctx, k, "new", time.Now().Add(time.Minute).UnixMilli(), 0); err != nil || !ok {
				t.Fatalf("new claim: %v %v", ok, err)
			}
			if ok, err := s.FinishDoc(ctx, d, "expired", true); err != nil || ok {
				t.Fatalf("stale writer accepted: %v %v", ok, err)
			}
			// Failure leaves the last good snapshot intact, including its date.
			d.Payload, d.LastError = "bad", "refresh_failed"
			attempted := d.AttemptedAt
			if ok, err := s.FinishDoc(ctx, d, "new", false); err != nil || !ok {
				t.Fatalf("failure: %v %v", ok, err)
			}
			d, _ = s.GetDoc(ctx, k)
			if d.Payload != `{"schema":"main"}` || d.GeneratedAt != 123 || d.LastError != "refresh_failed" || d.LeaseToken != "" {
				t.Fatalf("good snapshot lost: %+v", d)
			}
			// A dispatcher for an occurrence already attempted skips it.
			if ok, err := s.ClaimDoc(ctx, k, "late-dispatcher", time.Now().Add(time.Minute).UnixMilli(), attempted); err != nil || ok {
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
