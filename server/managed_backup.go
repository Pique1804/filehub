package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed original-backup.py
var originalBackupWorker string

const managedBackupDirectory = ".filehub-server-backups"

type backupFolder struct {
	Source string `json:"source"`
	Path   string `json:"path"`
}

type backupSettings struct {
	Enabled     bool           `json:"enabled"`
	Folders     []backupFolder `json:"folders"`
	Destination backupFolder   `json:"destination"`
	Days        []time.Weekday `json:"days"`
	Time        string         `json:"time"`
}

type backupDirectory struct {
	FullPath string `json:"fullPath"`
	Device   uint64 `json:"device"`
	Inode    uint64 `json:"inode"`
}

type backupResolvedFolder struct {
	backupFolder
	backupDirectory
}

type backupPlan struct {
	Instance    string                     `json:"instance"`
	Database    string                     `json:"database"`
	Folders     []backupResolvedFolder     `json:"folders"`
	Destination backupDirectory            `json:"destination"`
	Roots       map[string]backupDirectory `json:"roots"`
	SameVolume  bool                       `json:"sameVolume"`
}

type backupRun struct {
	State            string `json:"state"`
	Started          int64  `json:"started,omitempty"`
	Completed        int64  `json:"completed,omitempty"`
	Snapshot         string `json:"snapshot,omitempty"`
	Versions         int    `json:"versions"`
	Message          string `json:"message,omitempty"`
	SettingsRevision uint64 `json:"settingsRevision"`
}

type backupPreferences struct {
	Instance string         `json:"instance"`
	Settings backupSettings `json:"settings"`
	Plan     backupPlan     `json:"plan"`
	Legacy   bool           `json:"legacy"`
	SavedAt  int64          `json:"savedAt"`
	Revision uint64         `json:"revision"`
	LastSlot int64          `json:"lastSlot"`
	Run      backupRun      `json:"run"`
	LastCopy *backupCopy    `json:"lastCopy,omitempty"`
}

type backupCopy struct {
	Plan backupPlan `json:"plan"`
	Run  backupRun  `json:"run"`
}

type managedBackup struct {
	mu      sync.Mutex
	current backupPreferences
}

func initManagedBackup(legacy bool) (*managedBackup, error) {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS managed_backup(id INTEGER PRIMARY KEY CHECK (id=1), data TEXT NOT NULL)`); err != nil {
		return nil, err
	}
	manager := &managedBackup{}
	var raw string
	err := db.QueryRow(`SELECT data FROM managed_backup WHERE id=1`).Scan(&raw)
	if err == nil {
		if err = json.Unmarshal([]byte(raw), &manager.current); err != nil {
			return nil, err
		}
		// Existing managed backups already recorded the plan and successful snapshot.
		if manager.current.LastCopy == nil && manager.current.Run.State == "ok" &&
			manager.current.Run.SettingsRevision == manager.current.Revision {
			state := manager.current
			state.LastCopy = &backupCopy{Plan: state.Plan, Run: state.Run}
			if err = manager.save(state); err != nil {
				return nil, err
			}
		}
		if manager.current.Run.State == "running" {
			state := manager.current
			state.Run.State, state.Run.Message = "error", "Предыдущий запуск прервался при остановке сервера."
			if err = manager.save(state); err != nil {
				return nil, err
			}
		}
		return manager, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var token [16]byte
	if _, err = rand.Read(token[:]); err != nil {
		return nil, err
	}
	defaults := backupPreferences{
		Instance: hex.EncodeToString(token[:]),
		Settings: backupSettings{
			Folders: []backupFolder{}, Days: []time.Weekday{time.Sunday}, Time: "03:00",
		},
		Legacy: legacy, Run: backupRun{State: "idle"},
	}
	if err = manager.save(defaults); err != nil {
		return nil, err
	}
	return manager, nil
}

func (b *managedBackup) save(next backupPreferences) error {
	raw, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if _, err = db.Exec(`INSERT INTO managed_backup VALUES(1,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, string(raw)); err != nil {
		return err
	}
	b.current = next
	return nil
}

