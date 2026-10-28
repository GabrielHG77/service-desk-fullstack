// Consolidador CBA: a window showing the screen in ui/, plus what a page cannot do by itself: remember the folder,
// read its Excel files from disk, and save the result where the user picks with the Windows dialogs.
// The screen talks to this program over 127.0.0.1, guarded by the Host header and a token only the page knows.
package main

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

//go:embed ui
var uiFS embed.FS

// Config is what the app remembers between runs, in %APPDATA%\ConsolidadorCBA\config.json.
type Config struct {
	Folder    string   `json:"folder"`
	ExportDir string   `json:"exportDir"`
	Sheet     string   `json:"sheet"`
	Decimal   string   `json:"decimal"`
	Columns   []string `json:"columns"`
	Extras    bool     `json:"extras"`
	Repeated  string   `json:"repeated"`
	Planilha  string   `json:"planilha"` // the consolidated spreadsheet the app keeps up to date
}

type fileInfo struct {
	ID       string `json:"id"`
	Rel      string `json:"rel"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
	Temp     bool   `json:"temp"`
}

type scanResult struct {
	Folder    string     `json:"folder"`
	Files     []fileInfo `json:"files"`
	Other     int        `json:"other"`
	Truncated bool       `json:"truncated"`
}

// desktop is what only the operating system can do: its dialogs and opening files in their programs.
type desktop interface {
	pickFolder(start string) (string, error)
	saveFile(title, name, dir, kind string, overwrite bool) (string, error)
	open(path string) error
	reveal(path string) error
}

var errCancelled = errors.New("cancelled")

const version = "1.3.0"

// command is what the arguments ask for instead of opening the window: "instalar", "desinstalar" or "".
func command(args []string) string {
	for _, a := range args {
		switch strings.ToLower(strings.TrimLeft(a, "-/")) {
		case "instalar", "install":
			return "instalar"
		case "desinstalar", "uninstall":
			return "desinstalar"
		}
	}
	return ""
}

// maxEntries stops a scan of a whole drive picked by mistake.
const maxEntries = 50000

// scanFolder lists the .xlsx and .xlsm files under root, subfolders included, like Folder.Files in the query.
// The paths in ignore (the consolidated spreadsheet and its copy, when they live in the folder) are left out.
func scanFolder(root string, ignore ...string) (scanResult, map[string]string, error) {
	res := scanResult{Folder: root, Files: []fileInfo{}}
	info, err := os.Stat(root)
	if err != nil {
		return res, nil, err
	}
	if !info.IsDir() {
		return res, nil, fmt.Errorf("%s não é uma pasta", root)
	}
	paths := map[string]string{}
	seen := 0
	errStop := errors.New("stop")
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			if d != nil && d.IsDir() {
				return fs.SkipDir // a subfolder without permission does not stop the rest
			}
			return nil
		}
		if seen++; seen > maxEntries {
			res.Truncated = true
			return errStop
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasPrefix(d.Name(), "~cba-gravando-") {
			return nil
		}
		for _, skip := range ignore {
			if skip != "" && strings.EqualFold(filepath.Clean(skip), filepath.Clean(p)) {
				return nil
			}
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if ext != ".xlsx" && ext != ".xlsm" {
			res.Other++
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		id := "f" + strconv.Itoa(len(res.Files)+1)
		res.Files = append(res.Files, fileInfo{
			ID: id, Rel: filepath.ToSlash(rel), Name: d.Name(), Size: fi.Size(),
			Modified: fi.ModTime().UnixMilli(), Temp: strings.HasPrefix(d.Name(), "~$"),
		})
		paths[id] = p
		return nil
	})
	if err != nil && err != errStop {
		return res, nil, err
	}
	sort.SliceStable(res.Files, func(i, j int) bool {
		return strings.ToLower(res.Files[i].Rel) < strings.ToLower(res.Files[j].Rel)
	})
	return res, paths, nil
}

type app struct {
	token   string
	host    string // "127.0.0.1:port", the only Host the page is served under (blocks DNS rebinding)
	cfgPath string
	desk    desktop
	pinged  atomic.Int64 // unix ms of the last sign of life from the page

	mu       sync.Mutex
	cfg      Config
	files    map[string]string // id -> path, from the last scan
	exported map[string]bool   // files this app saved: the only ones it opens or shows in Explorer
	writing  sync.Mutex        // one write to the spreadsheet at a time
}

func (a *app) allow(path string) {
	a.mu.Lock()
	a.exported[path] = true
	a.mu.Unlock()
}

// answerPlanilha tells the screen which items the spreadsheet already has; a spreadsheet that cannot be read
// comes back with its error, so the screen can say what is wrong with it.
func (a *app) answerPlanilha(w http.ResponseWriter, path string) {
	if path == "" {
		writeJSON(w, http.StatusOK, planilhaInfo{Keys: map[string]string{}})
		return
	}
	info, err := readPlanilha(path)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"path": path, "exists": info.Exists, "keys": map[string]string{}, "error": err.Error()})
		return
	}
	a.allow(path)
	writeJSON(w, http.StatusOK, info)
}

func planilhaFailure(err error) string {
	if sharingViolation(err) {
		return "A planilha está aberta no Excel. Feche-a e grave de novo."
	}
	if errors.Is(err, fs.ErrPermission) {
		return "Sem permissão para gravar na planilha (ela pode estar aberta no Excel)."
	}
	return "Não foi possível gravar na planilha: " + err.Error()
}

func newApp(host, cfgPath string, desk desktop) *app {
	b := make([]byte, 16)
	rand.Read(b)
	a := &app{token: hex.EncodeToString(b), host: host, cfgPath: cfgPath, desk: desk, files: map[string]string{}, exported: map[string]bool{}}
	if data, err := os.ReadFile(cfgPath); err == nil {
		json.Unmarshal(data, &a.cfg)
	}
	a.pinged.Store(time.Now().UnixMilli())
	return a
}

func defaultConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "ConsolidadorCBA", "config.json")
}

func (a *app) saveConfig(c Config) error {
	a.mu.Lock()
	a.cfg = c
	a.mu.Unlock()
	data, _ := json.MarshalIndent(c, "", "  ")
	if err := os.MkdirAll(filepath.Dir(a.cfgPath), 0o755); err != nil {
		return err
	}
	tmp := a.cfgPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, a.cfgPath)
}

func (a *app) config() Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg
}

const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'none'"

var staticTypes = map[string]string{
	"app.js": "text/javascript; charset=utf-8", "core.js": "text/javascript; charset=utf-8",
	"xlsx.full.min.js": "text/javascript; charset=utf-8", "style.css": "text/css; charset=utf-8", "icon.png": "image/png",
}

func (a *app) handler() http.Handler {
	mux := http.NewServeMux()
	index, _ := uiFS.ReadFile("ui/index.html")
	page := bytes.Replace(index, []byte("{{TOKEN}}"), []byte(a.token), 1)
	page = bytes.ReplaceAll(page, []byte("{{VERSION}}"), []byte(strings.TrimSuffix(version, ".0")))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("Cache-Control", "no-store")
		w.Write(page)
	})
	mux.HandleFunc("GET /ui/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		typ, ok := staticTypes[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		body, _ := uiFS.ReadFile("ui/" + name)
		w.Header().Set("Content-Type", typ)
		w.Header().Set("Cache-Control", "no-store")
		w.Write(body)
	})

	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, a.config())
	})
	mux.HandleFunc("PUT /api/config", func(w http.ResponseWriter, r *http.Request) {
		var c Config
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&c); err != nil {
			writeError(w, http.StatusBadRequest, "Configuração inválida.")
			return
		}
		old := a.config()
		c.Folder, c.ExportDir, c.Planilha = old.Folder, old.ExportDir, old.Planilha // these change only through the dialogs and the scan
		if err := a.saveConfig(c); err != nil {
			writeError(w, http.StatusInternalServerError, "Não foi possível guardar os ajustes: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, c)
	})
	mux.HandleFunc("POST /api/pick-folder", func(w http.ResponseWriter, r *http.Request) {
		p, err := a.desk.pickFolder(a.config().Folder)
		if errors.Is(err, errCancelled) {
			writeJSON(w, http.StatusOK, map[string]any{"cancelled": true})
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Não foi possível abrir a janela de pastas: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"path": p})
	})
	mux.HandleFunc("GET /api/scan", func(w http.ResponseWriter, r *http.Request) {
		root := r.URL.Query().Get("path")
		if root == "" {
			root = a.config().Folder
		}
		if root == "" {
			writeError(w, http.StatusNotFound, "Nenhuma pasta escolhida.")
			return
		}
		pl := a.config().Planilha
		res, paths, err := scanFolder(root, pl, backupPath(pl))
		if err != nil {
			msg := "Não foi possível ler a pasta " + root + "."
			if errors.Is(err, fs.ErrNotExist) {
				msg = "A pasta " + root + " não foi encontrada. Se ela fica no drive Y:, confira se ele está conectado."
			} else if errors.Is(err, fs.ErrPermission) {
				msg = "Sem permissão para ler a pasta " + root + "."
			}
			writeError(w, http.StatusNotFound, msg)
			return
		}
		a.mu.Lock()
		a.files = paths
		a.mu.Unlock()
		c := a.config()
		c.Folder = root
		a.saveConfig(c)
		writeJSON(w, http.StatusOK, res)
	})
	mux.HandleFunc("GET /api/file", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		p, ok := a.files[r.URL.Query().Get("id")]
		a.mu.Unlock()
		if !ok {
			writeError(w, http.StatusNotFound, "Arquivo fora da última leitura da pasta.")
			return
		}
		f, err := os.Open(p)
		if err != nil {
			writeError(w, http.StatusConflict, readFailure(err))
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Cache-Control", "no-store")
		io.Copy(w, f)
	})
	mux.HandleFunc("POST /api/export", func(w http.ResponseWriter, r *http.Request) {
		kind := r.URL.Query().Get("kind")
		name := filepath.Base(r.URL.Query().Get("name"))
		if kind != "xlsx" && kind != "csv" || !strings.HasSuffix(strings.ToLower(name), "."+kind) {
			writeError(w, http.StatusBadRequest, "Tipo de arquivo inválido.")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 512<<20))
		if err != nil {
			writeError(w, http.StatusBadRequest, "O arquivo não chegou inteiro.")
			return
		}
		c := a.config()
		dir := c.ExportDir
		if dir == "" {
			dir = c.Folder
		}
		p, err := a.desk.saveFile("Salvar uma cópia do consolidado", name, dir, kind, true)
		if errors.Is(err, errCancelled) {
			writeJSON(w, http.StatusOK, map[string]any{"cancelled": true})
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Não foi possível abrir a janela de salvar: "+err.Error())
			return
		}
		if err := os.WriteFile(p, body, 0o644); err != nil {
			writeError(w, http.StatusConflict, writeFailure(err))
			return
		}
		a.mu.Lock()
		a.exported[p] = true
		a.mu.Unlock()
		c = a.config()
		c.ExportDir = filepath.Dir(p)
		a.saveConfig(c)
		writeJSON(w, http.StatusOK, map[string]any{"path": p})
	})
	for _, action := range []string{"open", "reveal"} {
		mux.HandleFunc("POST /api/"+action, func(w http.ResponseWriter, r *http.Request) {
			var req struct{ Path string }
			json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req)
			a.mu.Lock()
			ok := a.exported[req.Path]
			a.mu.Unlock()
			if !ok {
				writeError(w, http.StatusForbidden, "Só dá para abrir arquivos salvos por este app.")
				return
			}
			do := a.desk.open
			if action == "reveal" {
				do = a.desk.reveal
			}
			if err := do(req.Path); err != nil {
				writeError(w, http.StatusInternalServerError, "Não foi possível abrir: "+err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{})
		})
	}
	mux.HandleFunc("GET /api/planilha", func(w http.ResponseWriter, r *http.Request) {
		a.answerPlanilha(w, a.config().Planilha)
	})
	mux.HandleFunc("POST /api/planilha/escolher", func(w http.ResponseWriter, r *http.Request) {
		c := a.config()
		name, dir := "Consolidado_CBA.xlsx", filepath.Dir(c.Folder) // beside DadosCBA, not inside it
		if c.Planilha != "" {
			name, dir = filepath.Base(c.Planilha), filepath.Dir(c.Planilha)
		}
		p, err := a.desk.saveFile("Escolha a planilha consolidada (uma que já existe ou um nome novo)", name, dir, "xlsx", false)
		if errors.Is(err, errCancelled) {
			writeJSON(w, http.StatusOK, map[string]any{"cancelled": true})
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Não foi possível abrir a janela de escolher a planilha: "+err.Error())
			return
		}
		c = a.config()
		c.Planilha = p
		a.saveConfig(c)
		a.answerPlanilha(w, p)
	})
	mux.HandleFunc("POST /api/planilha/gravar", func(w http.ResponseWriter, r *http.Request) {
		p := a.config().Planilha
		if p == "" {
			writeError(w, http.StatusBadRequest, "Escolha a planilha primeiro.")
			return
		}
		var req gravarReq
		if err := json.NewDecoder(io.LimitReader(r.Body, 512<<20)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "Os dados não chegaram inteiros.")
			return
		}
		a.writing.Lock()
		res, err := writePlanilha(p, req)
		a.writing.Unlock()
		if err != nil {
			writeError(w, http.StatusConflict, planilhaFailure(err))
			return
		}
		a.allow(p)
		writeJSON(w, http.StatusOK, res)
	})
	mux.HandleFunc("POST /api/planilha/organizar", func(w http.ResponseWriter, r *http.Request) {
		p := a.config().Planilha
		if p == "" {
			writeError(w, http.StatusBadRequest, "Escolha a planilha primeiro.")
			return
		}
		var req organizarReq
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "Pedido inválido.")
			return
		}
		a.writing.Lock()
		res, err := organizePlanilha(p, req)
		a.writing.Unlock()
		if err != nil {
			writeError(w, http.StatusConflict, planilhaFailure(err))
			return
		}
		a.allow(p)
		writeJSON(w, http.StatusOK, res)
	})
	mux.HandleFunc("POST /api/ping", func(w http.ResponseWriter, r *http.Request) {
		a.pinged.Store(time.Now().UnixMilli())
		w.WriteHeader(http.StatusNoContent)
	})
	return a.guard(mux)
}

// guard serves only to the page this program opened: the right Host, and on /api/ the token written into it.
func (a *app) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != a.host {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Token")), []byte(a.token)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// waitForPage returns once the page has stopped saying it is open (used when it runs in an Edge window).
func (a *app) waitForPage(grace, quiet time.Duration) {
	start := time.Now()
	for {
		time.Sleep(2 * time.Second)
		last := time.UnixMilli(a.pinged.Load())
		if time.Since(start) > grace && time.Since(last) > quiet {
			return
		}
	}
}

// sharingViolation is ERROR_SHARING_VIOLATION: on Windows, the file is open in another program (usually Excel).
func sharingViolation(err error) bool {
	var errno syscall.Errno
	return runtime.GOOS == "windows" && errors.As(err, &errno) && errno == 32
}

func readFailure(err error) string {
	if sharingViolation(err) {
		return "O arquivo está bloqueado por outro programa."
	}
	return "Não foi possível ler o arquivo: " + err.Error()
}

func writeFailure(err error) string {
	if sharingViolation(err) {
		return "Não foi possível salvar: o arquivo está aberto no Excel. Feche-o e exporte de novo."
	}
	if errors.Is(err, fs.ErrPermission) {
		return "Não foi possível salvar: sem permissão para gravar nessa pasta."
	}
	return "Não foi possível salvar: " + err.Error()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
