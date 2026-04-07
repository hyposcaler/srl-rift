package agent

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/hyposcaler/srl-rift/internal/config"
	"github.com/hyposcaler/srl-rift/internal/encoding"
	"github.com/hyposcaler/srl-rift/internal/lie"
	"github.com/hyposcaler/srl-rift/internal/ndk"
	"github.com/hyposcaler/srl-rift/internal/spf"
	"github.com/hyposcaler/srl-rift/internal/tie"
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

	// Flood engine for TIE origination/flooding.
	floodEngine *tie.FloodEngine
	floodRecvCh chan transport.ReceivedPacket // shared by all flood recv loops

	// SPF engine for route computation.
	spfEngine *spf.Engine

	// Tracks routes currently programmed in FIB via NDK.
	programmedRIB map[string]programmedRoute

	mu sync.Mutex
}

const networkInstance = "default"

// programmedRoute tracks a route installed in the FIB.
type programmedRoute struct {
	nhgName string
	metric  uint32
	nhAddrs []netip.Addr
}

type interfaceState struct {
	cancel      context.CancelFunc
	transport   *transport.InterfaceTransport
	lieIface    *lie.Interface
	floodCancel context.CancelFunc // for flood recv goroutine, nil if not active
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

	a.logger.Info("configuration received, starting engines",
		"system_id", a.cfg.SystemID,
		"level", a.cfg.Level,
		"interfaces", len(a.cfg.Interfaces),
	)

	// Discover local prefixes for TIE origination.
	localPrefixes := a.discoverLocalPrefixes()

	// Create and start flood engine.
	a.floodRecvCh = make(chan transport.ReceivedPacket, 128)
	a.floodEngine = tie.NewFloodEngine(
		a.cfg.SystemID,
		a.cfg.Level,
		fmt.Sprintf("rift-%d", a.cfg.SystemID),
		localPrefixes,
		a.logger,
	)

	go func() {
		if err := a.floodEngine.Run(ctx); err != nil && ctx.Err() == nil {
			a.logger.Error("flood engine stopped", "error", err)
		}
	}()

	// Create SPF engine.
	a.spfEngine = spf.NewEngine(
		a.cfg.SystemID,
		a.cfg.Level,
		a.floodEngine.LSDB(),
		a.floodEngine.Adjacencies,
		a.logger,
	)

	// Flood send loop: reads from flood engine, dispatches to transport.
	go a.floodSendLoop(ctx)

	// Flood recv relay: reads from shared floodRecvCh, sends to flood engine.
	go a.floodRecvRelay(ctx)

	// Start interfaces that are configured.
	a.startConfiguredInterfaces(ctx)

	// Main event loop.
	return a.eventLoop(ctx)
}

// Close cleans up agent resources.
func (a *Agent) Close() {
	// Withdraw routes before tearing down interfaces.
	a.withdrawAllRoutes(context.Background())

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
	if iface.floodCancel != nil {
		iface.floodCancel()
	}
	iface.cancel()
	iface.transport.Close()
	delete(a.interfaces, name)
	a.logger.Info("interface stopped", "interface", name)
}

