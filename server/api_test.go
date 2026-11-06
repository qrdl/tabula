package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type testAPI struct {
	t       *testing.T
	handler http.Handler
	api     *api
	store   *Store
	changes int
}

func newTestAPI(t *testing.T) *testAPI {
	t.Helper()
	icons, err := LoadIcons()
	if err != nil {
		t.Fatal(err)
	}
	renderer, err := NewRenderer(defaultImageConfig())
	if err != nil {
		t.Fatal(err)
	}
	ta := &testAPI{t: t, store: newTestStore(t)}
	ta.api = &api{store: ta.store, icons: icons, renderer: renderer, changed: func() { ta.changes++ }}
	mux := http.NewServeMux()
	ta.api.routes(mux)
	ta.handler = mux
	return ta
}

// do sends a request and returns the status code and the response body.
func (ta *testAPI) do(method, path, body string) (int, string) {
	ta.t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	rec := httptest.NewRecorder()
	ta.handler.ServeHTTP(rec, httptest.NewRequest(method, path, r))
	return rec.Code, rec.Body.String()
}

// mustDo is do, failing the test unless the status is want; the body is decoded into out.
func (ta *testAPI) mustDo(method, path, body string, want int, out any) {
	ta.t.Helper()
	code, resp := ta.do(method, path, body)
	if code != want {
		ta.t.Fatalf("%s %s: status %d, want %d; body %s", method, path, code, want, resp)
	}
	if out != nil {
		if err := json.Unmarshal([]byte(resp), out); err != nil {
			ta.t.Fatalf("%s %s: decode %q: %v", method, path, resp, err)
		}
	}
}

func TestAPIValidation(t *testing.T) {
	ta := newTestAPI(t)
	long := strings.Repeat("a", maxTaskText+1)
	tests := []struct {
		name, method, path, body string
		want                     int
	}{
		{"empty text", "POST", "/api/tasks", `{"text":"  ","priority":"high"}`, 400},
		{"too long text", "POST", "/api/tasks", `{"text":"` + long + `","priority":"high"}`, 400},
		{"missing priority", "POST", "/api/tasks", `{"text":"x"}`, 400},
		{"bad priority", "POST", "/api/tasks", `{"text":"x","priority":"urgent"}`, 400},
		{"bad month", "POST", "/api/tasks", `{"text":"x","priority":"high","due":"2026-13-01"}`, 400},
		{"wrong date format", "POST", "/api/tasks", `{"text":"x","priority":"high","due":"24.10.2026"}`, 400},
		{"unknown category", "POST", "/api/tasks", `{"text":"x","priority":"high","category_id":999}`, 400},
		{"unknown field", "POST", "/api/tasks", `{"text":"x","priority":"high","colour":"red"}`, 400},
		{"not JSON", "POST", "/api/tasks", `text=x`, 400},
		{"bad status filter", "GET", "/api/tasks?status=done", ``, 400},
		{"update missing task", "PUT", "/api/tasks/999", `{"text":"x","priority":"high"}`, 404},
		{"non-numeric id", "PUT", "/api/tasks/abc", `{"text":"x","priority":"high"}`, 404},
		{"close missing task", "POST", "/api/tasks/999/close", ``, 404},
		{"delete missing task", "DELETE", "/api/tasks/999", ``, 404},
		{"empty category name", "POST", "/api/categories", `{"name":""}`, 400},
		{"unknown icon", "POST", "/api/categories", `{"name":"Shop","icon":"no_such_icon"}`, 400},
		{"delete missing category", "DELETE", "/api/categories/999", ``, 404},
		{"unknown device category", "PUT", "/api/device", `{"category_id":999}`, 400},
		{"unknown device field", "PUT", "/api/device", `{"rotation":90}`, 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, body := ta.do(tt.method, tt.path, tt.body)
			if code != tt.want {
				t.Errorf("status %d, want %d; body %s", code, tt.want, body)
			}
			var e struct{ Error string }
			if json.Unmarshal([]byte(body), &e); e.Error == "" {
				t.Errorf("no error message in %s", body)
			}
		})
	}
	if ta.changes != 0 {
		t.Errorf("failed requests reported %d changes", ta.changes)
	}
}

