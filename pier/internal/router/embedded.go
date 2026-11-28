package router

import (
	"github.com/caddyserver/caddy/v2"

	// Register the modules used by Pier's generated JSON.
	_ "github.com/caddyserver/caddy/v2/modules/caddyevents"
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp/fileserver"
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp/headers"
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp/reverseproxy"
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp/rewrite"
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp/tracing"
	_ "github.com/caddyserver/caddy/v2/modules/caddytls/standardstek"
	_ "github.com/caddyserver/caddy/v2/modules/filestorage"
	_ "github.com/caddyserver/caddy/v2/modules/logging"
)

func loadEmbedded(config []byte) error { return caddy.Load(config, false) }

// Stop releases the embedded listeners. An external Caddy owns its own lifetime.
func (c Caddy) Stop() error {
	if c.Admin != "" {
		return nil
	}
	return caddy.Stop()
}
