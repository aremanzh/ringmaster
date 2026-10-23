//go:build !gui

// This stub keeps `go build ./...` green without pulling in the Wails
// toolchain. The real GUI lives behind the `gui` build tag; build it with:
//
//	go build -tags gui,desktop,production -ldflags "-H windowsgui" -o ringmaster-gui.exe ./cmd/ringmaster-gui
package main

import "fmt"

func main() {
	fmt.Println("ringmaster-gui was built without the 'gui' tag.")
	fmt.Println(`Rebuild with: go build -tags gui,desktop,production -ldflags "-H windowsgui" -o ringmaster-gui.exe ./cmd/ringmaster-gui`)
}
