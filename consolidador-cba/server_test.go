package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeDesk struct {
	folder, saveTo string
	opened         []string
}

func (d *fakeDesk) pickFolder(string) (string, error) {
	if d.folder == "" {
		return "", errCancelled
	}
	return d.folder, nil
}
func (d *fakeDesk) saveFile(name, dir, kind string) (string, error) {
	if d.saveTo == "" {
		return "", errCancelled
	}
	return filepath.Join(d.saveTo, name), nil
}
func (d *fakeDesk) open(p string) error   { d.opened = append(d.opened, "open "+p); return nil }
func (d *fakeDesk) reveal(p string) error { d.opened = append(d.opened, "reveal "+p); return nil }

func write(t *testing.T, path, body string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCommand(t *testing.T) {
	for args, want := range map[string]string{
		"": "", "--instalar": "instalar", "/instalar": "instalar", "-INSTALAR": "instalar", "--install": "instalar",
		"--desinstalar": "desinstalar", "/uninstall": "desinstalar", "Y:\\DadosCBA": "",
	} {
		if got := command(strings.Fields(args)); got != want {
			t.Errorf("command(%q) = %q, want %q", args, got, want)
		}
	}
}

func TestScanFolder(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "b.xlsx"), "b")
	write(t, filepath.Join(root, "A.XLSM"), "a")
	write(t, filepath.Join(root, "~$b.xlsx"), "lock")
	write(t, filepath.Join(root, "notas.txt"), "x")
	write(t, filepath.Join(root, "sub", "c.xlsx"), "c")

	res, paths, err := scanFolder(root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range res.Files {
		got = append(got, f.Rel)
		if paths[f.ID] != filepath.Join(root, filepath.FromSlash(f.Rel)) {
			t.Errorf("id %s points to %s", f.ID, paths[f.ID])
		}
		if f.Temp != strings.HasPrefix(f.Name, "~$") {
			t.Errorf("%s: temp = %v", f.Rel, f.Temp)
		}
	}
	if want := "A.XLSM b.xlsx sub/c.xlsx ~$b.xlsx"; strings.Join(got, " ") != want {
		t.Errorf("files = %q, want %q", strings.Join(got, " "), want)
	}
	if res.Other != 1 {
		t.Errorf("other = %d, want 1", res.Other)
	}
	if _, _, err := scanFolder(filepath.Join(root, "nope")); !os.IsNotExist(err) {
		t.Errorf("missing folder: err = %v", err)
	}
}

func newTestApp(t *testing.T, desk desktop) (*app, http.Handler) {
	a := newApp("127.0.0.1:1", filepath.Join(t.TempDir(), "cfg", "config.json"), desk)
	return a, a.handler()
}

