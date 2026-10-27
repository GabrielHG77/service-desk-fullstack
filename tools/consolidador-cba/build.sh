#!/bin/sh
# Gera dist/ConsolidadorCBA.exe (Windows 10/11, 64 bits). Precisa de Go e do go-winres:
#   go install github.com/tc-hib/go-winres@v0.3.3
set -e
cd "$(dirname "$0")"
go-winres make --in winres/winres.json --arch amd64
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-H=windowsgui -s -w" -o dist/ConsolidadorCBA.exe .