// eventLoop processes interface, config, and adjacency events.
func (a *Agent) eventLoop(ctx context.Context) error {
	lsdbTicker := time.NewTicker(5 * time.Second)
	defer lsdbTicker.Stop()

	// SPF debounce timer: fires 100ms after last LSDB change.
	var spfTimer *time.Timer
	var spfTimerC <-chan time.Time

	for {
		select {
		case <-ctx.Done():
			if spfTimer != nil {
				spfTimer.Stop()
			}
			return nil

		case ev := <-a.notifHandler.InterfaceCh:
			a.handleInterfaceEvent(ctx, ev)

		case ev := <-a.notifHandler.ConfigCh:
			a.handleConfigChange(ctx, ev)

		case ev := <-a.adjEventCh:
			a.handleAdjacencyEvent(ctx, ev)

		case <-a.floodEngine.LSDBChangeCh:
			if spfTimer == nil {
				spfTimer = time.NewTimer(100 * time.Millisecond)
				spfTimerC = spfTimer.C
			} else {
				spfTimer.Reset(100 * time.Millisecond)
			}

		case <-spfTimerC:
			a.spfEngine.Run()

			// Positive disaggregation: spines check if any south
			// neighbors are unreachable from same-level peers.
			if a.cfg.Level > encoding.LeafLevel {
				a.runDisaggregation()
			}

			a.syncRoutes(ctx, a.spfEngine.RIB())
			a.updateRIBTelemetry(ctx)

		case <-lsdbTicker.C:
			a.updateLSDBTelemetry(ctx)
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

// handleAdjacencyEvent processes FSM state changes, manages flood sockets,
// and notifies the flood engine.
func (a *Agent) handleAdjacencyEvent(ctx context.Context, ev lie.AdjacencyEvent) {
	a.logger.Info("adjacency state change",
		"interface", ev.InterfaceName,
		"state", ev.State.String(),
	)

	a.updateAdjacencyTelemetry(ctx, ev)

	if a.floodEngine == nil {
		return
	}

	a.mu.Lock()
	iface, ok := a.interfaces[ev.InterfaceName]
	a.mu.Unlock()
	if !ok {
		return
	}

	if ev.State == lie.ThreeWay && ev.Neighbor != nil {
		// Open flood socket and start recv loop.
		if err := iface.transport.OpenFloodSocket(); err != nil {
			a.logger.Error("open flood socket failed",
				"interface", ev.InterfaceName,
				"error", err,
			)
			return
		}

		if iface.floodCancel == nil {
			floodCtx, floodCancel := context.WithCancel(ctx)
			iface.floodCancel = floodCancel
			go func() {
				if err := iface.transport.FloodRecvLoop(floodCtx, a.floodRecvCh); err != nil && floodCtx.Err() == nil {
					a.logger.Error("flood recv loop error",
						"interface", ev.InterfaceName,
						"error", err,
					)
				}
			}()
		}

		// Notify flood engine of adjacency up.
		a.floodEngine.AdjChangeCh <- tie.AdjacencyChange{
			InterfaceName: ev.InterfaceName,
			Info: &tie.AdjacencyInfo{
				InterfaceName: ev.InterfaceName,
				NeighborID:    ev.Neighbor.SystemID,
				NeighborLevel: ev.Neighbor.Level,
				NeighborAddr:  ev.Neighbor.Address,
				FloodPort:     ev.Neighbor.FloodPort,
				LocalLinkID:   encoding.LinkIDType(iface.transport.LocalID()),
			},
		}
	} else if ev.State != lie.ThreeWay {
		// Adjacency dropped. Notify flood engine and close flood socket.
		a.floodEngine.AdjChangeCh <- tie.AdjacencyChange{
			InterfaceName: ev.InterfaceName,
		}

		if iface.floodCancel != nil {
			iface.floodCancel()
			iface.floodCancel = nil
		}
		iface.transport.CloseFloodSocket()
	}
}

// nhgName returns a deterministic NHG name for a prefix, e.g. "rift_10.0.1.1_32_sdk".
// NDK requires NHG names to end with "_sdk" suffix.
func nhgName(prefix string) string {
	s := strings.ReplaceAll(prefix, "/", "_")
	return "rift_" + s + "_sdk"
}

// nhAddrsEqual returns true if two sorted next-hop address slices are equal.
func nhAddrsEqual(a, b []netip.Addr) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// syncRoutes pushes the full RIB to NDK using SyncStart/SyncEnd. SyncEnd
// runDisaggregation computes positive disaggregation prefixes and sends
// them to the flood engine for TIE origination or withdrawal.
func (a *Agent) runDisaggregation() {
	entries := a.floodEngine.LSDB().Snapshot()
	adjs := a.floodEngine.Adjacencies()
	southRIB := a.spfEngine.SouthRIB()
	if southRIB == nil {
		return
	}

	disaggPrefixes := spf.ComputeDisaggregation(
		a.cfg.SystemID, a.cfg.Level, adjs, entries, southRIB, a.logger)

	// Convert to encoding.PrefixEntry for the flood engine.
	var prefixEntries []encoding.PrefixEntry
	for _, dp := range disaggPrefixes {
		prefixEntries = append(prefixEntries, encoding.PrefixEntry{
			Prefix:     dp.Prefix,
			Attributes: encoding.PrefixAttributes{Metric: dp.Distance},
		})
	}

	// Non-blocking send: if the flood engine hasn't consumed the last update,
	// drain it and send the new one.
	select {
	case <-a.floodEngine.DisaggUpdateCh:
	default:
	}
	a.floodEngine.DisaggUpdateCh <- prefixEntries
}

// automatically removes any routes not added during the sync window, so
// we must push ALL current routes every time (not just deltas). NHGs are
// only created/updated when their next-hops actually changed.
func (a *Agent) syncRoutes(ctx context.Context, newRIB spf.RIB) {
	if a.programmedRIB == nil {
		a.programmedRIB = make(map[string]programmedRoute)
	}

	// Build the full set of routes and identify NHGs that need updating.
	var allRoutes []ndk.RouteEntry
	type nhgUpdate struct {
		name  string
		addrs []netip.Addr
	}
	var nhgsToUpdate []nhgUpdate
	newProgrammed := make(map[string]programmedRoute)

	for prefix, route := range newRIB {
		if len(route.NextHops) == 0 {
			continue
		}
		name := nhgName(prefix)
		metric := uint32(route.Metric)

		addrs := make([]netip.Addr, 0, len(route.NextHops))
		for _, nh := range route.NextHops {
			addrs = append(addrs, nh.Address)
		}

		allRoutes = append(allRoutes, ndk.RouteEntry{
			Prefix:  prefix,
			NHGName: name,
			Metric:  metric,
		})

		newProgrammed[prefix] = programmedRoute{
			nhgName: name,
			metric:  metric,
			nhAddrs: addrs,
		}

		// Only update NHG if next-hops changed.
		prev, exists := a.programmedRIB[prefix]
		if !exists || !nhAddrsEqual(prev.nhAddrs, addrs) {
			nhgsToUpdate = append(nhgsToUpdate, nhgUpdate{name: name, addrs: addrs})
		}
	}

	if len(allRoutes) == 0 && len(a.programmedRIB) == 0 {
		return
	}

	// Identify stale NHGs to clean up after SyncEnd removes their routes.
	var staleNHGs []string
	for prefix := range a.programmedRIB {
		if _, exists := newProgrammed[prefix]; !exists {
			staleNHGs = append(staleNHGs, nhgName(prefix))
		}
	}

	a.logger.Info("syncing routes to FIB",
		"total", len(allRoutes),
		"nhg_updates", len(nhgsToUpdate),
		"stale_nhgs", len(staleNHGs),
	)

	// Create/update NHGs before routes reference them.
	for _, nhg := range nhgsToUpdate {
		if err := a.ndk.AddOrUpdateNextHopGroup(ctx, nhg.name, networkInstance, nhg.addrs); err != nil {
			a.logger.Error("nhg add failed", "name", nhg.name, "error", err)
		}
	}

	// SyncStart, push ALL routes, SyncEnd.
	if err := a.ndk.RouteSyncStart(ctx); err != nil {
		a.logger.Error("route sync start failed", "error", err)
		return
	}
	if err := a.ndk.AddOrUpdateRoutes(ctx, networkInstance, allRoutes); err != nil {
		a.logger.Error("route add failed", "error", err)
	}
	if err := a.ndk.RouteSyncEnd(ctx); err != nil {
		a.logger.Error("route sync end failed", "error", err)
	}

	// Clean up NHGs for removed routes (after SyncEnd removed the routes).
	if err := a.ndk.DeleteNextHopGroups(ctx, networkInstance, staleNHGs); err != nil {
		a.logger.Error("nhg delete failed", "error", err)
	}

	a.programmedRIB = newProgrammed
}

// withdrawAllRoutes removes all programmed routes from the FIB.
func (a *Agent) withdrawAllRoutes(ctx context.Context) {
	if len(a.programmedRIB) == 0 {
		return
	}

	var prefixes []string
	var nhgNames []string
	for prefix := range a.programmedRIB {
		prefixes = append(prefixes, prefix)
		nhgNames = append(nhgNames, nhgName(prefix))
	}

	a.logger.Info("withdrawing all routes", "count", len(prefixes))

	if err := a.ndk.DeleteRoutes(ctx, networkInstance, prefixes); err != nil {
		a.logger.Error("withdraw routes failed", "error", err)
	}
	if err := a.ndk.DeleteNextHopGroups(ctx, networkInstance, nhgNames); err != nil {
		a.logger.Error("withdraw nhgs failed", "error", err)
	}

	a.programmedRIB = make(map[string]programmedRoute)
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

type lsdbTIESummary struct {
	Direction         string
	Originator        int64
	TIEType           string
	TIENr             int32
	SequenceNumber    int64
	RemainingLifetime int32
	SelfOriginated    bool
}

// formatLSDBSummary creates a human-readable summary of LSDB TIE entries.
func formatLSDBSummary(ties []lsdbTIESummary) string {
	if len(ties) == 0 {
		return "empty"
	}
	var s string
	for i, t := range ties {
		if i > 0 {
			s += " | "
		}
		origin := ""
		if t.SelfOriginated {
			origin = " (self)"
		}
		s += fmt.Sprintf("%s-%s:%d#%d-seq%d-lt%d%s",
			t.Direction, t.TIEType, t.Originator, t.TIENr,
			t.SequenceNumber, t.RemainingLifetime, origin)
	}
	return s
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

// floodSendLoop reads outbound flood packets from the engine and dispatches
// them to the correct interface transport.
func (a *Agent) floodSendLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case fp := <-a.floodEngine.FloodSendCh:
			a.mu.Lock()
			iface, ok := a.interfaces[fp.InterfaceName]
			a.mu.Unlock()
			if !ok {
				continue
			}
			if err := iface.transport.SendFlood(fp.Packet, fp.DestAddr, fp.DestPort, fp.RemainingLifetime); err != nil {
				a.logger.Debug("flood send error",
					"interface", fp.InterfaceName,
					"error", err,
				)
			}
		}
	}
}

// floodRecvRelay reads decoded flood packets from the shared channel and
// forwards them to the flood engine.
func (a *Agent) floodRecvRelay(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case rp := <-a.floodRecvCh:
			a.floodEngine.FloodRecvCh <- tie.ReceivedFloodPkt{
				Packet:  rp.Packet,
				SrcAddr: rp.SrcAddr.String(),
				IfName:  rp.IfName,
			}
		}
	}
}