func do(h http.Handler, a *app, method, target string, body io.Reader, token bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, body)
	r.Host = a.host
	if token {
		r.Header.Set("X-Token", a.token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestGuard(t *testing.T) {
	a, h := newTestApp(t, &fakeDesk{})
	if w := do(h, a, "GET", "/", nil, false); w.Code != 200 || !strings.Contains(w.Body.String(), a.token) {
		t.Fatalf("page: %d, token in page: %v", w.Code, strings.Contains(w.Body.String(), a.token))
	}
	if w := do(h, a, "GET", "/api/config", nil, false); w.Code != 403 {
		t.Errorf("api without token: %d", w.Code)
	}
	r := httptest.NewRequest("GET", "/api/config", nil)
	r.Host = "evil.example:1" // DNS rebinding: right address, wrong name
	r.Header.Set("X-Token", a.token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Errorf("wrong host: %d", w.Code)
	}
	if w := do(h, a, "GET", "/ui/app.js", nil, false); w.Code != 200 {
		t.Errorf("static: %d", w.Code)
	}
	if w := do(h, a, "GET", "/ui/../server.go", nil, false); w.Code == 200 {
		t.Errorf("served a file outside ui/")
	}
}

func TestScanReadExport(t *testing.T) {
	root := t.TempDir()
	out := t.TempDir()
	write(t, filepath.Join(root, "dados.xlsx"), "PK conteudo")
	desk := &fakeDesk{folder: root, saveTo: out}
	a, h := newTestApp(t, desk)

	w := do(h, a, "POST", "/api/pick-folder", nil, true)
	var pick struct{ Path string }
	json.Unmarshal(w.Body.Bytes(), &pick)
	if pick.Path != root {
		t.Fatalf("pick-folder = %s", w.Body.String())
	}

	w = do(h, a, "GET", "/api/scan?path="+root, nil, true)
	var scan scanResult
	if err := json.Unmarshal(w.Body.Bytes(), &scan); err != nil || len(scan.Files) != 1 {
		t.Fatalf("scan = %d %s", w.Code, w.Body.String())
	}
	if a.config().Folder != root {
		t.Errorf("folder not remembered")
	}

	w = do(h, a, "GET", "/api/file?id="+scan.Files[0].ID, nil, true)
	if w.Body.String() != "PK conteudo" {
		t.Errorf("file = %q", w.Body.String())
	}
	if w := do(h, a, "GET", "/api/file?id=f99", nil, true); w.Code != 404 {
		t.Errorf("unknown id: %d", w.Code)
	}

	w = do(h, a, "POST", "/api/export?kind=xlsx&name=Consolidado.xlsx", strings.NewReader("planilha"), true)
	var saved struct{ Path string }
	json.Unmarshal(w.Body.Bytes(), &saved)
	if data, _ := os.ReadFile(filepath.Join(out, "Consolidado.xlsx")); string(data) != "planilha" || saved.Path == "" {
		t.Fatalf("export = %d %s", w.Code, w.Body.String())
	}
	if a.config().ExportDir != out {
		t.Errorf("export folder not remembered")
	}
	if w := do(h, a, "POST", "/api/export?kind=exe&name=x.exe", strings.NewReader("x"), true); w.Code != 400 {
		t.Errorf("export of another kind: %d", w.Code)
	}

	body, _ := json.Marshal(map[string]string{"path": saved.Path})
	if w := do(h, a, "POST", "/api/open", bytes.NewReader(body), true); w.Code != 200 || len(desk.opened) != 1 {
		t.Errorf("open saved file: %d %v", w.Code, desk.opened)
	}
	body, _ = json.Marshal(map[string]string{"path": filepath.Join(root, "dados.xlsx")})
	if w := do(h, a, "POST", "/api/reveal", bytes.NewReader(body), true); w.Code != 403 {
		t.Errorf("reveal of a file the app did not save: %d", w.Code)
	}

	desk.saveTo = ""
	if w := do(h, a, "POST", "/api/export?kind=csv&name=c.csv", strings.NewReader("x"), true); !strings.Contains(w.Body.String(), "cancelled") {
		t.Errorf("cancelled export = %s", w.Body.String())
	}
}

func TestConfigRoundTrip(t *testing.T) {
	a, h := newTestApp(t, &fakeDesk{})
	a.saveConfig(Config{Folder: `Y:\DadosCBA`, ExportDir: `C:\Saida`})
	body := `{"folder":"C:\\outra","sheet":"Plan1","decimal":".","columns":["Item","AT_X"],"extras":true}`
	if w := do(h, a, "PUT", "/api/config", strings.NewReader(body), true); w.Code != 200 {
		t.Fatalf("put: %d %s", w.Code, w.Body.String())
	}
	b := newApp(a.host, a.cfgPath, &fakeDesk{}) // reads the file again, as the next start would
	c := b.config()
	if c.Folder != `Y:\DadosCBA` || c.ExportDir != `C:\Saida` || c.Sheet != "Plan1" || c.Decimal != "." || len(c.Columns) != 2 || !c.Extras {
		t.Errorf("config after restart = %+v", c)
	}
}
