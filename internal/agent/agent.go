package agent

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/hyposcaler/srl-rift/internal/config"
	"github.com/hyposcaler/srl-rift/internal/lie"
	"github.com/hyposcaler/srl-rift/internal/ndk"
	"github.com/hyposcaler/srl-rift/internal/transport"
)

// Agent is the top-level RIFT NDK agent.
type Agent struct {
	logger       *slog.Logger
	ndk          *ndk.Client
	notifHandler *ndk.NotificationHandler
	cfg          *config.Config

	// Pending config being accumulated before commit.
	pendingCfg *config.Config

	// Per-interface state, keyed by SRL interface name.
	interfaces map[string]*interfaceState
	adjEventCh chan lie.AdjacencyEvent

	mu sync.Mutex
}

type interfaceState struct {
	cancel    context.CancelFunc
	transport *transport.InterfaceTransport
	lieIface  *lie.Interface
}

// New creates and registers a new RIFT agent with the NDK.
func New(ctx context.Context, logger *slog.Logger) (*Agent, error) {
	ndkClient, err := ndk.New(ctx, logger)
	if err != nil {
		return nil, fmt.Errorf("ndk client: %w", err)
	}

	// Subscribe to interface and config notifications.
	if err := ndkClient.SubscribeInterfaces(ctx); err != nil {
		ndkClient.Close()
		return nil, fmt.Errorf("subscribe interfaces: %w", err)
	}
	if err := ndkClient.SubscribeConfig(ctx); err != nil {
		ndkClient.Close()
		return nil, fmt.Errorf("subscribe config: %w", err)
	}

	return &Agent{
		logger:       logger,
		ndk:          ndkClient,
		notifHandler: ndk.NewNotificationHandler(logger),
		interfaces:   make(map[string]*interfaceState),
		adjEventCh:   make(chan lie.AdjacencyEvent, 64),
	}, nil
}

// Run starts the agent's main loop. Blocks until ctx is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	// Start notification stream.
	stream, err := a.ndk.NotificationStream(ctx)
	if err != nil {
		return fmt.Errorf("notification stream: %w", err)
	}

	// Run notification handler in background.
	go func() {
		if err := a.notifHandler.Run(ctx, stream); err != nil {
			a.logger.Error("notification handler stopped", "error", err)
		}
	}()

	// Start keepalive.
	go a.keepAlive(ctx)

	a.logger.Info("waiting for RIFT configuration")

	// Wait for initial config.
	if err := a.waitForConfig(ctx); err != nil {
		return err
	}

	a.logger.Info("configuration received, starting LIE engine",
		"system_id", a.cfg.SystemID,
		"level", a.cfg.Level,
		"interfaces", len(a.cfg.Interfaces),
	)

	// Start interfaces that are configured.
	a.startConfiguredInterfaces(ctx)

	// Main event loop.
	return a.eventLoop(ctx)
}

// Close cleans up agent resources.
func (a *Agent) Close() {
	a.mu.Lock()
	for name := range a.interfaces {
		a.stopInterfaceLocked(name)
	}
	a.mu.Unlock()
	if a.ndk != nil {
		a.ndk.Close()
	}
}

// waitForConfig blocks until a valid RIFT config is received.
func (a *Agent) waitForConfig(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev := <-a.notifHandler.ConfigCh:
			a.processConfigEvent(ev)
			if a.cfg != nil && a.cfg.Valid() {
				return nil
			}
		case ev := <-a.notifHandler.InterfaceCh:
			a.logger.Debug("interface event before config",
				"name", ev.Name,
				"oper_up", ev.OperUp,
			)
		}
	}
}