// discoverLocalPrefixes discovers all locally attached prefixes for TIE
// origination: loopback, RIFT link prefixes, and host-facing subnets.
func (a *Agent) discoverLocalPrefixes() []tie.LocalPrefix {
	var prefixes []tie.LocalPrefix

	// Build set of RIFT interface Linux names for dedup.
	riftSubs := make(map[string]struct{})
	for ifName := range a.cfg.Interfaces {
		_, sub := transport.LinuxInterfaceNames(ifName)
		riftSubs[sub] = struct{}{}
	}

	// Discover all IPv4 prefixes in srbase-default.
	allPrefixes, err := transport.DiscoverAllPrefixes()
	if err != nil {
		a.logger.Warn("discover all prefixes failed", "error", err)
	}

	for ifName, prefix := range allPrefixes {
		if ifName == "system0.0" {
			prefixes = append(prefixes, tie.LocalPrefix{
				Prefix:   tie.IPv4ToPrefix(prefix.Addr(), 32),
				Loopback: true,
				Metric:   1,
			})
			a.logger.Info("loopback prefix", "addr", prefix.Addr())
			continue
		}
		prefixes = append(prefixes, tie.LocalPrefix{
			Prefix:   tie.IPv4ToPrefix(prefix.Addr(), int8(prefix.Bits())),
			Loopback: false,
			Metric:   1,
		})
		if _, isRift := riftSubs[ifName]; isRift {
			a.logger.Info("link prefix", "interface", ifName, "prefix", prefix)
		} else {
			a.logger.Info("local prefix", "interface", ifName, "prefix", prefix)
		}
	}

	return prefixes
}

