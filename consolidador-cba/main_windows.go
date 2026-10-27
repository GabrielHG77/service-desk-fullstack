//go:build windows

package main

import (
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/harry1453/go-common-file-dialog/cfd"
	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

func init() { runtime.LockOSThread() } // the window, and the dialogs it owns, live on the main thread

var (
	user32          = windows.NewLazySystemDLL("user32.dll")
	getDpiForSystem = user32.NewProc("GetDpiForSystem")
	getSysMetrics   = user32.NewProc("GetSystemMetrics")
)

func main() {
	switch command(os.Args[1:]) {
	case "instalar":
		if err := install(); err != nil {
			alert("Não foi possível instalar o Consolidador CBA:\n\n" + err.Error())
		}
		return
	case "desinstalar":
		uninstall()
		return
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		alert("Não foi possível iniciar o Consolidador CBA:\n\n" + err.Error())
		return
	}
	desk := &winDesktop{}
	a := newApp(ln.Addr().String(), defaultConfigPath(), desk)
	go (&http.Server{Handler: a.handler(), ReadHeaderTimeout: 10 * time.Second}).Serve(ln)
	url := "http://" + a.host + "/"

	cache, _ := os.UserCacheDir() // %LOCALAPPDATA%: the WebView2 data never lands next to the .exe (often on Y:)
	w, h := windowSize(1280, 820)
	view := webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath:      filepath.Join(cache, "ConsolidadorCBA", "WebView2"),
		AutoFocus:     true,
		WindowOptions: webview2.WindowOptions{Title: "Consolidador CBA", Width: w, Height: h, IconId: 1, Center: true},
	})
	if view == nil {
		// Without the WebView2 runtime the same screen opens in an Edge window, and the program ends when it closes.
		if !openAppWindow(url) {
			alert("O Consolidador CBA precisa do Microsoft Edge WebView2 Runtime, que não foi encontrado neste computador.\n\nPeça para a TI instalar o \"Microsoft Edge WebView2 Runtime\" e abra o app de novo.")
			return
		}
		a.waitForPage(60*time.Second, 30*time.Second)
		return
	}
	defer view.Destroy()
	desk.view = view
	desk.hwnd = uintptr(view.Window())
	mw, mh := windowSize(960, 600)
	view.SetSize(int(mw), int(mh), webview2.HintMin)
	view.Navigate(url)
	view.Run()
}

// windowSize scales a size in 96-dpi pixels to this screen, and keeps it inside the screen.
func windowSize(w, h uint) (uint, uint) {
	dpi := uint(96)
	if getDpiForSystem.Find() == nil {
		if d, _, _ := getDpiForSystem.Call(); d > 0 {
			dpi = uint(d)
		}
	}
	w, h = w*dpi/96, h*dpi/96
	sw, _, _ := getSysMetrics.Call(0) // SM_CXSCREEN
	sh, _, _ := getSysMetrics.Call(1) // SM_CYSCREEN
	if sw > 0 && w > uint(sw)*9/10 {
		w = uint(sw) * 9 / 10
	}
	if sh > 0 && h > uint(sh)*85/100 {
		h = uint(sh) * 85 / 100
	}
	return w, h
}

type winDesktop struct {
	view webview2.WebView
	hwnd uintptr
}

// onUI runs f where a dialog belongs: on the window's thread, or, with no window, alone on a thread of its own.
func (d *winDesktop) onUI(f func()) {
	done := make(chan struct{})
	run := func() { defer close(done); f() }
	if d.view != nil {
		d.view.Dispatch(run)
	} else {
		go func() { runtime.LockOSThread(); run() }() // the thread ends with the goroutine, COM state and all
	}
	<-done
}

func existingDir(p string) string {
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		return p
	}
	return ""
}

func (d *winDesktop) pickFolder(start string) (path string, err error) {
	conf := cfd.DialogConfig{Title: "Escolha a pasta com os arquivos Excel", Role: "ConsolidadorCBA.Pasta", ParentWindowHandle: d.hwnd}
	if existingDir(start) != "" {
		conf.Folder, conf.FileName = filepath.Dir(start), filepath.Base(start)
	}
	d.onUI(func() {
		dlg, e := cfd.NewSelectFolderDialog(conf)
		if e != nil {
			err = e
			return
		}
		defer dlg.Release()
		path, err = dlg.ShowAndGetResult()
	})
	if errors.Is(err, cfd.ErrorCancelled) {
		return "", errCancelled
	}
	return path, err
}

var filters = map[string][]cfd.FileFilter{
	"xlsx": {{DisplayName: "Pasta de Trabalho do Excel (*.xlsx)", Pattern: "*.xlsx"}},
	"csv":  {{DisplayName: "CSV separado por ponto e vírgula (*.csv)", Pattern: "*.csv"}},
}

func (d *winDesktop) saveFile(name, dir, kind string) (path string, err error) {
	conf := cfd.DialogConfig{
		Title: "Salvar consolidado", Role: "ConsolidadorCBA.Exportar", Folder: existingDir(dir), FileName: name,
		FileFilters: filters[kind], DefaultExtension: kind, ParentWindowHandle: d.hwnd,
	}
	d.onUI(func() {
		dlg, e := cfd.NewSaveFileDialog(conf)
		if e != nil {
			err = e
			return
		}
		defer dlg.Release()
		path, err = dlg.ShowAndGetResult()
	})
	if errors.Is(err, cfd.ErrorCancelled) {
		return "", errCancelled
	}
	return path, err
}

func (d *winDesktop) open(path string) error {
	verb, _ := windows.UTF16PtrFromString("open")
	file, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.ShellExecute(windows.Handle(d.hwnd), verb, file, nil, nil, windows.SW_SHOWNORMAL)
}

func (d *winDesktop) reveal(path string) error {
	cmd := exec.Command("explorer.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `explorer.exe /select,"` + path + `"`} // explorer wants the quotes after the comma
	return cmd.Start()
}

// openAppWindow opens url in an Edge (or Chrome) window without tabs or address bar.
func openAppWindow(url string) bool {
	for _, dir := range []string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles"), os.Getenv("LocalAppData")} {
		if dir == "" {
			continue
		}
		for _, exe := range []string{`Microsoft\Edge\Application\msedge.exe`, `Google\Chrome\Application\chrome.exe`} {
			p := filepath.Join(dir, exe)
			if _, err := os.Stat(p); err == nil && exec.Command(p, "--app="+url, "--window-size=1280,820").Start() == nil {
				return true
			}
		}
	}
	return false
}

func alert(text string) {
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString("Consolidador CBA")
	windows.MessageBox(0, t, c, windows.MB_OK|windows.MB_ICONWARNING)
}
