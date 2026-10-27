//go:build windows

package main

import (
	"errors"
	"io/fs"
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

// Installation for the current user only, so it needs no administrator:
// the program in %LOCALAPPDATA%\Programs, shortcuts in the Start menu and on the Desktop,
// and an entry in Settings > Apps that runs --desinstalar.

const uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\ConsolidadorCBA`

func installDir() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "ConsolidadorCBA")
}

func shortcutPaths() []string {
	var out []string
	for _, id := range []*windows.KNOWNFOLDERID{windows.FOLDERID_Programs, windows.FOLDERID_Desktop} {
		if dir, err := windows.KnownFolderPath(id, 0); err == nil {
			out = append(out, filepath.Join(dir, "Consolidador CBA.lnk"))
		}
	}
	return out
}

func install() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	dir := installDir()
	target := filepath.Join(dir, "ConsolidadorCBA.exe")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Clean(self), filepath.Clean(target)) {
		if err := copyFile(self, target); err != nil {
			if sharingViolation(err) || errors.Is(err, fs.ErrPermission) {
				return errors.New("O Consolidador CBA está aberto. Feche a janela dele e rode o comando de novo.")
			}
			return err
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
		alert("O Consolidador CBA foi instalado em " + dir + ", mas faltou:\n\n" + strings.Join(problems, "\n"))
	}
	run := exec.Command(target)
	run.Dir = dir
	return run.Start()
}

// copyFile writes next to the target and then swaps, so a failed copy never leaves half a program.
func copyFile(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	tmp := to + ".novo"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp, to); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
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

func uninstall() {
	if !ask("Desinstalar o Consolidador CBA?\n\nO programa, os atalhos e os ajustes salvos são apagados. As planilhas não são tocadas.") {
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
	// This program is inside the folder: a hidden cmd deletes it a moment after this process ends.
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine:       `cmd.exe /c ping -n 3 127.0.0.1 >nul & rmdir /s /q "` + installDir() + `"`,
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
	cmd.Start()
}

func ask(text string) bool {
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString("Consolidador CBA")
	r, _ := windows.MessageBox(0, t, c, windows.MB_YESNO|windows.MB_ICONQUESTION)
	return r == 6 // IDYES
}