// ribSummaryString builds a human-readable RIB summary.
func (a *Agent) ribSummaryString() string {
	if a.spfEngine == nil {
		return ""
	}
	rib := a.spfEngine.RIB()
	if len(rib) == 0 {
		return ""
	}
	var summary string
	for prefix, route := range rib {
		if summary != "" {
			summary += " | "
		}
		var nhAddrs []string
		for _, nh := range route.NextHops {
			nhAddrs = append(nhAddrs, nh.Address.String())
		}
		summary += fmt.Sprintf("%s metric=%d nh=%v", prefix, route.Metric, nhAddrs)
	}
	return summary
}

// updateRIBTelemetry triggers a combined telemetry push after SPF runs.
func (a *Agent) updateRIBTelemetry(ctx context.Context) {
	a.updateStateTelemetry(ctx)
}

// updateLSDBTelemetry triggers a combined telemetry push on LSDB tick.
func (a *Agent) updateLSDBTelemetry(ctx context.Context) {
	a.updateStateTelemetry(ctx)
}

// updateStateTelemetry pushes both LSDB and RIB summaries in a single
// telemetry update so one does not overwrite the other.
func (a *Agent) updateStateTelemetry(ctx context.Context) {
	if a.floodEngine == nil {
		return
	}

	var ties []lsdbTIESummary
	a.floodEngine.LSDB().ForEachSorted(func(id encoding.TIEID, entry *tie.LSDBEntry) bool {
		dir := "south"
		if id.Direction == encoding.TieDirectionNorth {
			dir = "north"
		}
		ttype := "node"
		switch id.TIEType {
		case encoding.TIETypePrefixTIEType:
			ttype = "prefix"
		case encoding.TIETypePositiveDisaggregationPrefixTIEType:
			ttype = "positive-disaggregation"
		}
		ties = append(ties, lsdbTIESummary{
			Direction:         dir,
			Originator:        id.Originator,
			TIEType:           ttype,
			TIENr:             id.TIENr,
			SequenceNumber:    entry.Packet.Header.SeqNr,
			RemainingLifetime: entry.RemainingLifetime,
			SelfOriginated:    entry.SelfOriginated,
		})
		return true
	})

	telem := struct {
		LSDBSummary string `json:"lsdb-summary"`
		RIBSummary  string `json:"rib-summary,omitempty"`
	}{
		LSDBSummary: formatLSDBSummary(ties),
		RIBSummary:  a.ribSummaryString(),
	}

	if err := a.ndk.UpdateTelemetry(ctx, ".rift", telem); err != nil {
		a.logger.Info("state telemetry update failed", "error", err, "ties", len(ties))
	}
}
