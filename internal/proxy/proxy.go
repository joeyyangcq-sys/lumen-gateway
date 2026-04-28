package proxy

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
)

type Proxy interface {
	ServeHTTP(ctx context.Context, c *app.RequestContext)
}
