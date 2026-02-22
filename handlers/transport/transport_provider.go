package transport

import (
	"context"
)

type ITransportProvider interface {
	Start() error
	Stop() error
	RegisterJobHandler(
		func(svc ITransportService, ctx context.Context) error,
	) error
}
