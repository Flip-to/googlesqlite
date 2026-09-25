// Package zoneinfo makes IANA time zone files visible to the GoogleSQL
// wasm analyzer.
//
// The analyzer's absl/cctz reads zone files itself through WASI, from
// $TZDIR or, when TZDIR is unset, from /usr/share/zoneinfo. The WASI
// filesystem that go-googlesql installs is rooted at "/" and resolves a
// guest path P to the host path "/" + P. On Linux and macOS
// /usr/share/zoneinfo normally exists and nothing needs to be done. On
// Windows it does not, and a host path such as C:\zoneinfo is not a
// valid guest path either: the guest must see the drive-relative form
// (\zoneinfo), which the host resolves against the current directory's
// volume.
//
// Prepare, which must run before the wasm runtime is initialised (the
// WASI layer snapshots the environment at that point), checks whether
// the guest would find zone files and, if not, extracts the embedded
// zoneinfo.zip to a per-user cache directory and points TZDIR at it.
package zoneinfo

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// zipData is a copy of $GOROOT/lib/time/zoneinfo.zip. See README.md.
//
//go:embed zoneinfo.zip
var zipData []byte

// defaultGuestDir is where cctz looks when TZDIR is unset.
const defaultGuestDir = "/usr/share/zoneinfo"

// probeZone is a zone file that every complete tree contains.
const probeZone = "UTC"

// completeMarker is written last into an extracted tree.
const completeMarker = ".googlesqlite-complete"

var (
	once    sync.Once
	prepErr error
)

// Prepare runs the check (and extraction if needed) once per process.
// Setting disableEnv to "0", "false" or "off" in the environment skips
// it entirely. The returned error is informational: the caller should
// still initialise the runtime, and use Hint to explain a later
// failure.
func Prepare(disableEnv string) error {
	once.Do(func() {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(disableEnv))) {
		case "0", "false", "off", "no":
			return
		}
		prepErr = prepare()
	})
	return prepErr
}

// Hint returns advice to attach to an analyzer initialisation failure,
// or "" when zone preparation succeeded or was not attempted.
func Hint() string {
	if prepErr == nil {
		return ""
	}
	return prepErr.Error()
}

func prepare() error {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}
	env := osEnv{}
	if dir, ok := findUsable(env, runtime.GOOS, cwd); ok {
		if dir != "" {
			return os.Setenv("TZDIR", dir)
		}
		return nil
	}
	host, err := extract()
	if err != nil {
		return fmt.Errorf("time zone files not found for the GoogleSQL analyzer and extracting the embedded copy failed: %w; set TZDIR to a zoneinfo directory in the form %s", err, tzdirForm(runtime.GOOS))
	}
	guest, err := guestPath(runtime.GOOS, host, cwd)
	if err != nil {
		// Different volume: try the temp directory, which may live on
		// the current volume even when the user cache does not.
		if alt, aerr := extractTo(os.TempDir()); aerr == nil {
			if g, gerr := guestPath(runtime.GOOS, alt, cwd); gerr == nil {
				return os.Setenv("TZDIR", g)
			}
		}
		return fmt.Errorf("time zone files for the GoogleSQL analyzer were extracted to %s, but %v; set TZDIR to a zoneinfo directory in the form %s", host, err, tzdirForm(runtime.GOOS))
	}
	return os.Setenv("TZDIR", guest)
}

func tzdirForm(goos string) string {
	if goos == "windows" {
		return `"/path/to/zoneinfo" (drive-relative, no volume name, on the drive of the current directory)`
	}
	return `"/path/to/zoneinfo"`
}

// statEnv abstracts the host lookups findUsable needs, for tests.
type statEnv interface {
	Getenv(string) string
	IsFile(string) bool
}

type osEnv struct{}

func (osEnv) Getenv(k string) string { return os.Getenv(k) }
func (osEnv) IsFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// hostOfGuest mirrors the WASI osFS rooted at "/": guest path P is
// opened on the host as "/" + P (without its leading slash).
func hostOfGuest(guest string) string {
	return "/" + strings.TrimLeft(guest, `/`)
}

