package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"pier/internal/apphost"
	"pier/internal/database"
	"pier/internal/store"
)

// databaseView is a project's Database tab: the server, its previews'
// databases, its backups, and whether production was started before the
// database was added, so it doesn't have DATABASE_URL yet.
type databaseView struct {
	Database      *database.Status `json:"database"`
	Backups       []store.Backup   `json:"backups"`
	MemoryChoices []int            `json:"memory_choices"`
	Default       int              `json:"default_memory_mb"`
	Image         string           `json:"image"`
	// Production is what production serves now, if it's a server app.
	Production *databaseProduction `json:"production"`
	// Externals are the project's external URLs, without their passwords,
	// and External the address they connect to; empty when this server
	// makes none.
	Externals []database.ExternalView `json:"externals"`
	External  string                  `json:"external"`
	NightlyAt int                     `json:"nightly_at"` // the hour, server time
	Timezone  string                  `json:"timezone"`
}

type databaseProduction struct {
	Deployment string `json:"deployment"`
	// Connected reports whether production was started after the database
	// was added, so it has DATABASE_URL.
	Connected bool `json:"connected"`
}

// operation is how long a database change may take once it has begun. It
// isn't cut short when the person who asked closes the page: a database
// half made or half restored is worse than one that finishes unwatched.
const operation = 10 * time.Minute

func (a *API) databasesReady(w http.ResponseWriter) bool {
	if a.Databases == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Databases aren't available on this server."})
		return false
	}
	return true
}

func (a *API) databaseProject(w http.ResponseWriter, r *http.Request) (string, bool) {
	name := r.PathValue("project")
	if !a.databasesReady(w) {
		return "", false
	}
	if _, err := a.Store.Project(r.Context(), name); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "there's no project called " + name})
		return "", false
	} else if err != nil {
		a.fail(w, err)
		return "", false
	}
	return name, true
}

// databaseError answers for the errors a database change can end with.
func (a *API) databaseError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, database.ErrNone):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "This project has no database."})
	case errors.Is(err, store.ErrExists):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "This project has a database already."})
	case errors.Is(err, database.ErrNoDatabase):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "There's no database by that name."})
	case errors.Is(err, database.ErrNoExternals):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "This server doesn't make external URLs. Start pierd with -postgres to turn them on."})
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "There's no such backup or URL."})
	default:
		a.Log.Warn("database", "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": sentenceOf(err)})
	}
}

func sentenceOf(err error) string {
	s := strings.TrimSpace(err.Error())
	if s == "" {
		return "It failed."
	}
	s = strings.ToUpper(s[:1]) + s[1:]
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

func (a *API) getDatabase(w http.ResponseWriter, r *http.Request) {
	project, ok := a.databaseProject(w, r)
	if !ok {
		return
	}
	v := databaseView{MemoryChoices: apphost.DatabaseMemoryChoices, Default: database.DefaultMemoryMB, Image: database.Image,
		NightlyAt: database.NightlyAt, Timezone: zoneName(), Backups: []store.Backup{}}
	st, err := a.Databases.Status(r.Context(), project)
	if err != nil && !errors.Is(err, database.ErrNone) {
		a.fail(w, err)
		return
	}
	if err == nil {
		v.Database = &st
		if v.Externals, err = a.Databases.Externals(r.Context(), project); err != nil {
			a.fail(w, err)
			return
		}
	}
	if v.Externals == nil {
		v.Externals = []database.ExternalView{}
	}
	v.External = a.Databases.External
	if v.Backups, err = a.Store.Backups(r.Context(), project); err != nil {
		a.fail(w, err)
		return
	}
	if v.Backups == nil {
		v.Backups = []store.Backup{}
	}
	hide, err := a.valueHider(r, project)
	if err != nil {
		a.fail(w, err)
		return
	}
	for i := range v.Backups {
		v.Backups[i].Error = hide(v.Backups[i].Error)
	}
	if v.Database != nil {
		v.Database.Problem = hide(v.Database.Problem)
		apps, err := a.Store.ProductionApps(r.Context())
		if err != nil {
			a.fail(w, err)
			return
		}
		for _, app := range apps {
			if app.Project != project {
				continue
			}
			if dep, err := a.Store.Deployment(r.Context(), app.Deployment); err == nil {
				v.Production = &databaseProduction{Deployment: dep.ID, Connected: dep.CreatedAt.After(v.Database.CreatedAt)}
			}
		}
	}
	writeJSON(w, http.StatusOK, v)
}

// databaseURL shows a database's DATABASE_URL, which holds its password,
// to members and the owner: production's, or with ?db= a preview's.
func (a *API) databaseURL(w http.ResponseWriter, r *http.Request) {
	project, ok := a.databaseProject(w, r)
	if !ok {
		return
	}
	// The address apps on Pier use; pier db tunnel swaps in its own host.
	u, err := a.Databases.LocalURL(r.Context(), project, r.URL.Query().Get("db"), apphost.DatabaseHost+":"+database.Port)
	if err != nil {
		a.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": u})
}

type databaseRequest struct {
	MemoryMB int `json:"memory_mb"`
}

func readDatabaseRequest(w http.ResponseWriter, r *http.Request) (databaseRequest, bool) {
	var req databaseRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "that isn't JSON"})
		return req, false
	}
	if req.MemoryMB == 0 {
		req.MemoryMB = database.DefaultMemoryMB
	}
	if !database.ValidMemory(req.MemoryMB) {
		fieldError(w, "memory_mb", "Choose one of the memory sizes listed.")
		return req, false
	}
	return req, true
}