func TestAPITaskFlow(t *testing.T) {
	ta := newTestAPI(t)

	var cat Category
	ta.mustDo("POST", "/api/categories", `{"name":"Shopping","icon":"shopping_cart"}`, 201, &cat)
	if ta.changes != 0 {
		t.Errorf("adding a category reported a change")
	}
	ta.mustDo("POST", "/api/categories", `{"name":"Shopping"}`, 409, nil)

	var task Task
	ta.mustDo("POST", "/api/tasks", `{"text":" Buy milk ","priority":"high","due":"2026-10-24","category_id":1}`, 201, &task)
	if task.Text != "Buy milk" || task.Priority != PriorityHigh || task.Due == nil || *task.Due != "2026-10-24" ||
		task.CategoryID == nil || *task.CategoryID != cat.ID || task.Status != "open" || task.ClosedAt != nil {
		t.Errorf("created task: %+v", task)
	}

	// An empty due date is the same as none.
	ta.mustDo("PUT", "/api/tasks/1", `{"text":"Buy oat milk","priority":"medium","due":"","category_id":1}`, 200, &task)
	if task.Text != "Buy oat milk" || task.Priority != PriorityMedium || task.Due != nil {
		t.Errorf("updated task: %+v", task)
	}

	var tasks []Task
	ta.mustDo("GET", "/api/tasks", ``, 200, &tasks)
	if len(tasks) != 1 {
		t.Fatalf("open tasks: %+v", tasks)
	}

	ta.mustDo("POST", "/api/tasks/1/close", ``, 200, &task)
	if task.Status != "closed" || task.ClosedAt == nil {
		t.Errorf("closed task: %+v", task)
	}
	ta.mustDo("GET", "/api/tasks", ``, 200, &tasks)
	if len(tasks) != 0 {
		t.Errorf("open tasks after close: %+v", tasks)
	}
	ta.mustDo("GET", "/api/tasks?status=closed", ``, 200, &tasks)
	if len(tasks) != 1 {
		t.Errorf("closed tasks: %+v", tasks)
	}

	ta.mustDo("DELETE", "/api/categories/1", ``, 204, nil)
	ta.mustDo("GET", "/api/tasks?status=all", ``, 200, &tasks)
	if len(tasks) != 1 || tasks[0].CategoryID != nil {
		t.Errorf("tasks after deleting their category: %+v", tasks)
	}

	ta.mustDo("DELETE", "/api/tasks/1", ``, 204, nil)
	ta.mustDo("GET", "/api/tasks?status=all", ``, 200, &tasks)
	if len(tasks) != 0 {
		t.Errorf("tasks after delete: %+v", tasks)
	}

	// add, update, close, delete category, delete task
	if ta.changes != 5 {
		t.Errorf("changes reported: %d, want 5", ta.changes)
	}
}

func TestAPIDeviceCategory(t *testing.T) {
	ta := newTestAPI(t)
	var shop, work Category
	ta.mustDo("POST", "/api/categories", `{"name":"Shopping"}`, 201, &shop)
	ta.mustDo("POST", "/api/categories", `{"name":"Work"}`, 201, &work)
	for _, body := range []string{
		`{"text":"milk","priority":"high","category_id":1}`,
		`{"text":"bread","priority":"low","category_id":1}`,
		`{"text":"report","priority":"high","category_id":2}`,
		`{"text":"call mum","priority":"medium"}`,
	} {
		ta.mustDo("POST", "/api/tasks", body, 201, nil)
	}
	shown := func() int {
		t.Helper()
		f, err := ta.api.frame(context.Background(), 0)
		if err != nil {
			t.Fatal(err)
		}
		return f.Total
	}

	var dev deviceSettings
	ta.mustDo("GET", "/api/device", ``, 200, &dev)
	if dev.CategoryID != nil {
		t.Errorf("initial device category %d", *dev.CategoryID)
	}
	if n := shown(); n != 4 {
		t.Errorf("all categories: %d tasks shown, want 4", n)
	}

	changes := ta.changes
	ta.mustDo("PUT", "/api/device", `{"category_id":1}`, 200, &dev)
	if dev.CategoryID == nil || *dev.CategoryID != shop.ID {
		t.Errorf("device category after set: %v", dev.CategoryID)
	}
	if ta.changes != changes+1 {
		t.Errorf("setting the device category reported %d changes, want 1", ta.changes-changes)
	}
	ta.mustDo("GET", "/api/device", ``, 200, &dev)
	if dev.CategoryID == nil || *dev.CategoryID != shop.ID {
		t.Errorf("device category: %v", dev.CategoryID)
	}
	if n := shown(); n != 2 {
		t.Errorf("Shopping: %d tasks shown, want 2", n)
	}

	ta.mustDo("PUT", "/api/device", `{"category_id":null}`, 200, &dev)
	if n := shown(); n != 4 {
		t.Errorf("back to all categories: %d tasks shown, want 4", n)
	}

	ta.mustDo("PUT", "/api/device", `{"category_id":2}`, 200, &dev)
	if n := shown(); n != 1 {
		t.Errorf("Work: %d tasks shown, want 1", n)
	}
	ta.mustDo("DELETE", "/api/categories/2", ``, 204, nil)
	ta.mustDo("GET", "/api/device", ``, 200, &dev)
	if dev.CategoryID != nil {
		t.Errorf("device category after deleting it: %d", *dev.CategoryID)
	}
	if n := shown(); n != 4 {
		t.Errorf("after deleting the shown category: %d tasks shown, want 4", n)
	}
}

func TestAPIEmptyListsAreArrays(t *testing.T) {
	ta := newTestAPI(t)
	for _, path := range []string{"/api/tasks", "/api/categories"} {
		if _, body := ta.do("GET", path, ``); strings.TrimSpace(body) != "[]" {
			t.Errorf("%s: got %s, want []", path, body)
		}
	}
}

func TestAPIIcons(t *testing.T) {
	ta := newTestAPI(t)
	var names []string
	ta.mustDo("GET", "/api/icons", ``, 200, &names)
	if len(names) < 2000 {
		t.Errorf("only %d icons", len(names))
	}
	found := false
	for _, n := range names {
		found = found || n == "shopping_cart"
	}
	if !found {
		t.Error("shopping_cart missing")
	}
}
