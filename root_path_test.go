package contracttrace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTraceKeepsSymlinkParentRootSemantics(t *testing.T) {
	base := t.TempDir()
	app := filepath.Join(base, "app")
	other := filepath.Join(base, "other")
	if err := os.MkdirAll(app, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(other, "nested"), 0755); err != nil {
		t.Fatal(err)
	}
	writeRootFixture(t, app, "example.com/app", "package app\nfunc Validate() bool { return false }\n")
	writeRootFixture(t, other, "example.com/other", "package other\nfunc Validate() bool { return true }\n")
	if err := os.Symlink(filepath.Join(other, "nested"), filepath.Join(app, "alias")); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	rawRoot := app + string(os.PathSeparator) + "alias" + string(os.PathSeparator) + ".."
	rawInfo, err := os.Stat(rawRoot)
	if err != nil {
		t.Skipf("platform cannot resolve the symlink/.. root path: %v", err)
	}
	otherInfo, err := os.Stat(other)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(rawInfo, otherInfo) {
		t.Skip("platform resolves symlink/.. differently from the POSIX root alias under test")
	}

	wantRoot, err := filepath.Abs(other)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Trace(context.Background(), Options{Root: rawRoot, Seeds: []string{"Validate"}, Depth: 2, MaxNodes: 50})
	if err != nil {
		t.Fatal(err)
	}
	if report.Root != wantRoot {
		t.Fatalf("Trace selected lexical root %q; OS-resolved root is %q", report.Root, wantRoot)
	}
	otherFound, appFound := false, false
	for _, node := range report.Nodes {
		otherFound = otherFound || node.ID == "example.com/other::Validate"
		appFound = appFound || node.ID == "example.com/app::Validate"
	}
	if !otherFound || appFound {
		t.Fatalf("Trace loaded the wrong module for the raw root path: nodes=%+v", report.Nodes)
	}
}

func TestCanonicalRootUsesPhysicalBaseForRelativeParent(t *testing.T) {
	base := t.TempDir()
	actualCWD := filepath.Join(base, "other", "nested", "child")
	logicalAlias := filepath.Join(base, "app", "alias")
	if err := os.MkdirAll(actualCWD, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(logicalAlias), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "other", "nested"), logicalAlias); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	logicalCWD := filepath.Join(logicalAlias, "child")
	originalCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	originalPWD, hadPWD := os.LookupEnv("PWD")
	t.Cleanup(func() {
		if err := os.Chdir(originalCWD); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
		if hadPWD {
			_ = os.Setenv("PWD", originalPWD)
		} else {
			_ = os.Unsetenv("PWD")
		}
	})
	if err := os.Chdir(logicalCWD); err != nil {
		t.Skipf("cannot enter symlinked working directory: %v", err)
	}
	if err := os.Setenv("PWD", logicalCWD); err != nil {
		t.Fatal(err)
	}
	reportedCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(reportedCWD) != filepath.Clean(logicalCWD) {
		t.Skipf("runtime does not preserve a logical PWD path: got %q, want %q", reportedCWD, logicalCWD)
	}

	want, err := filepath.EvalSymlinks(filepath.Join(actualCWD, "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	got, err := canonicalRoot("../..")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("relative root resolved against logical PWD: got %q, want %q", got, want)
	}
}

func TestCanonicalRootAcceptsWindowsPathForms(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path forms")
	}
	root := t.TempDir()
	t.Chdir(root)
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	volume := filepath.VolumeName(root)
	relativeToVolume := strings.TrimPrefix(root, volume)
	for _, input := range []string{".", root, volume + ".", string(os.PathSeparator) + strings.TrimLeft(relativeToVolume, string(os.PathSeparator))} {
		got, err := canonicalRoot(input)
		if err != nil {
			t.Errorf("canonicalRoot(%q): %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("canonicalRoot(%q)=%q, want %q", input, got, want)
		}
	}
}

func TestTraceUsesWindowsJunctionParentResolution(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows junction path behavior")
	}
	base := t.TempDir()
	app := filepath.Join(base, "app")
	other := filepath.Join(base, "other")
	nested := filepath.Join(other, "nested")
	if err := os.MkdirAll(app, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	writeRootFixture(t, app, "example.com/app", "package app\nfunc Validate() bool { return false }\n")
	writeRootFixture(t, other, "example.com/other", "package other\nfunc Validate() bool { return true }\n")
	junction := filepath.Join(app, "alias")
	output, err := exec.Command("cmd.exe", "/c", "mklink", "/J", junction, nested).CombinedOutput()
	if err != nil {
		t.Skipf("directory junction unavailable: %v: %s", err, output)
	}
	rawRoot := app + string(os.PathSeparator) + "alias" + string(os.PathSeparator) + ".."
	rawInfo, err := os.Stat(rawRoot)
	if err != nil {
		t.Skipf("OS cannot resolve the junction/.. root path: %v", err)
	}
	appInfo, err := os.Stat(app)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(rawInfo, appInfo) {
		t.Skip("this Windows version resolves junction/.. differently from the tested parent-directory behavior")
	}
	wantRoot, err := filepath.Abs(app)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Trace(context.Background(), Options{Root: rawRoot, Seeds: []string{"Validate"}, Depth: 2, MaxNodes: 50})
	if err != nil {
		t.Fatal(err)
	}
	if report.Root != wantRoot {
		t.Fatalf("Trace selected root %q; OS-resolved module root is %q", report.Root, wantRoot)
	}
	appFound, otherFound := false, false
	for _, node := range report.Nodes {
		appFound = appFound || node.ID == "example.com/app::Validate"
		otherFound = otherFound || node.ID == "example.com/other::Validate"
	}
	if !appFound || otherFound {
		t.Fatalf("Trace did not analyze the OS-selected app module: nodes=%+v", report.Nodes)
	}
}

func writeRootFixture(t *testing.T, root, module, source string) {
	t.Helper()
	for name, contents := range map[string]string{
		"go.mod":    "module " + module + "\n\ngo 1.27.0\n",
		"source.go": source,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
}
