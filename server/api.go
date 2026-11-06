package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxTaskText     = 500
	maxCategoryName = 50
	maxBodyBytes    = 64 << 10
)

type api struct {
	store    *Store
	icons    *Icons
	renderer *Renderer
	// changed is called after every change that can affect the device image.
	changed func()
}

func (a *api) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/tasks", a.listTasks)
	mux.HandleFunc("POST /api/tasks", a.addTask)
	mux.HandleFunc("PUT /api/tasks/{id}", a.updateTask)
	mux.HandleFunc("POST /api/tasks/{id}/close", a.closeTask)
	mux.HandleFunc("DELETE /api/tasks/{id}", a.deleteTask)
	mux.HandleFunc("GET /api/categories", a.listCategories)
	mux.HandleFunc("POST /api/categories", a.addCategory)
	mux.HandleFunc("DELETE /api/categories/{id}", a.deleteCategory)
	mux.HandleFunc("GET /api/icons", a.listIcons)
	mux.HandleFunc("GET /api/device", a.getDevice)
	mux.HandleFunc("PUT /api/device", a.setDevice)
	mux.HandleFunc("GET /api/image.png", a.imagePNG)
	mux.HandleFunc("GET /api/image.bin", a.imageBin)
}

func (a *api) listTasks(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "open"
	}
	if status != "open" && status != "closed" && status != "all" {
		writeError(w, http.StatusBadRequest, `status must be "open", "closed" or "all"`)
		return
	}
	tasks, err := a.store.ListTasks(r.Context(), status)
	if storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (a *api) addTask(w http.ResponseWriter, r *http.Request) {
	var in TaskInput
	if !decode(w, r, &in) || !validateTask(w, &in) {
		return
	}
	t, err := a.store.AddTask(r.Context(), in)
	if storeError(w, err) {
		return
	}
	a.changed()
	writeJSON(w, http.StatusCreated, t)
}

func (a *api) updateTask(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in TaskInput
	if !decode(w, r, &in) || !validateTask(w, &in) {
		return
	}
	t, err := a.store.UpdateTask(r.Context(), id, in)
	if storeError(w, err) {
		return
	}
	a.changed()
	writeJSON(w, http.StatusOK, t)
}

func (a *api) closeTask(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	t, err := a.store.CloseTask(r.Context(), id)
	if storeError(w, err) {
		return
	}
	a.changed()
	writeJSON(w, http.StatusOK, t)
}

func (a *api) deleteTask(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if storeError(w, a.store.DeleteTask(r.Context(), id)) {
		return
	}
	a.changed()
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) listCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := a.store.ListCategories(r.Context())
	if storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, cats)
}

