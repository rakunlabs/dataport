package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rakunlabs/dataport/internal/notebook"
)

func TestNotebookStore(t *testing.T) {
	for name, st := range stores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			alice, bob := &User{Username: "note-alice", Role: RoleUser, PasswordHash: "x"}, &User{Username: "note-bob", Role: RoleUser, PasswordHash: "x"}
			for _, u := range []*User{alice, bob} {
				if err := st.CreateUser(ctx, u); err != nil {
					t.Fatal(err)
				}
			}
			n := &Notebook{OwnerID: alice.ID, Name: "Report", Path: "report", Document: notebook.Document{Cells: []notebook.Cell{{ID: "a", Path: "a", SQL: "SELECT 1", ConnectionID: "db", Enabled: true}}}}
			if err := st.SaveNotebook(ctx, n); err != nil {
				t.Fatal(err)
			}
			if _, err := st.GetNotebook(ctx, bob, n.ID, false); !errors.Is(err, ErrNotFound) {
				t.Fatal(err)
			}
			if err := st.PublishNotebook(ctx, n.ID, n.Version, true); err != nil {
				t.Fatal(err)
			}
			old := n.Version
			n.Document.Cells[0].SQL = "SELECT 2"
			if err := st.SaveNotebook(ctx, n); err != nil {
				t.Fatal(err)
			}
			saved, err := st.GetNotebook(ctx, alice, n.ID, false)
			if err != nil || saved.Published.Cells[0].SQL != "SELECT 1" || saved.Document.Cells[0].SQL != "SELECT 2" {
				t.Fatalf("%+v %v", saved, err)
			}
			stale := *n
			stale.Version = old
			if err := st.SaveNotebook(ctx, &stale); !errors.Is(err, ErrVersion) {
				t.Fatal(err)
			}
			if err := st.PublishNotebook(ctx, n.ID, old, true); !errors.Is(err, ErrVersion) {
				t.Fatal(err)
			}
			group := &Group{Name: "readers", MemberIDs: []string{bob.ID}}
			if err := st.CreateGroup(ctx, group); err != nil {
				t.Fatal(err)
			}
			if err := st.SetNotebookAccess(ctx, n.ID, []NotebookAccess{{SubjectType: "group", SubjectID: group.ID, Permission: "run"}}); err != nil {
				t.Fatal(err)
			}
			shared, err := st.GetNotebook(ctx, bob, n.ID, false)
			if err != nil || shared.Permission != "run" {
				t.Fatalf("%+v %v", shared, err)
			}
			list, err := st.ListNotebooks(ctx, bob)
			if err != nil || len(list) != 1 || list[0].Document.Cells != nil {
				t.Fatalf("%+v %v", list, err)
			}
			run := &NotebookRun{NotebookID: n.ID, UserID: bob.ID, Version: n.Version, Source: "notebook"}
			if err := st.CreateNotebookRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			if ok, err := st.HeartbeatNotebookRun(ctx, run.ID); err != nil || !ok {
				t.Fatalf("%v %v", ok, err)
			}
			if err := st.CancelNotebookRun(ctx, run.ID); err != nil {
				t.Fatal(err)
			}
			if ok, _ := st.HeartbeatNotebookRun(ctx, run.ID); ok {
				t.Fatal("cancel ignored")
			}
			if err := st.UpdateNotebookRun(ctx, run.ID, "cancelled", map[string]any{"cells": []any{}}); err != nil {
				t.Fatal(err)
			}
			if err := st.UpdateNotebookRun(ctx, run.ID, "completed", nil); err != nil {
				t.Fatal(err)
			}
			final, err := st.GetNotebookRun(ctx, run.ID)
			if err != nil || final.Status != "cancelled" {
				t.Fatalf("%+v %v", final, err)
			}
			if err := st.MaintainNotebookRuns(ctx, time.Now().UnixMilli(), time.Now().Add(-time.Hour).UnixMilli()); err != nil {
				t.Fatal(err)
			}
			// Removing a subject removes its notebook access, so the owner
			// can still save the remaining sharing list.
			carol := &User{Username: "note-carol", Role: RoleUser, PasswordHash: "x"}
			if err := st.CreateUser(ctx, carol); err != nil {
				t.Fatal(err)
			}
			if err := st.SetNotebookAccess(ctx, n.ID, []NotebookAccess{{SubjectType: SubjectGroup, SubjectID: group.ID, Permission: "run"}, {SubjectType: SubjectUser, SubjectID: carol.ID, Permission: "view"}}); err != nil {
				t.Fatal(err)
			}
			if err := st.DeleteUser(ctx, carol.ID); err != nil {
				t.Fatal(err)
			}
			if err := st.DeleteGroup(ctx, group.ID); err != nil {
				t.Fatal(err)
			}
			if access, err := st.NotebookAccess(ctx, n.ID); err != nil || len(access) != 0 {
				t.Fatalf("orphaned notebook access %+v %v", access, err)
			}
			if err := st.DeleteNotebook(ctx, n.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := st.GetNotebookRun(ctx, run.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("run not cascaded", err)
			}
		})
	}
}