// processConfigEvent handles a single config notification. NDK delivers
// config as multiple notifications:
//   - ".rift" with JSON for scalar fields
//   - ".rift.interface" per interface, name in PathWithKeys
//   - ".commit.end" signals end of config batch
func (a *Agent) processConfigEvent(ev ndk.ConfigEvent) {
	switch ev.JSPath {
	case ".rift":
		cfg, err := config.ParseRiftData(ev.Data)
		if err != nil {
			a.logger.Warn("config parse error", "error", err)
			return
		}
		// Start a new pending config.
		a.pendingCfg = cfg

	case ".rift.interface":
		ifName, ok := config.ExtractInterfaceName(ev.PathWithKeys)
		if !ok && len(ev.Keys) > 0 {
			ifName = ev.Keys[0]
			ok = true
		}
		if ok && a.pendingCfg != nil {
			a.pendingCfg.Interfaces[ifName] = struct{}{}
		} else if ok && a.cfg != nil {
			// Interface added to existing config.
			a.cfg.Interfaces[ifName] = struct{}{}
		}

	case ".commit.end":
		if a.pendingCfg != nil {
			a.cfg = a.pendingCfg
			a.pendingCfg = nil
			a.logger.Info("config committed",
				"system_id", a.cfg.SystemID,
				"level", a.cfg.Level,
				"interfaces", len(a.cfg.Interfaces),
			)
		}
	}
}

// startConfiguredInterfaces starts LIE on all configured interfaces.
func (a *Agent) startConfiguredInterfaces(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()

	for ifName := range a.cfg.Interfaces {
		if err := a.startInterfaceLocked(ctx, ifName); err != nil {
			a.logger.Error("start interface failed",
				"interface", ifName,
				"error", err,
			)
		}
	}
}

// startInterfaceLocked creates transport and FSM for one interface.
// Caller must hold a.mu.
func (a *Agent) startInterfaceLocked(ctx context.Context, srlName string) error {
	if _, exists := a.interfaces[srlName]; exists {
		return nil // already running
	}

	// Discover IPv4 address of subinterface.
	_, sub := transport.LinuxInterfaceNames(srlName)
	localAddr, err := transport.DiscoverInterfaceAddr(sub)
	if err != nil {
		return fmt.Errorf("discover address %s: %w", sub, err)
	}

	// Create transport.
	ifCtx, cancel := context.WithCancel(ctx)
	tr, err := transport.New(ifCtx, srlName, localAddr, a.logger)
	if err != nil {
		cancel()
		return fmt.Errorf("transport %s: %w", srlName, err)
	}

	// Create FSM.
	fsm := lie.NewFSM(
		a.cfg.SystemID,
		a.cfg.Level,
		fmt.Sprintf("rift-%d", a.cfg.SystemID),
		srlName,
		tr,
		a.adjEventCh,
		a.logger,
	)

	// Create recv channel and interface handler.
	recvCh := make(chan transport.ReceivedPacket, 64)
	lieIface := lie.NewInterface(srlName, fsm, recvCh, a.logger)

	// Start recv loop goroutine.
	go func() {
		if err := tr.RecvLoop(ifCtx, recvCh); err != nil && ifCtx.Err() == nil {
			a.logger.Error("recv loop error", "interface", srlName, "error", err)
		}
	}()

	// Start LIE interface goroutine.
	go func() {
		if err := lieIface.Run(ifCtx); err != nil && ifCtx.Err() == nil {
			a.logger.Error("LIE interface error", "interface", srlName, "error", err)
		}
	}()

	a.interfaces[srlName] = &interfaceState{
		cancel:    cancel,
		transport: tr,
		lieIface:  lieIface,
	}

	a.logger.Info("interface started", "interface", srlName, "addr", localAddr)
	return nil
}

// stopInterfaceLocked tears down one interface. Caller must hold a.mu.
func (a *Agent) stopInterfaceLocked(name string) {
	iface, ok := a.interfaces[name]
	if !ok {
		return
	}
	iface.cancel()
	iface.transport.Close()
	delete(a.interfaces, name)
	a.logger.Info("interface stopped", "interface", name)
}

// eventLoop processes interface, config, and adjacency events.
func (a *Agent) eventLoop(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil

		case ev := <-a.notifHandler.InterfaceCh:
			a.handleInterfaceEvent(ctx, ev)

		case ev := <-a.notifHandler.ConfigCh:
			a.handleConfigChange(ctx, ev)

		case ev := <-a.adjEventCh:
			a.handleAdjacencyEvent(ctx, ev)
		}
	}
}

