//go:build windows

package main

// The Windows "choose folder" and "save as" windows, through IFileOpenDialog and IFileSaveDialog called
// directly. Every pointer handed to Windows is converted inside the syscall.SyscallN call itself, so the Go
// runtime keeps it alive and in place for the call, and every HRESULT is checked as the signed number it is.

import (
	"errors"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procCoCreateInstance            = windows.NewLazySystemDLL("ole32.dll").NewProc("CoCreateInstance")
	procSHCreateItemFromParsingName = windows.NewLazySystemDLL("shell32.dll").NewProc("SHCreateItemFromParsingName")

	clsidFileOpenDialog = mustGUID("{DC1C5A9C-E88A-4DDE-A5A1-60F82A20AEF7}")
	iidFileOpenDialog   = mustGUID("{D57C7288-D4AD-4768-BE02-9D969532D960}")
	clsidFileSaveDialog = mustGUID("{C0B4E2F3-BA21-4773-8DBA-335EC946EB8B}")
	iidFileSaveDialog   = mustGUID("{84BCCD23-5FDE-4CDB-AEA4-AF64B83D78AB}")
	iidShellItem        = mustGUID("{43826D1E-E718-42EE-BC55-A1E261C37BFE}")
)

// Method numbers in the COM tables (IUnknown, IModalWindow, IFileDialog, IShellItem), from ShObjIdl_core.h.
const (
	mRelease             = 2
	mShow                = 3
	mSetFileTypes        = 4
	mSetOptions          = 9
	mGetOptions          = 10
	mSetFolder           = 12
	mSetFileName         = 15
	mSetTitle            = 17
	mGetResult           = 20
	mSetDefaultExtension = 22
	mGetDisplayName      = 5 // IShellItem

	fosOverwritePrompt  = 0x2
	fosNoChangeDir      = 0x8
	fosPickFolders      = 0x20
	fosForceFileSystem  = 0x40
	fosPathMustExist    = 0x800
	sigdnFileSysPath    = 0x80058000
	clsctxInprocServer  = 0x1
	hrCancelled         = 0x800704C7 // HRESULT_FROM_WIN32(ERROR_CANCELLED)
	hrSFalse            = 1
	coinitApartmentThrd = 0x2
)

func mustGUID(s string) windows.GUID {
	g, err := windows.GUIDFromString(s)
	if err != nil {
		panic(err)
	}
	return g
}

// method returns the address of method i of the COM interface at obj.
func method(obj unsafe.Pointer, i int) uintptr {
	vtbl := *(*unsafe.Pointer)(obj)
	return *(*uintptr)(unsafe.Add(vtbl, uintptr(i)*unsafe.Sizeof(uintptr(0))))
}

func failed(hr uintptr) error {
	if int32(uint32(hr)) < 0 {
		return syscall.Errno(uint32(hr))
	}
	return nil
}

func release(obj unsafe.Pointer) {
	if obj != nil {
		syscall.SyscallN(method(obj, mRelease), uintptr(obj))
	}
}

// withCOM runs f with COM set up for a window on this thread (already done on the WebView2 thread).
func withCOM(f func() (string, error)) (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	err := windows.CoInitializeEx(0, coinitApartmentThrd)
	if err == nil || err == syscall.Errno(hrSFalse) {
		defer windows.CoUninitialize()
	}
	return f()
}

type fileDialog struct{ p unsafe.Pointer }

func newFileDialog(clsid, iid *windows.GUID) (fileDialog, error) {
	var p unsafe.Pointer
	hr, _, _ := syscall.SyscallN(procCoCreateInstance.Addr(),
		uintptr(unsafe.Pointer(clsid)), 0, clsctxInprocServer, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&p)))
	if err := failed(hr); err != nil {
		return fileDialog{}, err
	}
	if p == nil {
		return fileDialog{}, errors.New("o Windows não criou a janela")
	}
	return fileDialog{p}, nil
}

func (d fileDialog) addOptions(opts uint32) error { return d.changeOptions(opts, 0) }

// changeOptions turns options on and off over the window's own defaults (the save window starts with
// overwrite confirmation on).
func (d fileDialog) changeOptions(on, off uint32) error {
	var cur uint32
	hr, _, _ := syscall.SyscallN(method(d.p, mGetOptions), uintptr(d.p), uintptr(unsafe.Pointer(&cur)))
	if err := failed(hr); err != nil {
		return err
	}
	hr, _, _ = syscall.SyscallN(method(d.p, mSetOptions), uintptr(d.p), uintptr((cur|on)&^off))
	return failed(hr)
}