// Existing installations keep their local database snapshots until an admin saves
// an explicit backup preference. New installations start with scheduling off.
func (b *managedBackup) legacyEnabled() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.current.Legacy
}

func backupDirectoryInfo(path string) (backupDirectory, error) {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return backupDirectory{}, os.ErrNotExist
	}
	identity, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return backupDirectory{}, os.ErrInvalid
	}
	return backupDirectory{FullPath: path, Device: uint64(identity.Dev), Inode: uint64(identity.Ino)}, nil
}

func resolveBackupFolder(folder backupFolder) (backupResolvedFolder, error) {
	root, found := byName[folder.Source]
	if !found {
		return backupResolvedFolder{}, fmt.Errorf("Хранилище не найдено.")
	}
	if folder.Path != "" && (!filepath.IsLocal(folder.Path) || protectedRelativePath(folder.Path) ||
		strings.ContainsAny(folder.Path, "\\\x00")) {
		return backupResolvedFolder{}, fmt.Errorf("Укажите папку внутри выбранного хранилища.")
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return backupResolvedFolder{}, fmt.Errorf("Хранилище недоступно.")
	}
	folder.Path = filepath.Clean(folder.Path)
	if folder.Path == "." {
		folder.Path = ""
	}
	path := filepath.Join(root, folder.Path)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return backupResolvedFolder{}, fmt.Errorf("Папка недоступна или является ссылкой.")
	}
	info, err := backupDirectoryInfo(path)
	if err != nil {
		return backupResolvedFolder{}, fmt.Errorf("Выбранная папка недоступна.")
	}
	return backupResolvedFolder{backupFolder: folder, backupDirectory: info}, nil
}

func containsBackupPath(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && filepath.IsLocal(relative)
}

func validateBackupSchedule(settings backupSettings) error {
	if len(settings.Days) == 0 || len(settings.Days) > 7 {
		return fmt.Errorf("Выберите дни запуска.")
	}
	days := [7]bool{}
	for _, day := range settings.Days {
		if day < time.Sunday || day > time.Saturday || days[day] {
			return fmt.Errorf("Указаны неверные дни запуска.")
		}
		days[day] = true
	}
	if value, err := time.Parse("15:04", settings.Time); err != nil || value.Format("15:04") != settings.Time {
		return fmt.Errorf("Укажите время в формате ЧЧ:ММ.")
	}
	return nil
}

