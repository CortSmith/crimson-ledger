package apiserver

import (
	"encoding/json"
	"net/http"
)

// health answers liveness only. It never touches the store, so it reveals nothing and costs nothing.
func (s *server) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", jsonContentType)
	_ = json.NewEncoder(w).Encode(struct {
		OK      bool   `json:"ok"`
		Version string `json:"version"`
	}{true, s.opts.Version})
}
