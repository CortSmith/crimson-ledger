// Command poc proves the T01 risky calls: atomic write + perms, unknown-field rejection, redact regex.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type cfg struct {
	Version int `json:"version"`
}

func main() {
	dir, _ := os.MkdirTemp("", "poc-t01")
	defer os.RemoveAll(dir)

	// 1. atomic write + perms
	p := filepath.Join(dir, "a.json")
	tmp, _ := os.CreateTemp(dir, ".tmp-*")
	_ = tmp.Chmod(0o600)
	_, _ = tmp.Write([]byte(`{"version":1}`))
	_ = tmp.Sync()
	_ = tmp.Close()
	_ = os.Rename(tmp.Name(), p)
	st, _ := os.Stat(p)
	if st.Mode().Perm()&0o077 != 0 {
		fmt.Println("FAIL atomic")
		os.Exit(1)
	}
	fmt.Println("PASS atomic")
	bad := filepath.Join(dir, "b.json")
	_ = os.WriteFile(bad, []byte("{}"), 0o644)
	_ = os.Chmod(bad, 0o644)
	st, _ = os.Lstat(bad)
	if st.Mode().Perm()&0o077 == 0 {
		fmt.Println("FAIL perm-detect")
		os.Exit(1)
	}
	fmt.Println("PASS perm-detect")

	// 2. unknown field
	dec := json.NewDecoder(bytes.NewReader([]byte(`{"version":1,"typo":2}`)))
	dec.DisallowUnknownFields()
	var c cfg
	if err := dec.Decode(&c); err == nil || !strings.Contains(err.Error(), "typo") {
		fmt.Println("FAIL unknown-field", err)
		os.Exit(1)
	}
	fmt.Println("PASS unknown-field")

	// 4. redact
	re := regexp.MustCompile(`clk_[iv]_([a-z0-9]{8})_[A-Za-z0-9_-]{20,}`)
	secret := strings.Repeat("Ab3_-", 8) + "xyz"
	out := re.ReplaceAllString("auth failed for clk_i_k3f9a2x1_"+secret, "clk_x_${1}_[redacted]")
	if strings.Contains(out, secret) || !strings.Contains(out, "k3f9a2x1") {
		fmt.Println("FAIL redact", out)
		os.Exit(1)
	}
	fmt.Println("PASS redact:", out)
}