func resolveBackupPlan(settings *backupSettings) (backupPlan, error) {
	if !settings.Enabled {
		return backupPlan{}, nil
	}
	if err := validateBackupSchedule(*settings); err != nil {
		return backupPlan{}, err
	}
	if len(settings.Folders) == 0 || len(settings.Folders) > 32 {
		return backupPlan{}, fmt.Errorf("Выберите от одной до 32 папок.")
	}
	for _, executable := range []string{"python3", "rsync"} {
		if _, err := exec.LookPath(executable); err != nil {
			return backupPlan{}, fmt.Errorf("Для резервного копирования на сервере требуется %s.", executable)
		}
	}
	destination, err := resolveBackupFolder(settings.Destination)
	if err != nil {
		return backupPlan{}, fmt.Errorf("Назначение недоступно: %w", err)
	}
	settings.Destination = destination.backupFolder
	plan := backupPlan{Destination: destination.backupDirectory, Roots: make(map[string]backupDirectory)}
	for name, root := range byName {
		if real, err := filepath.EvalSymlinks(root); err == nil {
			if info, err := backupDirectoryInfo(real); err == nil {
				plan.Roots[name] = info
			}
		}
	}
	plan.Database, err = filepath.Abs(dbPath())
	if err != nil {
		return backupPlan{}, err
	}
	private := []string{plan.Database, env("FILEHUB_SECRET_FILE", "/opt/filehub/secret")}
	for i, selected := range settings.Folders {
		folder, err := resolveBackupFolder(selected)
		if err != nil {
			return backupPlan{}, fmt.Errorf("Источник копии недоступен: %w", err)
		}
		for _, path := range private {
			if real, err := filepath.EvalSymlinks(path); err == nil && containsBackupPath(folder.FullPath, real) {
				rel, _ := filepath.Rel(folder.FullPath, real)
				if !protectedRelativePath(rel) {
					return backupPlan{}, fmt.Errorf("Выберите папку с файлами вне каталога данных сервера.")
				}
			}
		}
		plan.SameVolume = plan.SameVolume || folder.Device == destination.Device
		for _, added := range plan.Folders {
			if (folder.Device == added.Device && folder.Inode == added.Inode) ||
				containsBackupPath(added.FullPath, folder.FullPath) || containsBackupPath(folder.FullPath, added.FullPath) {
				return backupPlan{}, fmt.Errorf("Выберите папки без повторов и вложенных друг в друга путей.")
			}
		}
		settings.Folders[i] = folder.backupFolder
		plan.Folders = append(plan.Folders, folder)
	}
	if info, err := os.Lstat(filepath.Join(destination.FullPath, managedBackupDirectory)); err == nil {
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !info.IsDir() || info.Mode().Perm()&0077 != 0 || !ok || owner.Uid != uint32(os.Geteuid()) {
			return backupPlan{}, fmt.Errorf("Служебная папка копий в этом месте недоступна для безопасной записи.")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return backupPlan{}, fmt.Errorf("Не удалось проверить папку назначения.")
	}
	// Probe the selected directory without creating a backup before opt-in.
	probe, err := os.CreateTemp(destination.FullPath, ".filehub-write-check-*")
	if err != nil {
		return backupPlan{}, fmt.Errorf("В папку назначения нельзя записать данные.")
	}
	closeErr := probe.Close()
	removeErr := os.Remove(probe.Name())
	if closeErr != nil || removeErr != nil {
		return backupPlan{}, fmt.Errorf("Не удалось завершить проверку папки назначения.")
	}
	return plan, nil
}

// Scan the seven-day calendar in the server's time zone. A persisted slot
// prevents duplicate runs on restart or when the clock is set back.
func backupSlot(settings backupSettings, now time.Time, future bool) time.Time {
	clock, err := time.Parse("15:04", settings.Time)
	if err != nil {
		return time.Time{}
	}
	direction := -1
	if future {
		direction = 1
	}
	for offset := 0; offset <= 7; offset++ {
		date := now.AddDate(0, 0, direction*offset)
		for _, day := range settings.Days {
			if day == date.Weekday() {
				slot := time.Date(date.Year(), date.Month(), date.Day(), clock.Hour(), clock.Minute(), 0, 0, now.Location())
				if slot.After(now) == future {
					return slot
				}
				break
			}
		}
	}
	return time.Time{}
}

func (b *managedBackup) response(w http.ResponseWriter) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	zone, _ := now.Zone()
	response := map[string]any{
		"settings": b.current.Settings, "timeZone": zone, "lastRun": b.current.Run,
		"sameVolume": b.current.Plan.SameVolume, "legacyDatabaseSnapshots": b.current.Legacy,
		"lastRunMatchesSettings": b.current.Run.SettingsRevision == b.current.Revision,
	}
	if b.current.Settings.Enabled {
		response["nextRun"] = backupSlot(b.current.Settings, now, true)
	}
	writeJSON(w, response)
}

func (b *managedBackup) settings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		b.response(w)
		return
	}
	requireAdminMutation(b.update)(w, r)
}

