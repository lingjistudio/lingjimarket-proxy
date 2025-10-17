package clashapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
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
			Tag    string      `json:"tag"`
			Type   string      `json:"type"`
			Config interface{} `json:"config"`
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			render.Status(r, http.StatusBadRequest)
			render.JSON(w, r, newError("Invalid request body"))
			return
		}

		if request.Tag == "" || request.Type == "" {
			render.Status(r, http.StatusBadRequest)
			render.JSON(w, r, newError("Tag and type are required"))
			return
		}

		// Create new outbound - ensure to return error message if creation fails
		if err := server.outbound.Create(
			r.Context(), router,
			logFactory.NewLogger(F.ToString("outbound/", request.Type, "[", request.Tag, "]")),
			request.Tag, request.Type, request.Config); err != nil {
			// 如果Create失败，返回错误信息给接口
			render.Status(r, http.StatusInternalServerError)
			render.JSON(w, r, newError("Failed to create new outbound: "+err.Error()))
			return
		}

		// Return success response
		render.JSON(w, r, map[string]interface{}{
			"success": true,
			"message": "Outbound updated successfully",
		})
	}
}
