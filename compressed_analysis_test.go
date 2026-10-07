package contracttrace

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCompressedAnalysisRetainsGraphAndStrictInputChecks(t *testing.T) {
	_, original, err := TraceWithAnalysis(context.Background(), Options{Root: "testdata/sample", Seeds: []string{"Validate"}, Depth: 3, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	compress := func(data []byte) []byte {
		var output bytes.Buffer
		writer := gzip.NewWriter(&output)
		if _, err := writer.Write(data); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		return output.Bytes()
	}
	compressed := compress(plain)
	loaded, err := ReadAnalysis(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, loaded) {
		t.Fatal("compressed graph changed content")
	}
	before, err := Explore(context.Background(), original, ExploreOptions{Seeds: []string{"Validate"}, Depth: 3, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	after, err := Explore(context.Background(), loaded, ExploreOptions{Seeds: []string{"Validate"}, Depth: 3, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("compressed saved exploration changed scope")
	}
	for _, gzipCompressed := range []bool{false, true} {
		var output bytes.Buffer
		if err := WriteAnalysis(&output, original, gzipCompressed); err != nil {
			t.Fatal(err)
		}
		written, err := ReadAnalysis(bytes.NewReader(output.Bytes()))
		if err != nil || !reflect.DeepEqual(original, written) {
			t.Fatalf("writer round trip gzip=%v: %v", gzipCompressed, err)
		}
		if err := WriteAnalysis(analysisFailWriter{}, original, gzipCompressed); err == nil {
			t.Fatalf("writer failure lost gzip=%v", gzipCompressed)
		}
	}
	corrupt := append([]byte(nil), compressed...)
	corrupt[len(corrupt)-1] ^= 1
	unknown := append([]byte(`{"unexpected":true,`), plain[1:]...)
	for name, data := range map[string][]byte{"checksum": corrupt, "truncated": compressed[:len(compressed)-5], "second JSON": compress(append(append([]byte(nil), plain...), []byte(" {}")...)), "unknown field": compress(unknown), "trailing bytes": append(append([]byte(nil), compressed...), []byte("garbage")...)} {
		t.Run(name, func(t *testing.T) {
			if _, err := ReadAnalysis(bytes.NewReader(data)); err == nil {
				t.Fatal("invalid compressed input accepted")
			}
		})
	}
}

type analysisFailWriter struct{}

func (analysisFailWriter) Write([]byte) (int, error) { return 0, errors.New("write rejected") }

func TestCompressedAnalysisStillRejectsChangedSource(t *testing.T) {
	root := t.TempDir()
	for name, contents := range map[string]string{"go.mod": "module example.com/compressed\n\ngo 1.27.0\n", "source.go": "package compressed\nfunc Validate(n int) bool {return n>0}\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	_, original, err := TraceWithAnalysis(context.Background(), Options{Root: root, Seeds: []string{"Validate"}, Depth: 3, MaxNodes: 100})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := WriteAnalysis(&output, original, true); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadAnalysis(bytes.NewReader(output.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source.go"), []byte("package compressed\nfunc Validate(n int) bool {return n>1}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Explore(context.Background(), loaded, ExploreOptions{Seeds: []string{"Validate"}, Depth: 3, MaxNodes: 100}); err == nil {
		t.Fatal("compression bypassed source identity check")
	}
}