// work is the context a database change runs in: the request's values,
// but not its end.
func work(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.Context()), operation)
}

func (a *API) createDatabase(w http.ResponseWriter, r *http.Request) {
	project, ok := a.databaseProject(w, r)
	if !ok {
		return
	}
	req, ok := readDatabaseRequest(w, r)
	if !ok {
		return
	}
	ctx, cancel := work(r)
	defer cancel()
	if err := a.Databases.Create(ctx, project, req.MemoryMB, userFrom(r.Context()).DisplayName()); err != nil {
		a.databaseError(w, err)
		return
	}
	a.getDatabase(w, r)
}

func (a *API) changeDatabase(w http.ResponseWriter, r *http.Request) {
	project, ok := a.databaseProject(w, r)
	if !ok {
		return
	}
	req, ok := readDatabaseRequest(w, r)
	if !ok {
		return
	}
	ctx, cancel := work(r)
	defer cancel()
	if err := a.Databases.SetMemory(ctx, project, req.MemoryMB, userFrom(r.Context()).DisplayName()); err != nil {
		a.databaseError(w, err)
		return
	}
	a.getDatabase(w, r)
}

func (a *API) restartDatabase(w http.ResponseWriter, r *http.Request) {
	project, ok := a.databaseProject(w, r)
	if !ok {
		return
	}
	ctx, cancel := work(r)
	defer cancel()
	if err := a.Databases.Restart(ctx, project, userFrom(r.Context()).DisplayName()); err != nil {
		a.databaseError(w, err)
		return
	}
	a.getDatabase(w, r)
}

func (a *API) deleteDatabase(w http.ResponseWriter, r *http.Request) {
	project, ok := a.databaseProject(w, r)
	if !ok {
		return
	}
	ctx, cancel := work(r)
	defer cancel()
	if err := a.Databases.Delete(ctx, project, userFrom(r.Context()).DisplayName()); err != nil {
		a.databaseError(w, err)
		return
	}
	a.getDatabase(w, r)
}

func (a *API) backUpDatabase(w http.ResponseWriter, r *http.Request) {
	project, ok := a.databaseProject(w, r)
	if !ok {
		return
	}
	ctx, cancel := work(r)
	defer cancel()
	if _, err := a.Store.Database(ctx, project); errors.Is(err, store.ErrNotFound) {
		a.databaseError(w, database.ErrNone)
		return
	}
	by := userFrom(r.Context()).DisplayName()
	all, err := a.Databases.BackupAll(ctx, project, store.BackupManual, by)
	if err != nil {
		a.databaseError(w, err)
		return
	}
	var total int64
	for _, b := range all {
		total += b.Bytes
	}
	a.event(r.Context(), project, "Backed up the databases", size(total), "")
	writeJSON(w, http.StatusCreated, map[string]any{"backups": all, "bytes": total})
}

func (a *API) backupFrom(w http.ResponseWriter, r *http.Request) (store.Backup, bool) {
	project, ok := a.databaseProject(w, r)
	if !ok {
		return store.Backup{}, false
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "There's no such backup."})
		return store.Backup{}, false
	}
	b, err := a.Store.BackupByID(r.Context(), project, id)
	if err != nil {
		a.databaseError(w, err)
		return b, false
	}
	return b, true
}

// deleteBackup removes a backup and its file.
func (a *API) deleteBackup(w http.ResponseWriter, r *http.Request) {
	b, ok := a.backupFrom(w, r)
	if !ok {
		return
	}
	if _, err := a.Databases.DeleteBackup(r.Context(), b.Project, b.ID, userFrom(r.Context()).DisplayName()); err != nil {
		if errors.Is(err, database.ErrBackupRunning) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": sentenceOf(err)})
			return
		}
		a.databaseError(w, err)
		return
	}
	a.getDatabase(w, r)
}

