package apiclient

import (
	"encoding/pem"
	"net/http/httptest"
	"os"
	"testing"
)

func writePEM(t *testing.T, path string, srv *httptest.Server) {
	t.Helper()
	b := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}