func guestHasZones(env statEnv, guest string) bool {
	if guest == "" {
		return false
	}
	return env.IsFile(hostOfGuest(path.Join(filepath.ToSlash(guest), probeZone)))
}

// findUsable reports whether the guest can already find zone files. The
// returned dir, when non-empty, is a TZDIR value that must be set (e.g.
// a Windows TZDIR given with a volume name, rewritten to the
// drive-relative form the guest resolves).
func findUsable(env statEnv, goos, cwd string) (string, bool) {
	tz := env.Getenv("TZDIR")
	if tz != "" {
		if guestHasZones(env, tz) {
			return "", true
		}
		if goos == "windows" {
			if g, err := guestPath(goos, tz, cwd); err == nil && g != tz && guestHasZones(env, g) {
				return g, true
			}
		}
		return "", false
	}
	return "", guestHasZones(env, defaultGuestDir)
}

// volumeName is filepath.VolumeName for Windows paths, usable on every
// GOOS so the path logic can be unit tested anywhere.
func volumeName(p string) string {
	p = strings.ReplaceAll(p, "/", `\`)
	if len(p) >= 2 && p[1] == ':' && ((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z')) {
		return p[:2]
	}
	if strings.HasPrefix(p, `\\`) {
		// \\server\share
		rest := p[2:]
		i := strings.IndexByte(rest, '\\')
		if i <= 0 {
			return ""
		}
		j := strings.IndexByte(rest[i+1:], '\\')
		if j < 0 {
			return p
		}
		return p[:2+i+1+j]
	}
	return ""
}

// guestPath converts an absolute host directory into the TZDIR value the
// WASI guest resolves back to it. On Windows the volume name is removed,
// which is only correct when the directory is on the same volume as the
// current directory.
func guestPath(goos, host, cwd string) (string, error) {
	if goos != "windows" {
		return host, nil
	}
	vol := volumeName(host)
	if vol == "" {
		return "", fmt.Errorf("%q is not an absolute path with a volume name", host)
	}
	if cv := volumeName(cwd); !strings.EqualFold(cv, vol) {
		return "", fmt.Errorf("it is on volume %s while the current directory is on volume %q (the analyzer resolves TZDIR against the current volume)", vol, cv)
	}
	rest := strings.ReplaceAll(host[len(vol):], `\`, "/")
	if !strings.HasPrefix(rest, "/") {
		return "", fmt.Errorf("%q is not an absolute path", host)
	}
	return rest, nil
}

func extract() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return extractTo(filepath.Join(base, "googlesqlite"))
}

// extractTo unpacks the embedded archive into parent/zoneinfo-<hash> and
// returns that directory. It extracts into a temporary sibling and
// renames it into place, so concurrent processes never observe a partial
// tree; a loser of the rename race uses the winner's copy.
func extractTo(parent string) (string, error) {
	sum := sha256.Sum256(zipData)
	final := filepath.Join(parent, "zoneinfo-"+hex.EncodeToString(sum[:8]))
	if isComplete(final) {
		return final, nil
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(parent, ".zoneinfo-tmp-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if err := unzip(tmp); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(tmp, completeMarker), nil, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, final); err != nil {
		if isComplete(final) {
			return final, nil
		}
		// A stale incomplete directory from a crashed run: replace it.
		if rerr := os.RemoveAll(final); rerr == nil {
			if err2 := os.Rename(tmp, final); err2 == nil || isComplete(final) {
				return final, nil
			}
		}
		return "", err
	}
	return final, nil
}

func isComplete(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, completeMarker))
	return err == nil
}

func unzip(dst string) error {
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		name := path.Clean(f.Name)
		if name == "." || strings.HasPrefix(name, "../") || name == ".." || path.IsAbs(name) {
			return errors.New("zoneinfo: unsafe entry " + f.Name)
		}
		target := filepath.Join(dst, filepath.FromSlash(name))
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := writeEntry(f, target); err != nil {
			return err
		}
	}
	return nil
}

func writeEntry(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