// downloadBackup sends a backup's file, which pg_restore reads.
func (a *API) downloadBackup(w http.ResponseWriter, r *http.Request) {
	b, ok := a.backupFrom(w, r)
	if !ok {
		return
	}
	if b.State != store.BackupDone || b.File == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "That backup didn't finish, so there's no file."})
		return
	}
	f, err := os.Open(a.Databases.BackupPath(b))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "The backup's file is gone from this server."})
		return
	}
	defer f.Close()
	name := fmt.Sprintf("%s-%s.dump", b.Project, b.StartedAt.UTC().Format("2006-01-02-1504"))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, name, b.FinishedAt, f)
}

func (a *API) restoreBackup(w http.ResponseWriter, r *http.Request) {
	b, ok := a.backupFrom(w, r)
	if !ok {
		return
	}
	ctx, cancel := work(r)
	defer cancel()
	if err := a.Databases.Restore(ctx, b.Project, b.ID, userFrom(r.Context()).DisplayName()); err != nil {
		a.databaseError(w, err)
		return
	}
	a.getDatabase(w, r)
}

func readSpec(w http.ResponseWriter, r *http.Request) (database.Spec, bool) {
	var spec database.Spec
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&spec) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "that isn't JSON"})
		return spec, false
	}
	return spec, true
}

// specError answers for a database name or variable that can't be used,
// naming the field.
func (a *API) specError(w http.ResponseWriter, err error) {
	msg := err.Error()
	switch {
	case errors.Is(err, database.ErrNone), errors.Is(err, database.ErrNoDatabase):
		a.databaseError(w, err)
	case strings.Contains(msg, "variable"):
		fieldError(w, "variable", sentenceOf(err))
	case strings.Contains(msg, "name") || strings.Contains(msg, "called"):
		fieldError(w, "name", sentenceOf(err))
	default:
		a.databaseError(w, err)
	}
}

func (a *API) addProjectDatabase(w http.ResponseWriter, r *http.Request) {
	project, ok := a.databaseProject(w, r)
	if !ok {
		return
	}
	spec, ok := readSpec(w, r)
	if !ok {
		return
	}
	ctx, cancel := work(r)
	defer cancel()
	if err := a.Databases.AddDatabase(ctx, project, spec, userFrom(r.Context()).DisplayName()); err != nil {
		a.specError(w, err)
		return
	}
	a.getDatabase(w, r)
}

func (a *API) linkProjectDatabase(w http.ResponseWriter, r *http.Request) {
	project, ok := a.databaseProject(w, r)
	if !ok {
		return
	}
	spec, ok := readSpec(w, r)
	if !ok {
		return
	}
	if err := a.Databases.LinkDatabase(r.Context(), project, r.PathValue("name"), spec.Environments, spec.Variable, userFrom(r.Context()).DisplayName()); err != nil {
		a.specError(w, err)
		return
	}
	a.getDatabase(w, r)
}

func (a *API) dropProjectDatabase(w http.ResponseWriter, r *http.Request) {
	project, ok := a.databaseProject(w, r)
	if !ok {
		return
	}
	ctx, cancel := work(r)
	defer cancel()
	if err := a.Databases.DropDatabase(ctx, project, r.PathValue("name"), userFrom(r.Context()).DisplayName()); err != nil {
		a.databaseError(w, err)
		return
	}
	a.getDatabase(w, r)
}

// createExternal makes an external URL for a database and shows it, with
// its password.
func (a *API) createExternal(w http.ResponseWriter, r *http.Request) {
	project, ok := a.databaseProject(w, r)
	if !ok {
		return
	}
	e, u, err := a.Databases.CreateExternal(r.Context(), project, r.PathValue("name"), userFrom(r.Context()).DisplayName())
	if err != nil {
		a.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"external": e, "url": u})
}

// externalURL shows an external URL again, with its password.
func (a *API) externalURL(w http.ResponseWriter, r *http.Request) {
	project, ok := a.databaseProject(w, r)
	if !ok {
		return
	}
	u, err := a.Databases.ExternalURL(r.Context(), project, r.PathValue("role"))
	if err != nil {
		a.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": u})
}

func (a *API) revokeExternal(w http.ResponseWriter, r *http.Request) {
	project, ok := a.databaseProject(w, r)
	if !ok {
		return
	}
	if err := a.Databases.RevokeExternal(r.Context(), project, r.PathValue("role"), userFrom(r.Context()).DisplayName()); err != nil {
		a.databaseError(w, err)
		return
	}
	a.getDatabase(w, r)
}

// size writes a number of bytes the way the dashboard does.
func size(n int64) string {
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.1f GB", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%d MB", (n+5e5)/1e6)
	case n >= 1e3:
		return fmt.Sprintf("%d KB", (n+500)/1e3)
	}
	return fmt.Sprintf("%d bytes", n)
}

