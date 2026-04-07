package lie

import (
	"context"
	"log/slog"
	"time"

	"github.com/hyposcaler/srl-rift/internal/transport"
)

// Interface manages the LIE FSM and transport for a single RIFT interface.
type Interface struct {
	name   string // SRL interface name
	fsm    *FSM
	recvCh <-chan transport.ReceivedPacket
	logger *slog.Logger
}

// NewInterface creates a new per-interface LIE handler.
func NewInterface(name string, fsm *FSM, recvCh <-chan transport.ReceivedPacket, logger *slog.Logger) *Interface {
	return &Interface{
		name:   name,
		fsm:    fsm,
		recvCh: recvCh,
		logger: logger.With("interface", name),
	}
}

// Run starts the per-interface LIE goroutine. It handles timer ticks,
// received packets, and context cancellation. Blocks until ctx is cancelled.
func (i *Interface) Run(ctx context.Context) error {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	i.logger.Info("LIE interface started")

	for {
		select {
		case <-ctx.Done():
			i.logger.Info("LIE interface stopped")
			return nil
		case <-ticker.C:
			i.fsm.Tick()
		case pkt := <-i.recvCh:
			i.fsm.HandlePacket(pkt.Packet, pkt.SrcAddr)
		}
	}
}

// FSM returns the underlying FSM for inspection.
func (i *Interface) FSM() *FSM {
	return i.fsm
}