func (b *managedBackup) update(w http.ResponseWriter, r *http.Request) {
	var settings backupSettings
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&settings) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) {
		http.Error(w, "Некорректные настройки копирования.", http.StatusBadRequest)
		return
	}
	if settings.Folders == nil {
		settings.Folders = []backupFolder{}
	}
	if settings.Days == nil {
		settings.Days = []time.Weekday{}
	}
	plan, err := resolveBackupPlan(&settings)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	b.mu.Lock()
	next := b.current
	plan.Instance = next.Instance
	next.Settings, next.Plan, next.Legacy, next.SavedAt = settings, plan, false, time.Now().Unix()
	next.Revision++
	err = b.save(next)
	b.mu.Unlock()
	if err != nil {
		log.Printf("filehub: persist backup settings: %v", err)
		http.Error(w, "Не удалось сохранить настройки.", http.StatusInternalServerError)
		return
	}
	b.response(w)
}

func (b *managedBackup) schedule() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		b.start(false)
		<-ticker.C
	}
}

func (b *managedBackup) run(w http.ResponseWriter, _ *http.Request) {
	if err := b.start(true); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	b.response(w)
}

func (b *managedBackup) start(manual bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	next := b.current
	if !next.Settings.Enabled || next.Run.State == "running" {
		return fmt.Errorf("Копирование выключено или уже выполняется.")
	}
	now := time.Now()
	slot := backupSlot(next.Settings, now, false).Unix()
	if !manual && slot <= max(next.SavedAt, next.LastSlot) {
		return nil
	}
	if !manual {
		next.LastSlot = slot
	}
	next.Run = backupRun{State: "running", Started: now.Unix(), SettingsRevision: next.Revision}
	if err := b.save(next); err != nil {
		log.Printf("filehub: persist backup start: %v", err)
		return fmt.Errorf("Не удалось зарегистрировать запуск копирования.")
	}
	go b.copy(next.Plan, next.Settings, next.Run)
	return nil
}

// Re-resolve saved choices against the active source configuration. A database
// restored to a different installation must not start copying the old server's
// paths simply because those paths still exist.
func currentBackupPlan(plan backupPlan, settings backupSettings) (backupPlan, error) {
	database, err := filepath.Abs(dbPath())
	if err != nil || database != plan.Database {
		return plan, fmt.Errorf("База сервера изменилась. Сохраните настройки копирования заново.")
	}
	destination, err := resolveBackupFolder(settings.Destination)
	if err != nil || destination.backupDirectory != plan.Destination {
		return plan, fmt.Errorf("Место хранения копии недоступно или заменено. Проверьте назначение.")
	}
	for _, saved := range plan.Folders {
		folder, err := resolveBackupFolder(saved.backupFolder)
		if err != nil || folder.backupDirectory != saved.backupDirectory {
			return plan, fmt.Errorf("Папка для копирования недоступна или заменена. Проверьте выбранные папки.")
		}
	}
	roots := make(map[string]backupDirectory, len(plan.Roots))
	for name, saved := range plan.Roots {
		folder, err := resolveBackupFolder(backupFolder{Source: name})
		if err == nil && folder.backupDirectory == saved {
			roots[name] = saved
		}
	}
	plan.Roots = roots
	return plan, nil
}

func executeBackup(plan backupPlan, run *backupRun) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour+time.Minute)
	defer cancel()
	input, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, "python3", "-c", originalBackupWorker, "--configured")
	command.Stdin = bytes.NewReader(input)
	output, err := command.Output()
	if err != nil {
		return err
	}
	return json.Unmarshal(output, run)
}

func (b *managedBackup) copy(plan backupPlan, settings backupSettings, run backupRun) {
	plan, err := currentBackupPlan(plan, settings)
	if err != nil {
		run.Message = err.Error()
	} else if err = executeBackup(plan, &run); err != nil {
		run.Message = "Копия не завершена. Проверьте доступность папок, место и журнал сервера."
	}
	run.Completed = time.Now().Unix()
	run.State = "ok"
	if err != nil {
		log.Printf("filehub: configured backup failed: %v", err)
		run.State = "error"
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	next := b.current
	next.Run = run
	if run.State == "ok" {
		next.LastCopy = &backupCopy{Plan: plan, Run: run}
	}
	if err := b.save(next); err != nil {
		b.current.Run = run
		log.Printf("filehub: persist backup result: %v", err)
	}
}
