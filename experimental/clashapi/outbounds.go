package clashapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	F "github.com/sagernet/sing/common/format"
)

func outboundRouter(server *Server, router adapter.Router, logFactory log.Factory) http.Handler {
	r := chi.NewRouter()
	r.Post("/", updateOutboundHandler(server, router, logFactory))
	return r
}

func updateOutboundHandler(server *Server, router adapter.Router, logFactory log.Factory) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Tag    string `json:"tag"`
			Type   string `json:"type"`
			Config any    `json:"config"`
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if request.Tag == "" || request.Type == "" {
			http.Error(w, "Tag and type are required", http.StatusBadRequest)
			return
		}

		// Check if outbound exists
		if _, exists := server.outbound.Outbound(request.Tag); exists {
			// Remove existing outbound
			if err := server.outbound.Remove(request.Tag); err != nil {
				http.Error(w, "Failed to remove existing outbound: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}

		// Create new outbound - create a context logger from the existing logger
		if err := server.outbound.Create(
			r.Context(), router,
			logFactory.NewLogger(F.ToString("outbound/", request.Type, "[", request.Tag, "]")),
			request.Tag, request.Type, request.Config); err != nil {
			http.Error(w, "Failed to create new outbound: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Outbound updated successfully",
		})
	}
}