// browseError answers for a failed read of a database's data: what
// Postgres said, when it said something.
func (a *API) browseError(w http.ResponseWriter, err error) {
	var qe *database.QueryError
	if errors.As(err, &qe) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": qe.Message, "detail": qe})
		return
	}
	a.databaseError(w, err)
}

// databaseTables lists the tables in one of the project's databases.
func (a *API) databaseTables(w http.ResponseWriter, r *http.Request) {
	project, ok := a.databaseProject(w, r)
	if !ok {
		return
	}
	tables, err := a.Databases.Tables(r.Context(), project, r.URL.Query().Get("db"))
	if err != nil {
		a.browseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tables": tables})
}

// databaseRows is a page of one table's rows.
func (a *API) databaseRows(w http.ResponseWriter, r *http.Request) {
	project, ok := a.databaseProject(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	offset, _ := strconv.Atoi(q.Get("offset"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit == 0 {
		limit = 50
	}
	page, err := a.Databases.Rows(r.Context(), project, q.Get("db"), r.PathValue("schema"), r.PathValue("table"), q.Get("sort"), q.Get("desc") == "1", q.Get("where"), offset, limit)
	if err != nil {
		a.browseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// runQuery runs SQL in one of the project's databases.
func (a *API) runQuery(w http.ResponseWriter, r *http.Request) {
	project, ok := a.databaseProject(w, r)
	if !ok {
		return
	}
	var req struct {
		DB  string `json:"db"`
		SQL string `json:"sql"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "that isn't JSON"})
		return
	}
	if strings.TrimSpace(req.SQL) == "" {
		fieldError(w, "sql", "Write some SQL to run.")
		return
	}
	out, err := a.Databases.Query(r.Context(), project, req.DB, req.SQL, userFrom(r.Context()).DisplayName())
	if err != nil {
		a.browseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// tunnelProtocol is what pier db tunnel asks to upgrade a request to: after
// pierd's 101, the connection carries Postgres's own protocol to and from
// the database's server, as if the computer were beside it.
const tunnelProtocol = "pier-postgres"

// databaseTunnel joins the request's connection to the project's Postgres
// server. Postgres still asks for the role's password, which pier db
// tunnel gets from databaseURL.
func (a *API) databaseTunnel(w http.ResponseWriter, r *http.Request) {
	project, ok := a.databaseProject(w, r)
	if !ok {
		return
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), tunnelProtocol) {
		writeJSON(w, http.StatusUpgradeRequired, map[string]string{"error": "Connect with pier db tunnel."})
		return
	}
	if _, err := a.Store.Database(r.Context(), project); errors.Is(err, store.ErrNotFound) {
		a.databaseError(w, database.ErrNone)
		return
	}
	addr, err := a.Databases.Host.PostgresAddress(r.Context(), project)
	if err != nil {
		a.databaseError(w, err)
		return
	}
	upstream, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		a.databaseError(w, fmt.Errorf("couldn't reach the database: %w", err))
		return
	}
	defer upstream.Close()
	conn, buf, err := http.NewResponseController(w).Hijack()
	if err != nil {
		a.fail(w, err)
		return
	}
	defer conn.Close()
	conn.SetDeadline(time.Time{})
	io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: "+tunnelProtocol+"\r\nConnection: Upgrade\r\n\r\n")
	done := make(chan struct{}, 2)
	go func() {
		// Anything the client sent with the request comes first.
		if n := buf.Reader.Buffered(); n > 0 {
			b, _ := buf.Reader.Peek(n)
			upstream.Write(b)
		}
		io.Copy(upstream, conn)
		if c, ok := upstream.(*net.TCPConn); ok {
			c.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		io.Copy(conn, upstream)
		if c, ok := conn.(interface{ CloseWrite() error }); ok {
			c.CloseWrite()
		}
		done <- struct{}{}
	}()
	<-done
	<-done
}

// zoneName is this server's time zone as people write it: UTC, WAT, or
// an offset like +0100 when the zone has no abbreviation.
func zoneName() string {
	name, offset := time.Now().Zone()
	if name == "" || name == "Local" {
		return time.Now().Format("-0700")
	}
	if offset == 0 {
		return "UTC"
	}
	return name
}
