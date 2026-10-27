//go:build !windows

package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
)

// On other systems the program serves the same screen for development and tests:
// the folder and the place where Export saves come from flags instead of the Windows dialogs.
func main() {
	addr := flag.String("addr", "127.0.0.1:8765", "address to serve on")
	folder := flag.String("folder", "", "folder returned by Escolher pasta")
	out := flag.String("out", os.TempDir(), "folder where Exportar saves")
	cfg := flag.String("config", defaultConfigPath(), "settings file")
	flag.Parse()
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	a := newApp(ln.Addr().String(), *cfg, devDesktop{folder: *folder, out: *out})
	fmt.Println("http://" + a.host + "/")
	log.Fatal(http.Serve(ln, a.handler()))
}

type devDesktop struct{ folder, out string }

func (d devDesktop) pickFolder(start string) (string, error) {
	if d.folder == "" {
		return "", errCancelled
	}
	return d.folder, nil
}

func (d devDesktop) saveFile(name, dir, kind string) (string, error) {
	return filepath.Join(d.out, name), nil
}

func (d devDesktop) open(path string) error   { log.Println("abrir", path); return nil }
func (d devDesktop) reveal(path string) error { log.Println("mostrar na pasta", path); return nil }