func (a *api) addCategory(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
		Icon string `json:"icon"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Name == "":
		writeError(w, http.StatusBadRequest, "name is required")
		return
	case utf8.RuneCountInString(in.Name) > maxCategoryName:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("name is longer than %d characters", maxCategoryName))
		return
	}
	if _, ok := a.icons.Rune(in.Icon); in.Icon != "" && !ok {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown icon %q", in.Icon))
		return
	}
	c, err := a.store.AddCategory(r.Context(), in.Name, in.Icon)
	if errors.Is(err, ErrDuplicate) {
		writeError(w, http.StatusConflict, fmt.Sprintf("category %q already exists", in.Name))
		return
	}
	if storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (a *api) deleteCategory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if storeError(w, a.store.DeleteCategory(r.Context(), id)) {
		return
	}
	a.changed()
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) listIcons(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.icons.Names())
}

// deviceSettings are the device settings made in the browser.
type deviceSettings struct {
	// CategoryID is the only category whose tasks the device shows; nil shows all tasks.
	CategoryID *int64 `json:"category_id"`
}

func (a *api) getDevice(w http.ResponseWriter, r *http.Request) {
	id, err := a.store.DeviceCategory(r.Context())
	if storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, deviceSettings{CategoryID: id})
}

func (a *api) setDevice(w http.ResponseWriter, r *http.Request) {
	var in deviceSettings
	if !decode(w, r, &in) {
		return
	}
	if storeError(w, a.store.SetDeviceCategory(r.Context(), in.CategoryID)) {
		return
	}
	a.changed()
	writeJSON(w, http.StatusOK, in)
}

// deviceFrame renders the current open tasks at the rotation the device last asked for.
func (a *api) deviceFrame(ctx context.Context) (*Frame, error) {
	rotation, err := a.store.DeviceRotation(ctx)
	if err != nil {
		return nil, err
	}
	return a.frame(ctx, rotation)
}

// frame renders the current open tasks, only those of the device's category if it has one.
func (a *api) frame(ctx context.Context, rotation int) (*Frame, error) {
	tasks, err := a.store.ListTasks(ctx, "open")
	if err != nil {
		return nil, err
	}
	only, err := a.store.DeviceCategory(ctx)
	if err != nil {
		return nil, err
	}
	cats, err := a.store.ListCategories(ctx)
	if err != nil {
		return nil, err
	}
	icons := map[int64]rune{}
	for _, c := range cats {
		if r, ok := a.icons.Rune(c.Icon); ok {
			icons[c.ID] = r
		}
	}
	items := make([]ImageTask, 0, len(tasks))
	for _, t := range tasks {
		if only != nil && (t.CategoryID == nil || *t.CategoryID != *only) {
			continue
		}
		item := ImageTask{Text: t.Text, Priority: t.Priority, Due: t.Due}
		if t.CategoryID != nil {
			item.Icon = icons[*t.CategoryID]
		}
		items = append(items, item)
	}
	return a.renderer.Render(items, rotation), nil
}

// rotation returns the rotation asked for with ?rotation=, or the device's last rotation if
// none is given. It writes a 400 response and returns false if the value is invalid.
func (a *api) rotation(w http.ResponseWriter, r *http.Request) (int, bool) {
	s := r.URL.Query().Get("rotation")
	if s == "" {
		rotation, err := a.store.DeviceRotation(r.Context())
		return rotation, !storeError(w, err)
	}
	rotation, err := strconv.Atoi(s)
	if err != nil || !ValidRotation(rotation) {
		writeError(w, http.StatusBadRequest, "rotation must be 0, 90, 180 or 270")
		return 0, false
	}
	return rotation, true
}

// imagePNG serves a preview, by default at the rotation the device shows.
func (a *api) imagePNG(w http.ResponseWriter, r *http.Request) {
	rotation, ok := a.rotation(w, r)
	if !ok {
		return
	}
	f, err := a.frame(r.Context(), rotation)
	if storeError(w, err) {
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	if err := png.Encode(w, f.Image); err != nil {
		log.Printf("write image: %v", err)
	}
}

// imageBin serves the panel data. A request from the device also records its address and rotation.
func (a *api) imageBin(w http.ResponseWriter, r *http.Request) {
	rotation, ok := a.rotation(w, r)
	if !ok {
		return
	}
	if r.Header.Get("X-Device") == "photopainter" {
		a.recordDevice(r, rotation)
	}
	f, err := a.frame(r.Context(), rotation)
	if storeError(w, err) {
		return
	}
	data := f.PanelData()
	etag := ETag(data)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	if strings.Contains(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if _, err := w.Write(data); err != nil {
		log.Printf("write panel data: %v", err)
	}
}

// recordDevice saves where the device is and how it is rotated, so the server can
// ask it to refresh and draw previews the way it shows them.
func (a *api) recordDevice(r *http.Request, rotation int) {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		if err := a.store.SetDeviceAddr(r.Context(), host); err != nil {
			log.Printf("save device address: %v", err)
		}
	}
	if err := a.store.SetDeviceRotation(r.Context(), rotation); err != nil {
		log.Printf("save device rotation: %v", err)
	}
}

// validateTask normalises the input and writes a 400 response if it is invalid.
func validateTask(w http.ResponseWriter, in *TaskInput) bool {
	in.Text = strings.TrimSpace(in.Text)
	if in.Due != nil && *in.Due == "" {
		in.Due = nil
	}
	var msg string
	switch {
	case in.Text == "":
		msg = "text is required"
	case utf8.RuneCountInString(in.Text) > maxTaskText:
		msg = fmt.Sprintf("text is longer than %d characters", maxTaskText)
	case !in.Priority.Valid():
		msg = `priority must be "high", "medium" or "low"`
	case in.Due != nil && !validDate(*in.Due):
		msg = "due must be a date in YYYY-MM-DD format"
	default:
		return true
	}
	writeError(w, http.StatusBadRequest, msg)
	return false
}

func validDate(s string) bool {
	_, err := time.Parse(time.DateOnly, s)
	return err == nil
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return 0, false
	}
	return id, true
}

// storeError writes the response for a store error and reports whether there was one.
func storeError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, ErrUnknownCategory):
		writeError(w, http.StatusBadRequest, "unknown category")
	default:
		log.Printf("store: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
