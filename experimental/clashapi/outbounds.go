package clashapi

import (
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	F "github.com/sagernet/sing/common/format"
	"github.com/sagernet/sing/common/json"
)

func outboundRouter(server *Server, router adapter.Router, logFactory log.Factory) http.Handler {
	r := chi.NewRouter()
	r.Post("/", updateOutboundHandler(server, router, logFactory))
	return r
}

func updateOutboundHandler(server *Server, router adapter.Router, logFactory log.Factory) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		content, err := io.ReadAll(r.Body)
		if err != nil {
			render.Status(r, http.StatusBadRequest)
			render.JSON(w, r, newError("Invalid request body(read): "+err.Error()))
			return
		}
		outboundConfig, err := json.UnmarshalExtendedContext[option.Outbound](server.ctx, content)
		if err != nil {
			render.Status(r, http.StatusBadRequest)
			render.JSON(w, r, newError("Invalid request body(unmarshal): "+err.Error()))
			return
		}
		// check: tag, type are required
		if outboundConfig.Tag == "" || outboundConfig.Type == "" {
			render.Status(r, http.StatusBadRequest)
			render.JSON(w, r, newError("Tag and type are required"))
			return
		}
		var action string
		if _, exists := server.outbound.Outbound(outboundConfig.Tag); exists {
			if err := server.outbound.Remove(outboundConfig.Tag); err != nil {
				render.Status(r, http.StatusInternalServerError)
				render.JSON(w, r, newError("Failed to remove existing outbound: "+err.Error()))
				return
			}
			action = "REPLACE"
		} else {
			action = "CREATED"
		}
		logger := logFactory.NewLogger(F.ToString("outbound/", outboundConfig.Type, "[", outboundConfig.Tag, "]"))
		// Create new outbound
		if err := server.outbound.Create(
			server.ctx, router,
			logger,
			outboundConfig.Tag, outboundConfig.Type, outboundConfig.Options); err != nil {
			// 如果Create失败，返回错误信息给接口
			render.Status(r, http.StatusInternalServerError)
			render.JSON(w, r, newError("Failed to create new outbound: "+err.Error()))
			return
		}
		logger.Info("outbound update, action: ", action)
		// Return success response
		render.Status(r, http.StatusOK)
		render.JSON(w, r, map[string]any{
			"action": action,
			"outbound": map[string]any{
				"tag":  outboundConfig.Tag,
				"type": outboundConfig.Type,
			},
		})
	}
}
