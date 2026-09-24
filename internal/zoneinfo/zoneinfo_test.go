package zoneinfo

import (
	"os"
	"path/filepath"
	"testing"
)

type fakeEnv struct {
	vars  map[string]string
	files map[string]bool
}

func (f fakeEnv) Getenv(k string) string { return f.vars[k] }
func (f fakeEnv) IsFile(p string) bool   { return f.files[p] }

func TestVolumeName(t *testing.T) {
	for in, want := range map[string]string{
		`C:\Users\a`:      "C:",
		`c:/x`:            "c:",
		`\\srv\share\x\y`: `\\srv\share`,
		`/usr/share`:      "",
		`\tmp\zoneinfo`:   "",
		`relative\path`:   "",
	} {
		if got := volumeName(in); got != want {
			t.Errorf("volumeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGuestPath(t *testing.T) {
	tests := []struct {
		goos, host, cwd, want string
		wantErr               bool
	}{
		{"windows", `C:\Users\a\AppData\Local\googlesqlite\zoneinfo-1`, `C:\dev`, "/Users/a/AppData/Local/googlesqlite/zoneinfo-1", false},
		{"windows", `C:/tmp/zoneinfo`, `c:\work`, "/tmp/zoneinfo", false},
		{"windows", `C:\tmp\zoneinfo`, `D:\work`, "", true},
		{"windows", `\\srv\share\zi`, `\\srv\share\w`, "/zi", false},
		{"windows", `\tmp\zoneinfo`, `C:\`, "", true},
		{"linux", "/home/a/.cache/googlesqlite/zoneinfo-1", "/w", "/home/a/.cache/googlesqlite/zoneinfo-1", false},
	}
	for _, tt := range tests {
		got, err := guestPath(tt.goos, tt.host, tt.cwd)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("guestPath(%s, %q, %q) = %q, %v; want %q, err=%v", tt.goos, tt.host, tt.cwd, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestFindUsable(t *testing.T) {
	tests := []struct {
		name    string
		goos    string
		tzdir   string
		files   []string
		wantDir string
		wantOK  bool
	}{
		{"system zoneinfo", "linux", "", []string{"/usr/share/zoneinfo/UTC"}, "", true},
		{"nothing", "windows", "", nil, "", false},
		{"working TZDIR respected", "windows", "/tmp/zoneinfo", []string{"/tmp/zoneinfo/UTC"}, "", true},
		{"TZDIR with volume rewritten", "windows", `C:\tmp\zoneinfo`, []string{"/tmp/zoneinfo/UTC"}, "/tmp/zoneinfo", true},
		{"TZDIR other volume", "windows", `D:\tmp\zoneinfo`, []string{"/tmp/zoneinfo/UTC"}, "", false},
		{"broken TZDIR", "linux", "/nope", []string{"/usr/share/zoneinfo/UTC"}, "", false},
	}
	for _, tt := range tests {
		env := fakeEnv{vars: map[string]string{"TZDIR": tt.tzdir}, files: map[string]bool{}}
		for _, f := range tt.files {
			env.files[f] = true
		}
		dir, ok := findUsable(env, tt.goos, `C:\work`)
		if dir != tt.wantDir || ok != tt.wantOK {
			t.Errorf("%s: findUsable = %q, %v; want %q, %v", tt.name, dir, ok, tt.wantDir, tt.wantOK)
		}
	}
}

func TestExtractTo(t *testing.T) {
	parent := t.TempDir()
	dir, err := extractTo(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"UTC", filepath.Join("America", "New_York"), completeMarker} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	again, err := extractTo(parent)
	if err != nil || again != dir {
		t.Fatalf("second extract = %q, %v; want %q", again, err, dir)
	}
}
