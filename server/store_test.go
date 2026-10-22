package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	// Each timestamp is one minute after the previous, so creation order is deterministic.
	clock := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { clock = clock.Add(time.Minute); return clock }
	return s
}

func ptr[T any](v T) *T { return &v }

func mustAddTask(t *testing.T, s *Store, in TaskInput) Task {
	t.Helper()
	task, err := s.AddTask(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func taskTexts(tasks []Task) []string {
	var out []string
	for _, t := range tasks {
		out = append(out, t.Text)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestListTasksOrder(t *testing.T) {
	s := newTestStore(t)
	mustAddTask(t, s, TaskInput{Text: "low, no due", Priority: PriorityLow})
	mustAddTask(t, s, TaskInput{Text: "high, Oct 25", Priority: PriorityHigh, Due: ptr("2026-10-25")})
	mustAddTask(t, s, TaskInput{Text: "high, no due", Priority: PriorityHigh})
	mustAddTask(t, s, TaskInput{Text: "high, Oct 22, first", Priority: PriorityHigh, Due: ptr("2026-10-22")})
	mustAddTask(t, s, TaskInput{Text: "medium, Oct 21", Priority: PriorityMedium, Due: ptr("2026-10-21")})
	mustAddTask(t, s, TaskInput{Text: "high, Oct 22, second", Priority: PriorityHigh, Due: ptr("2026-10-22")})

	tasks, err := s.ListTasks(context.Background(), "open")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"high, Oct 22, first", "high, Oct 22, second", "high, Oct 25", "high, no due", "medium, Oct 21", "low, no due"}
	if got := taskTexts(tasks); !equalStrings(got, want) {
		t.Errorf("order:\n got %q\nwant %q", got, want)
	}
}

func TestCloseTask(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	open := mustAddTask(t, s, TaskInput{Text: "stays open", Priority: PriorityLow})
	done := mustAddTask(t, s, TaskInput{Text: "gets closed", Priority: PriorityHigh})

	closed, err := s.CloseTask(ctx, done.ID)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Status != "closed" || closed.ClosedAt == nil {
		t.Fatalf("after close: status %q, closed_at %v", closed.Status, closed.ClosedAt)
	}

	again, err := s.CloseTask(ctx, done.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !again.ClosedAt.Equal(*closed.ClosedAt) {
		t.Errorf("closing twice changed closed_at from %v to %v", closed.ClosedAt, again.ClosedAt)
	}

	for status, want := range map[string][]string{
		"open":   {open.Text},
		"closed": {done.Text},
		"all":    {done.Text, open.Text},
	} {
		tasks, err := s.ListTasks(ctx, status)
		if err != nil {
			t.Fatal(err)
		}
		if got := taskTexts(tasks); !equalStrings(got, want) {
			t.Errorf("status %s: got %q, want %q", status, got, want)
		}
	}

	if _, err := s.CloseTask(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("close missing task: got %v, want ErrNotFound", err)
	}
}

func TestDeleteTask(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	task := mustAddTask(t, s, TaskInput{Text: "x", Priority: PriorityLow})
	if err := s.DeleteTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetTask(ctx, task.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("get deleted task: got %v, want ErrNotFound", err)
	}
	if err := s.DeleteTask(ctx, task.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete twice: got %v, want ErrNotFound", err)
	}
}

func TestUpdateTask(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	cat, err := s.AddCategory(ctx, "Home", "home")
	if err != nil {
		t.Fatal(err)
	}
	task := mustAddTask(t, s, TaskInput{Text: "old", Priority: PriorityLow, Due: ptr("2026-10-24"), CategoryID: &cat.ID})

	got, err := s.UpdateTask(ctx, task.ID, TaskInput{Text: "new", Priority: PriorityHigh})
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "new" || got.Priority != PriorityHigh || got.Due != nil || got.CategoryID != nil {
		t.Errorf("after update: %+v", got)
	}
	if _, err := s.UpdateTask(ctx, 999, TaskInput{Text: "x", Priority: PriorityLow}); !errors.Is(err, ErrNotFound) {
		t.Errorf("update missing task: got %v, want ErrNotFound", err)
	}
}

func TestUnknownCategory(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if _, err := s.AddTask(ctx, TaskInput{Text: "x", Priority: PriorityLow, CategoryID: ptr(int64(999))}); !errors.Is(err, ErrUnknownCategory) {
		t.Errorf("add: got %v, want ErrUnknownCategory", err)
	}
	task := mustAddTask(t, s, TaskInput{Text: "x", Priority: PriorityLow})
	if _, err := s.UpdateTask(ctx, task.ID, TaskInput{Text: "x", Priority: PriorityLow, CategoryID: ptr(int64(999))}); !errors.Is(err, ErrUnknownCategory) {
		t.Errorf("update: got %v, want ErrUnknownCategory", err)
	}
}

func TestCategories(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	shop, err := s.AddCategory(ctx, "shopping", "shopping_cart")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddCategory(ctx, "Work", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddCategory(ctx, "shopping", ""); !errors.Is(err, ErrDuplicate) {
		t.Errorf("duplicate name: got %v, want ErrDuplicate", err)
	}

	cats, err := s.ListCategories(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(cats) != 2 || cats[0].Name != "shopping" || cats[0].Icon != "shopping_cart" || cats[1].Name != "Work" || cats[1].Icon != "" {
		t.Errorf("categories: %+v", cats)
	}

	task := mustAddTask(t, s, TaskInput{Text: "milk", Priority: PriorityLow, CategoryID: &shop.ID})
	if err := s.DeleteCategory(ctx, shop.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("task was deleted with its category: %v", err)
	}
	if got.CategoryID != nil {
		t.Errorf("task still has category %d", *got.CategoryID)
	}
	if err := s.DeleteCategory(ctx, shop.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete twice: got %v, want ErrNotFound", err)
	}
}

func TestDeviceAddr(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if addr, err := s.DeviceAddr(ctx); err != nil || addr != "" {
		t.Fatalf("initial: %q, %v", addr, err)
	}
	for _, want := range []string{"192.168.8.50", "192.168.8.57"} {
		if err := s.SetDeviceAddr(ctx, want); err != nil {
			t.Fatal(err)
		}
		if got, err := s.DeviceAddr(ctx); err != nil || got != want {
			t.Errorf("got %q, %v; want %q", got, err, want)
		}
	}
}
