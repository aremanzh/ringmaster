package core

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DirEntry is one subfolder in a DirListing.
type DirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// DirListing is one step of the folder picker: where it is, where it can go.
type DirListing struct {
	// Path is the absolute, cleaned path of the listed folder.
	Path string `json:"path"`
	// Parent is the folder above, or "" at the top of a drive or of the
	// file system.
	Parent string `json:"parent"`
	// Roots are the places to start from: the drives on Windows, "/" elsewhere.
	Roots []string `json:"roots"`
	// Dirs are the subfolders, sorted by name with hidden ones last.
	Dirs []DirEntry `json:"dirs"`
	// Markers names the files found here that say what the folder is:
	// artisan, composer.json, package.json, or a PHP binary.
	Markers []string `json:"markers"`
	// Truncated is set when the folder holds more subfolders than are listed.
	Truncated bool `json:"truncated,omitempty"`
}

// maxDirEntries bounds one listing; a folder with more is cut off and says so.
const maxDirEntries = 2000

// dirMarkers are the file names reported in DirListing.Markers.
var dirMarkers = []string{"artisan", "composer.json", "package.json", "php.exe", "php"}

// ListDir lists the subfolders of path for the folder picker. An empty path
// means the user's home folder. It only reads folder names: anyone allowed to
// call it can already point a project at any folder and run commands there.
func ListDir(path string) (DirListing, error) {
	if strings.TrimSpace(path) == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			roots := fsRoots()
			if len(roots) == 0 {
				return DirListing{}, internal("no home folder and no drives found: %s", err)
			}
			home = roots[0]
		}
		path = home
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return DirListing{}, badRequest("%q is not a usable path", path)
	}
	fi, err := os.Stat(abs)
	if err != nil || !fi.IsDir() {
		return DirListing{}, badRequest("%q does not exist or is not a folder", abs)
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return DirListing{}, badRequest("cannot read %q: %s", abs, err)
	}

	out := DirListing{Path: abs, Roots: fsRoots(), Dirs: []DirEntry{}, Markers: []string{}}
	if parent := filepath.Dir(abs); parent != abs {
		out.Parent = parent
	}
	for _, e := range entries {
		full := filepath.Join(abs, e.Name())
		isDir := e.IsDir()
		if !isDir && e.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			// A link or a Windows junction: it is a folder if its target is.
			if target, err := os.Stat(full); err == nil {
				isDir = target.IsDir()
			}
		}
		if !isDir {
			continue
		}
		if len(out.Dirs) == maxDirEntries {
			out.Truncated = true
			break
		}
		out.Dirs = append(out.Dirs, DirEntry{Name: e.Name(), Path: full})
	}
	sort.SliceStable(out.Dirs, func(i, j int) bool {
		a, b := out.Dirs[i].Name, out.Dirs[j].Name
		if ha, hb := hiddenName(a), hiddenName(b); ha != hb {
			return hb
		}
		return strings.ToLower(a) < strings.ToLower(b)
	})
	for _, name := range dirMarkers {
		if fi, err := os.Stat(filepath.Join(abs, name)); err == nil && !fi.IsDir() {
			out.Markers = append(out.Markers, name)
		}
	}
	return out, nil
}

// hiddenName reports names that are hidden by convention: dot folders, and
// Windows system folders such as $Recycle.Bin.
func hiddenName(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "$")
}