func (d fileDialog) setString(m int, s string) error {
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return err
	}
	hr, _, _ := syscall.SyscallN(method(d.p, m), uintptr(d.p), uintptr(unsafe.Pointer(p)))
	return failed(hr)
}

// setFolder opens the window in folder; a folder that does not exist (Y: disconnected) is just skipped.
func (d fileDialog) setFolder(folder string) {
	path, err := windows.UTF16PtrFromString(folder)
	if err != nil {
		return
	}
	var item unsafe.Pointer
	hr, _, _ := syscall.SyscallN(procSHCreateItemFromParsingName.Addr(),
		uintptr(unsafe.Pointer(path)), 0, uintptr(unsafe.Pointer(&iidShellItem)), uintptr(unsafe.Pointer(&item)))
	if failed(hr) != nil || item == nil {
		return
	}
	defer release(item)
	syscall.SyscallN(method(d.p, mSetFolder), uintptr(d.p), uintptr(item))
}

func (d fileDialog) setFilter(name, pattern string) error {
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	p, err := windows.UTF16PtrFromString(pattern)
	if err != nil {
		return err
	}
	spec := &[1]struct{ name, pattern *uint16 }{{n, p}} // COMDLG_FILTERSPEC
	hr, _, _ := syscall.SyscallN(method(d.p, mSetFileTypes), uintptr(d.p), 1, uintptr(unsafe.Pointer(spec)))
	runtime.KeepAlive(n)
	runtime.KeepAlive(p)
	return failed(hr)
}

// show opens the window, owned by owner, and returns the chosen path, or errCancelled.
func (d fileDialog) show(owner uintptr) (string, error) {
	hr, _, _ := syscall.SyscallN(method(d.p, mShow), uintptr(d.p), owner)
	if uint32(hr) == hrCancelled {
		return "", errCancelled
	}
	if err := failed(hr); err != nil {
		return "", err
	}
	var item unsafe.Pointer
	hr, _, _ = syscall.SyscallN(method(d.p, mGetResult), uintptr(d.p), uintptr(unsafe.Pointer(&item)))
	if err := failed(hr); err != nil {
		return "", err
	}
	if item == nil {
		return "", errCancelled
	}
	defer release(item)
	var name *uint16
	hr, _, _ = syscall.SyscallN(method(item, mGetDisplayName), uintptr(item), sigdnFileSysPath, uintptr(unsafe.Pointer(&name)))
	if err := failed(hr); err != nil {
		return "", err
	}
	if name == nil {
		return "", errors.New("o Windows não devolveu o caminho")
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(name))
	return windows.UTF16PtrToString(name), nil
}

func folderDialog(owner uintptr, title, start string) (string, error) {
	return withCOM(func() (string, error) {
		d, err := newFileDialog(&clsidFileOpenDialog, &iidFileOpenDialog)
		if err != nil {
			return "", err
		}
		defer release(d.p)
		if err := d.addOptions(fosPickFolders | fosForceFileSystem | fosPathMustExist | fosNoChangeDir); err != nil {
			return "", err
		}
		d.setString(mSetTitle, title)
		if start != "" {
			d.setFolder(start)
		}
		return d.show(owner)
	})
}

// saveDialog asks where to save; overwrite makes it confirm replacing a file that exists (off when picking
// the spreadsheet to update, which is meant to exist).
func saveDialog(owner uintptr, title, folder, name, filterName, pattern, ext string, overwrite bool) (string, error) {
	return withCOM(func() (string, error) {
		d, err := newFileDialog(&clsidFileSaveDialog, &iidFileSaveDialog)
		if err != nil {
			return "", err
		}
		defer release(d.p)
		var off uint32
		if !overwrite {
			off = fosOverwritePrompt
		}
		if err := d.changeOptions(fosForceFileSystem|fosPathMustExist|fosNoChangeDir, off); err != nil {
			return "", err
		}
		d.setString(mSetTitle, title)
		d.setFilter(filterName, pattern)
		d.setString(mSetDefaultExtension, ext)
		if folder != "" {
			d.setFolder(folder)
		}
		d.setString(mSetFileName, name)
		return d.show(owner)
	})
}
