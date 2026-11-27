// Package hmacvectors loads proto/testdata/hmac_vectors.txt, the golden
// vectors for the HMAC projections the server and the firmware must build
// byte for byte (CLAUDE.md hazard #5). Used only by tests.
package hmacvectors

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Vector is one line of the file: its kind, the inputs, the projection they
// produce, and the projection's hex HMAC-SHA256 under the file's key.
type Vector struct {
	Kind       string
	Fields     []string // inputs; "" where the file has "-"
	Projection string
	HMAC       string
}

// Load reads the shared vector file and returns its key and vectors.
func Load() (key string, vectors []Vector, err error) {
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		return "", nil, fmt.Errorf("hmacvectors: cannot locate source file")
	}
	path := filepath.Join(filepath.Dir(self), "..", "..", "..", "proto", "testdata", "hmac_vectors.txt")
	f, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if parts[0] == "key" && len(parts) == 2 {
			key = parts[1]
			continue
		}
		if len(parts) < 3 {
			return "", nil, fmt.Errorf("hmacvectors: short line %q", line)
		}
		fields := parts[1 : len(parts)-2]
		for i, v := range fields {
			if v == "-" {
				fields[i] = ""
			}
		}
		vectors = append(vectors, Vector{
			Kind:       parts[0],
			Fields:     fields,
			Projection: parts[len(parts)-2],
			HMAC:       parts[len(parts)-1],
		})
	}
	if err := sc.Err(); err != nil {
		return "", nil, err
	}
	if key == "" || len(vectors) == 0 {
		return "", nil, fmt.Errorf("hmacvectors: no key or no vectors in %s", path)
	}
	return key, vectors, nil
}