// handleInterfaceEvent processes NDK interface notifications.
func (a *Agent) handleInterfaceEvent(ctx context.Context, ev ndk.InterfaceEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Only care about interfaces in our config.
	if a.cfg == nil || !a.cfg.HasInterface(ev.Name) {
		return
	}

	_, running := a.interfaces[ev.Name]

	// Ignore initial notifications with zeroed-out data (admin_up=false,
	// oper_up=false, mtu=0). These arrive during agent startup before
	// the real state is known.
	if !ev.AdminUp && !ev.OperUp && ev.MTU == 0 {
		return
	}

	if ev.OperUp && !running {
		if err := a.startInterfaceLocked(ctx, ev.Name); err != nil {
			a.logger.Error("start interface on oper-up failed",
				"interface", ev.Name,
				"error", err,
			)
		}
	} else if !ev.OperUp && running {
		a.stopInterfaceLocked(ev.Name)
	}
}

// handleConfigChange processes config notifications after initial config.
func (a *Agent) handleConfigChange(ctx context.Context, ev ndk.ConfigEvent) {
	oldCfg := a.cfg
	a.processConfigEvent(ev)

	// Only act on commit.end when config actually changed.
	if ev.JSPath != ".commit.end" || a.cfg == oldCfg {
		return
	}
	if a.cfg == nil || !a.cfg.Valid() {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// Stop interfaces that are no longer configured.
	for name := range a.interfaces {
		if !a.cfg.HasInterface(name) {
			a.stopInterfaceLocked(name)
		}
	}

	// Start newly configured interfaces.
	for ifName := range a.cfg.Interfaces {
		if _, running := a.interfaces[ifName]; !running {
			if err := a.startInterfaceLocked(ctx, ifName); err != nil {
				a.logger.Error("start new interface failed",
					"interface", ifName,
					"error", err,
				)
			}
		}
	}
}

// handleAdjacencyEvent processes FSM state changes and updates telemetry.
func (a *Agent) handleAdjacencyEvent(ctx context.Context, ev lie.AdjacencyEvent) {
	a.logger.Info("adjacency state change",
		"interface", ev.InterfaceName,
		"state", ev.State.String(),
	)

	a.updateAdjacencyTelemetry(ctx, ev)
}

// interfaceTelemetry is the JSON structure for the interface list entry.
type interfaceTelemetry struct {
	Adjacency *adjacencyTelemetry `json:"adjacency,omitempty"`
}

// adjacencyTelemetry is the JSON structure for adjacency state telemetry.
type adjacencyTelemetry struct {
	State            string `json:"state"`
	NeighborSystemID *int64 `json:"neighbor_system_id,omitempty"`
	NeighborLevel    *int8  `json:"neighbor_level,omitempty"`
	NeighborAddress  string `json:"neighbor_address,omitempty"`
}

// updateAdjacencyTelemetry pushes adjacency state to NDK telemetry.
func (a *Agent) updateAdjacencyTelemetry(ctx context.Context, ev lie.AdjacencyEvent) {
	jsPath := fmt.Sprintf(".rift.interface{.name==%q}", ev.InterfaceName)

	adj := &adjacencyTelemetry{
		State: ev.State.String(),
	}
	if ev.Neighbor != nil {
		sysID := int64(ev.Neighbor.SystemID)
		level := int8(ev.Neighbor.Level)
		adj.NeighborSystemID = &sysID
		adj.NeighborLevel = &level
		adj.NeighborAddress = ev.Neighbor.Address.String()
	}

	telem := interfaceTelemetry{Adjacency: adj}

	if err := a.ndk.UpdateTelemetry(ctx, jsPath, telem); err != nil {
		a.logger.Warn("telemetry update failed",
			"interface", ev.InterfaceName,
			"error", err,
		)
	}
}

func (a *Agent) keepAlive(ctx context.Context) {
	ticker := time.NewTicker(a.ndk.KeepaliveInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := a.ndk.KeepAlive(ctx); err != nil {
				a.logger.Error("keepalive failed", "error", err)
			}
		}
	}
}
