package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

var (
	ErrNotFound        = errors.New("not found")
	ErrDuplicate       = errors.New("already exists")
	ErrUnknownCategory = errors.New("unknown category")
)

type Priority int

const (
	PriorityHigh   Priority = 1
	PriorityMedium Priority = 2
	PriorityLow    Priority = 3
)

var priorityNames = map[Priority]string{
	PriorityHigh:   "high",
	PriorityMedium: "medium",
	PriorityLow:    "low",
}

func (p Priority) Valid() bool { _, ok := priorityNames[p]; return ok }

func (p Priority) MarshalJSON() ([]byte, error) {
	name, ok := priorityNames[p]
	if !ok {
		return nil, fmt.Errorf("invalid priority %d", int(p))
	}
	return json.Marshal(name)
}

func (p *Priority) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return errors.New(`priority must be "high", "medium" or "low"`)
	}
	for v, name := range priorityNames {
		if name == s {
			*p = v
			return nil
		}
	}
	return errors.New(`priority must be "high", "medium" or "low"`)
}

type Task struct {
	ID         int64      `json:"id"`
	Text       string     `json:"text"`
	Priority   Priority   `json:"priority"`
	Due        *string    `json:"due"` // YYYY-MM-DD
	CategoryID *int64     `json:"category_id"`
	Status     string     `json:"status"` // "open" or "closed"
	CreatedAt  time.Time  `json:"created_at"`
	ClosedAt   *time.Time `json:"closed_at"`
}

// TaskInput holds the editable fields of a task.
type TaskInput struct {
	Text       string   `json:"text"`
	Priority   Priority `json:"priority"`
	Due        *string  `json:"due"`
	CategoryID *int64   `json:"category_id"`
}

type Category struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Icon string `json:"icon"` // Material icon name; "" = none
}

const schema = `
CREATE TABLE IF NOT EXISTS categories (
  id    INTEGER PRIMARY KEY,
  name  TEXT NOT NULL UNIQUE,
  icon  TEXT
);
CREATE TABLE IF NOT EXISTS tasks (
  id          INTEGER PRIMARY KEY,
  text        TEXT NOT NULL,
  priority    INTEGER NOT NULL CHECK (priority IN (1,2,3)),
  due         TEXT,
  category_id INTEGER REFERENCES categories(id) ON DELETE SET NULL,
  status      TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','closed')),
  created_at  TEXT NOT NULL,
  closed_at   TEXT
);
CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`

const taskColumns = `id, text, priority, due, category_id, status, created_at, closed_at`

const taskOrder = `ORDER BY priority, due IS NULL, due, created_at, id`

type Store struct {
	db  *sql.DB
	now func() time.Time
}

func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	// One connection serialises writes, which is plenty for a home to-do list.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	return &Store{db: db, now: time.Now}, nil
}

func (s *Store) Close() error { return s.db.Close() }

type rowScanner interface{ Scan(dest ...any) error }

func scanTask(row rowScanner) (Task, error) {
	var t Task
	var due, created, closed sql.NullString
	var cat sql.NullInt64
	if err := row.Scan(&t.ID, &t.Text, &t.Priority, &due, &cat, &t.Status, &created, &closed); err != nil {
		return t, err
	}
	if due.Valid {
		t.Due = &due.String
	}
	if cat.Valid {
		t.CategoryID = &cat.Int64
	}
	var err error
	if t.CreatedAt, err = time.Parse(time.RFC3339, created.String); err != nil {
		return t, fmt.Errorf("task %d created_at: %w", t.ID, err)
	}
	if closed.Valid {
		ct, err := time.Parse(time.RFC3339, closed.String)
		if err != nil {
			return t, fmt.Errorf("task %d closed_at: %w", t.ID, err)
		}
		t.ClosedAt = &ct
	}
	return t, nil
}

