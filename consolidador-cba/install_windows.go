//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Installation for the current user only, so it needs no administrator. The program stays where it is
// (the folder the install command downloaded it to); --instalar adds shortcuts in the Start menu and on the
// Desktop and an entry in Settings > Apps that runs --desinstalar.

const uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\ConsolidadorCBA`

// defaultInstallDir is the folder the first install command used; only there may uninstall remove the folder itself.
func defaultInstallDir() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "ConsolidadorCBA")
}

func samePath(a, b string) bool { return strings.EqualFold(filepath.Clean(a), filepath.Clean(b)) }

func shortcutPaths() []string {
	var out []string
	for _, id := range []*windows.KNOWNFOLDERID{windows.FOLDERID_Programs, windows.FOLDERID_Desktop} {
		if dir, err := windows.KnownFolderPath(id, 0); err == nil {
			out = append(out, filepath.Join(dir, "Consolidador CBA.lnk"))
		}
	}
	return out
}

// installedAt is the folder of the current installation, from Settings > Apps, or "".
func installedAt() string {
	k, err := registry.OpenKey(registry.CURRENT_USER, uninstallKey, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	dir, _, _ := k.GetStringValue("InstallLocation")
	return dir
}

func install() error {
	target, err := os.Executable()
	if err != nil {
		return err
	}
	dir := filepath.Dir(target)
	// Installing in another folder leaves no second copy behind in the old one.
	if old := installedAt(); old != "" && !samePath(old, dir) {
		os.Remove(filepath.Join(old, "ConsolidadorCBA.exe"))
		if samePath(old, defaultInstallDir()) {
			os.Remove(old) // only if empty
		}
	}
	var problems []string
	for _, lnk := range shortcutPaths() {
		if err := makeShortcut(lnk, target, dir); err != nil {
			problems = append(problems, "atalho "+lnk+": "+err.Error())
		}
	}
	if err := register(target, dir); err != nil {
		problems = append(problems, "registro em Aplicativos instalados: "+err.Error())
	}
	if len(problems) > 0 {
		alert("O Consolidador CBA está em " + dir + ", mas faltou:\n\n" + strings.Join(problems, "\n"))
	}
	run := exec.Command(target)
	run.Dir = dir
	return run.Start()
}

func makeShortcut(lnk, target, dir string) error {
	ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED)
	defer ole.CoUninitialize()
	unknown, err := oleutil.CreateObject("WScript.Shell")
	if err != nil {
		return err
	}
	defer unknown.Release()
	shell, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return err
	}
	defer shell.Release()
	v, err := oleutil.CallMethod(shell, "CreateShortcut", lnk)
	if err != nil {
		return err
	}
	sc := v.ToIDispatch()
	defer sc.Release()
	for name, value := range map[string]string{
		"TargetPath": target, "WorkingDirectory": dir, "IconLocation": target + ",0",
		"Description": "Junta a aba Data dos arquivos Excel da pasta DadosCBA",
	} {
		if _, err := oleutil.PutProperty(sc, name, value); err != nil {
			return err
		}
	}
	_, err = oleutil.CallMethod(sc, "Save")
	return err
}

func register(target, dir string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, uninstallKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	size := uint32(0)
	if fi, err := os.Stat(target); err == nil {
		size = uint32(fi.Size() / 1024)
	}
	for name, value := range map[string]string{
		"DisplayName": "Consolidador CBA", "DisplayVersion": version, "DisplayIcon": target,
		"InstallLocation": dir, "UninstallString": `"` + target + `" --desinstalar`,
	} {
		if err := k.SetStringValue(name, value); err != nil {
			return err
		}
	}
	for name, value := range map[string]uint32{"NoModify": 1, "NoRepair": 1, "EstimatedSize": size} {
		if err := k.SetDWordValue(name, value); err != nil {
			return err
		}
	}
	return nil
}

// uninstall removes the shortcuts, the Settings > Apps entry, the saved settings and ConsolidadorCBA.exe.
// It never removes the folder the program is in, other than the default one when it is left empty:
// that folder may hold other programs (mqtt-panel, FatorImportacao).
func uninstall() {
	if !ask("Desinstalar o Consolidador CBA?\n\nO programa, os atalhos e os ajustes salvos são apagados. As planilhas e os outros arquivos da pasta não são tocados.") {
		return
	}
	for _, lnk := range shortcutPaths() {
		os.Remove(lnk)
	}
	registry.DeleteKey(registry.CURRENT_USER, uninstallKey)
	os.RemoveAll(filepath.Dir(defaultConfigPath()))
	if cache, err := os.UserCacheDir(); err == nil {
		os.RemoveAll(filepath.Join(cache, "ConsolidadorCBA"))
	}
	alert("O Consolidador CBA foi desinstalado.")
	// The running program cannot delete itself: a hidden cmd deletes it a moment after this process ends.
	exe, err := os.Executable()
	if err != nil {
		return
	}
	line := `cmd.exe /c ping -n 3 127.0.0.1 >nul & del /f /q "` + exe + `"`
	if dir := filepath.Dir(exe); samePath(dir, defaultInstallDir()) {
		line += ` & rmdir "` + dir + `"` // no /s: only when empty
	}
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: line, HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	cmd.Start()
}

func ask(text string) bool {
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString("Consolidador CBA")
	r, _ := windows.MessageBox(0, t, c, windows.MB_YESNO|windows.MB_ICONQUESTION)
	return r == 6 // IDYES
}