// ListTasks returns tasks with the given status ("open", "closed" or "all"),
// sorted by priority, then due date (no date last), then creation time.
func (s *Store) ListTasks(ctx context.Context, status string) ([]Task, error) {
	q := `SELECT ` + taskColumns + ` FROM tasks `
	var args []any
	if status != "all" {
		q += `WHERE status = ? `
		args = append(args, status)
	}
	rows, err := s.db.QueryContext(ctx, q+taskOrder, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := []Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func (s *Store) GetTask(ctx context.Context, id int64) (Task, error) {
	t, err := scanTask(s.db.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM tasks WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

func (s *Store) AddTask(ctx context.Context, in TaskInput) (Task, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO tasks (text, priority, due, category_id, created_at) VALUES (?, ?, ?, ?, ?)`,
		in.Text, in.Priority, in.Due, in.CategoryID, s.timestamp())
	if err != nil {
		return Task{}, mapConstraintError(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Task{}, err
	}
	return s.GetTask(ctx, id)
}

func (s *Store) UpdateTask(ctx context.Context, id int64, in TaskInput) (Task, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET text = ?, priority = ?, due = ?, category_id = ? WHERE id = ?`,
		in.Text, in.Priority, in.Due, in.CategoryID, id)
	if err != nil {
		return Task{}, mapConstraintError(err)
	}
	if err := requireRow(res); err != nil {
		return Task{}, err
	}
	return s.GetTask(ctx, id)
}

// CloseTask marks an open task closed. Closing an already closed task keeps
// its original close time.
func (s *Store) CloseTask(ctx context.Context, id int64) (Task, error) {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET status = 'closed', closed_at = ? WHERE id = ? AND status = 'open'`,
		s.timestamp(), id); err != nil {
		return Task{}, err
	}
	return s.GetTask(ctx, id)
}

func (s *Store) DeleteTask(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM tasks WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return requireRow(res)
}

func (s *Store) ListCategories(ctx context.Context) ([]Category, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, icon FROM categories ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cats := []Category{}
	for rows.Next() {
		var c Category
		var icon sql.NullString
		if err := rows.Scan(&c.ID, &c.Name, &icon); err != nil {
			return nil, err
		}
		c.Icon = icon.String
		cats = append(cats, c)
	}
	return cats, rows.Err()
}

func (s *Store) AddCategory(ctx context.Context, name, icon string) (Category, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO categories (name, icon) VALUES (?, ?)`,
		name, sql.NullString{String: icon, Valid: icon != ""})
	if err != nil {
		return Category{}, mapConstraintError(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Category{}, err
	}
	return Category{ID: id, Name: name, Icon: icon}, nil
}

// DeleteCategory removes a category; its tasks are kept without a category.
// If the device was showing only this category, it goes back to showing all.
func (s *Store) DeleteCategory(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM categories WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if err := requireRow(res); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ? AND value = ?`,
		settingDeviceCategory, strconv.FormatInt(id, 10)); err != nil {
		return err
	}
	return tx.Commit()
}

const (
	settingDeviceAddr     = "device_addr"
	settingDeviceRotation = "device_rotation"
	settingDeviceCategory = "device_category"
)

// setting returns a stored value, or "" if it isn't set.
func (s *Store) setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *Store) setSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		key, value)
	return err
}

// DeviceAddr returns the last known device address, or "" if none is known.
func (s *Store) DeviceAddr(ctx context.Context) (string, error) {
	return s.setting(ctx, settingDeviceAddr)
}

func (s *Store) SetDeviceAddr(ctx context.Context, addr string) error {
	return s.setSetting(ctx, settingDeviceAddr, addr)
}

// DeviceRotation returns the rotation the device last asked for, or 0 if it hasn't asked yet.
func (s *Store) DeviceRotation(ctx context.Context) (int, error) {
	v, err := s.setting(ctx, settingDeviceRotation)
	if err != nil || v == "" {
		return 0, err
	}
	r, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("device rotation %q: %w", v, err)
	}
	return r, nil
}

func (s *Store) SetDeviceRotation(ctx context.Context, rotation int) error {
	return s.setSetting(ctx, settingDeviceRotation, strconv.Itoa(rotation))
}

// DeviceCategory returns the category whose tasks the device shows, or nil if it shows all tasks.
func (s *Store) DeviceCategory(ctx context.Context) (*int64, error) {
	v, err := s.setting(ctx, settingDeviceCategory)
	if err != nil || v == "" {
		return nil, err
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("device category %q: %w", v, err)
	}
	return &id, nil
}

// SetDeviceCategory makes the device show only the tasks of category id, or all tasks if id is nil.
func (s *Store) SetDeviceCategory(ctx context.Context, id *int64) error {
	if id == nil {
		_, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, settingDeviceCategory)
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) SELECT ?, id FROM categories WHERE id = ?
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		settingDeviceCategory, *id)
	if err != nil {
		return err
	}
	err = requireRow(res)
	if errors.Is(err, ErrNotFound) {
		return ErrUnknownCategory
	}
	return err
}

func (s *Store) timestamp() string { return s.now().UTC().Format(time.RFC3339) }

func requireRow(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func mapConstraintError(err error) error {
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() {
		case sqlite3.SQLITE_CONSTRAINT_UNIQUE:
			return ErrDuplicate
		case sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY:
			return ErrUnknownCategory
		}
	}
	return err
}
