// Package transport manages UDP sockets for RIFT protocol exchange.
// Each RIFT-enabled interface gets a multicast socket pair for LIE exchange
// (recv in srbase, send in srbase-default) and an optional unicast flood
// socket for TIE/TIDE/TIRE exchange (in srbase-default on port 915).
package transport

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"runtime"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/hyposcaler/srl-rift/internal/encoding"
)

const (
	riftMcastAddr = "224.0.0.121"
	riftLIEPort   = 914
	riftFloodPort = 915
	recvBufSize   = 9000
)

// ReceivedPacket is a decoded RIFT packet from the wire.
type ReceivedPacket struct {
	Packet  *encoding.ProtocolPacket
	SrcAddr netip.Addr
	IfName  string // SRL interface name
}

// InterfaceTransport manages send/recv sockets for one RIFT interface.
type InterfaceTransport struct {
	ifName    string              // SRL name, e.g. "ethernet-1/1"
	parent    string              // Linux parent, e.g. "e1-1"
	sub       string              // Linux sub, e.g. "e1-1.0"
	localAddr netip.Addr          // IPv4 of subinterface
	linkID    encoding.LinkIDType // ifindex of subinterface
	recvConn  net.PacketConn      // mcast recv (created in srbase)
	sendConn  net.PacketConn      // mcast send (srbase-default)
	floodConn net.PacketConn      // unicast flood (srbase-default, port 915)
	logger    *slog.Logger
}

// LinuxInterfaceNames maps an SR Linux interface name to Linux names.
// "ethernet-1/1" -> parent "e1-1", sub "e1-1.0"
func LinuxInterfaceNames(srlName string) (parent, sub string) {
	s := srlName
	s = strings.Replace(s, "ethernet-", "e", 1)
	s = strings.Replace(s, "/", "-", 1)
	return s, s + ".0"
}

// New creates both sockets for an interface. localAddr is the IPv4 address
// of the subinterface (discovered by the caller from Linux).
func New(ctx context.Context, srlName string, localAddr netip.Addr, logger *slog.Logger) (*InterfaceTransport, error) {
	parent, sub := LinuxInterfaceNames(srlName)
	logger = logger.With("interface", srlName, "parent", parent, "sub", sub)

	// Get subinterface index for link ID (in srbase-default).
	linkID, err := getIfIndexInNS(sub, "/var/run/netns/srbase-default")
	if err != nil {
		return nil, fmt.Errorf("interface lookup %s: %w", sub, err)
	}

	t := &InterfaceTransport{
		ifName:    srlName,
		parent:    parent,
		sub:       sub,
		localAddr: localAddr,
		linkID:    encoding.LinkIDType(linkID),
		logger:    logger,
	}

	// Create multicast receive socket in srbase namespace.
	recvConn, err := createMcastRecvSocket(parent)
	if err != nil {
		return nil, fmt.Errorf("mcast recv socket: %w", err)
	}
	t.recvConn = recvConn

	// Create multicast send socket in srbase-default namespace.
	sendConn, err := createMcastSendSocket(localAddr, sub)
	if err != nil {
		recvConn.Close()
		return nil, fmt.Errorf("mcast send socket: %w", err)
	}
	t.sendConn = sendConn

	logger.Info("transport ready",
		"local_addr", localAddr.String(),
		"link_id", t.linkID,
	)
	return t, nil
}

// getIfIndexInNS gets the interface index in a given network namespace.
func getIfIndexInNS(ifName, nsPath string) (int, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	origNS, err := unix.Open("/proc/self/ns/net", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return 0, fmt.Errorf("open current netns: %w", err)
	}
	defer unix.Close(origNS)

	targetNS, err := unix.Open(nsPath, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return 0, fmt.Errorf("open netns %s: %w", nsPath, err)
	}
	defer unix.Close(targetNS)

	if err := unix.Setns(targetNS, unix.CLONE_NEWNET); err != nil {
		return 0, fmt.Errorf("setns: %w", err)
	}
	defer func() {
		_ = unix.Setns(origNS, unix.CLONE_NEWNET)
	}()

	iface, err := net.InterfaceByName(ifName)
	if err != nil {
		return 0, err
	}
	return iface.Index, nil
}

// RecvLoop reads from the multicast receive socket, decodes packets,
// and sends them on out. Blocks until ctx is cancelled.
func (t *InterfaceTransport) RecvLoop(ctx context.Context, out chan<- ReceivedPacket) error {
	buf := make([]byte, recvBufSize)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		n, addr, err := t.recvConn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			t.logger.Warn("recv error", "error", err)
			continue
		}

		srcAddr, err := extractAddr(addr)
		if err != nil {
			t.logger.Warn("bad source address", "addr", addr, "error", err)
			continue
		}

		// Skip our own packets.
		if srcAddr == t.localAddr {
			continue
		}

		pkt, err := decodePacket(buf[:n])
		if err != nil {
			t.logger.Debug("decode error", "error", err, "src", srcAddr)
			continue
		}

		// Only pass LIE packets to the FSM.
		if pkt.Content.LIE == nil {
			continue
		}

		select {
		case out <- ReceivedPacket{Packet: pkt, SrcAddr: srcAddr, IfName: t.ifName}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// SendLIE encodes and sends a ProtocolPacket as multicast to 224.0.0.121:914.
func (t *InterfaceTransport) SendLIE(pkt *encoding.ProtocolPacket) error {
	data, err := encodePacket(pkt)
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}

	dst := &net.UDPAddr{
		IP:   net.ParseIP(riftMcastAddr),
		Port: riftLIEPort,
	}
	_, err = t.sendConn.WriteTo(data, dst)
	if err != nil {
		return fmt.Errorf("send: %w", err)
	}
	return nil
}

// LocalAddr returns the interface's IPv4 address.
func (t *InterfaceTransport) LocalAddr() netip.Addr {
	return t.localAddr
}

// LocalID returns the link ID (ifindex) for this interface.
func (t *InterfaceTransport) LocalID() encoding.LinkIDType {
	return t.linkID
}

// Close closes all sockets.
func (t *InterfaceTransport) Close() error {
	var errs []error
	if t.recvConn != nil {
		if err := t.recvConn.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if t.sendConn != nil {
		if err := t.sendConn.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if t.floodConn != nil {
		if err := t.floodConn.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("close: %v", errs)
	}
	return nil
}

// OpenFloodSocket creates the unicast flood socket (port 915) in
// srbase-default. Call this when an adjacency reaches ThreeWay.
func (t *InterfaceTransport) OpenFloodSocket() error {
	if t.floodConn != nil {
		return nil // already open
	}
	conn, err := createUnicastSocket(t.localAddr, t.sub)
	if err != nil {
		return fmt.Errorf("flood socket: %w", err)
	}
	t.floodConn = conn
	t.logger.Info("flood socket opened", "port", riftFloodPort)
	return nil
}

// CloseFloodSocket closes the unicast flood socket.
func (t *InterfaceTransport) CloseFloodSocket() {
	if t.floodConn != nil {
		t.floodConn.Close()
		t.floodConn = nil
		t.logger.Info("flood socket closed")
	}
}

// FloodRecvLoop reads TIE/TIDE/TIRE packets from the unicast flood socket
// and sends them on out. Blocks until ctx is cancelled.
func (t *InterfaceTransport) FloodRecvLoop(ctx context.Context, out chan<- ReceivedPacket) error {
	if t.floodConn == nil {
		return fmt.Errorf("flood socket not open")
	}
	buf := make([]byte, recvBufSize)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		n, addr, err := t.floodConn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			t.logger.Warn("flood recv error", "error", err)
			continue
		}

		srcAddr, err := extractAddr(addr)
		if err != nil {
			t.logger.Warn("flood bad source", "addr", addr, "error", err)
			continue
		}

		// Skip our own packets.
		if srcAddr == t.localAddr {
			continue
		}

		pkt, err := decodePacket(buf[:n])
		if err != nil {
			t.logger.Debug("flood decode error", "error", err, "src", srcAddr)
			continue
		}

		select {
		case out <- ReceivedPacket{Packet: pkt, SrcAddr: srcAddr, IfName: t.ifName}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// SendFlood encodes and sends a ProtocolPacket via unicast to the given
// destination. remainingLifetime is set in the security envelope.
func (t *InterfaceTransport) SendFlood(pkt *encoding.ProtocolPacket, destAddr netip.Addr, destPort int, remainingLifetime encoding.LifeTimeInSecType) error {
	if t.floodConn == nil {
		return fmt.Errorf("flood socket not open")
	}
	data, err := encodeFloodPacket(pkt, remainingLifetime)
	if err != nil {
		return fmt.Errorf("encode flood: %w", err)
	}
	dst := &net.UDPAddr{
		IP:   destAddr.AsSlice(),
		Port: destPort,
	}
	_, err = t.floodConn.WriteTo(data, dst)
	if err != nil {
		return fmt.Errorf("send flood: %w", err)
	}
	return nil
}

// createMcastRecvSocket creates a UDP multicast receive socket in the srbase
// namespace, bound to the parent interface. The fd remains valid after
// switching back to srbase-default.
func createMcastRecvSocket(parentIf string) (net.PacketConn, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Save current namespace.
	origNS, err := unix.Open("/proc/self/ns/net", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open current netns: %w", err)
	}
	defer unix.Close(origNS)

	// Switch to srbase.
	srbaseNS, err := unix.Open("/var/run/netns/srbase", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open srbase netns: %w", err)
	}
	defer unix.Close(srbaseNS)

	if err := unix.Setns(srbaseNS, unix.CLONE_NEWNET); err != nil {
		return nil, fmt.Errorf("setns srbase: %w", err)
	}

	// Restore original namespace on exit.
	defer func() {
		_ = unix.Setns(origNS, unix.CLONE_NEWNET)
	}()

	// Get parent interface index in srbase.
	iface, err := net.InterfaceByName(parentIf)
	if err != nil {
		return nil, fmt.Errorf("interface %s in srbase: %w", parentIf, err)
	}

	// Create raw UDP socket.
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, unix.IPPROTO_UDP)
	if err != nil {
		return nil, fmt.Errorf("socket: %w", err)
	}

	// Set socket options.
	if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("SO_REUSEADDR: %w", err)
	}
	if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEPORT, 1); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("SO_REUSEPORT: %w", err)
	}

	// Bind to parent interface.
	if err := unix.SetsockoptString(fd, unix.SOL_SOCKET, unix.SO_BINDTODEVICE, parentIf); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("SO_BINDTODEVICE %s: %w", parentIf, err)
	}

	// Bind to 0.0.0.0:914.
	sa := &unix.SockaddrInet4{Port: riftLIEPort}
	if err := unix.Bind(fd, sa); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("bind :914: %w", err)
	}

	// Join multicast group on parent interface.
	mcastIP := net.ParseIP(riftMcastAddr).To4()
	mreqn := &unix.IPMreqn{
		Multiaddr: [4]byte{mcastIP[0], mcastIP[1], mcastIP[2], mcastIP[3]},
		Ifindex:   int32(iface.Index),
	}
	if err := unix.SetsockoptIPMreqn(fd, unix.IPPROTO_IP, unix.IP_ADD_MEMBERSHIP, mreqn); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("IP_ADD_MEMBERSHIP: %w", err)
	}

	// Wrap fd into Go net.PacketConn.
	f := os.NewFile(uintptr(fd), fmt.Sprintf("mcast-recv-%s", parentIf))
	conn, err := net.FilePacketConn(f)
	f.Close() // FilePacketConn dups the fd
	if err != nil {
		return nil, fmt.Errorf("FilePacketConn: %w", err)
	}

	return conn, nil
}

// createMcastSendSocket creates a UDP multicast send socket in the
// srbase-default namespace, bound to the subinterface.
func createMcastSendSocket(localAddr netip.Addr, subIf string) (net.PacketConn, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Save current namespace.
	origNS, err := unix.Open("/proc/self/ns/net", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open current netns: %w", err)
	}
	defer unix.Close(origNS)

	// Switch to srbase-default.
	defaultNS, err := unix.Open("/var/run/netns/srbase-default", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open srbase-default netns: %w", err)
	}
	defer unix.Close(defaultNS)

	if err := unix.Setns(defaultNS, unix.CLONE_NEWNET); err != nil {
		return nil, fmt.Errorf("setns srbase-default: %w", err)
	}
	defer func() {
		_ = unix.Setns(origNS, unix.CLONE_NEWNET)
	}()

	iface, err := net.InterfaceByName(subIf)
	if err != nil {
		return nil, fmt.Errorf("interface %s: %w", subIf, err)
	}

	// Create raw UDP socket.
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, unix.IPPROTO_UDP)
	if err != nil {
		return nil, fmt.Errorf("socket: %w", err)
	}

	if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("SO_REUSEADDR: %w", err)
	}

	// Bind to local address (ephemeral port).
	addr4 := localAddr.As4()
	sa := &unix.SockaddrInet4{Addr: addr4}
	if err := unix.Bind(fd, sa); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("bind %s: %w", localAddr, err)
	}

	// Set multicast interface.
	mreqn := &unix.IPMreqn{
		Ifindex: int32(iface.Index),
	}
	copy(mreqn.Address[:], addr4[:])
	if err := unix.SetsockoptIPMreqn(fd, unix.IPPROTO_IP, unix.IP_MULTICAST_IF, mreqn); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("IP_MULTICAST_IF: %w", err)
	}

	// TTL = 1 (link-local).
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_MULTICAST_TTL, 1); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("IP_MULTICAST_TTL: %w", err)
	}

	// Disable loopback.
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_MULTICAST_LOOP, 0); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("IP_MULTICAST_LOOP: %w", err)
	}

	// Wrap fd into Go net.PacketConn.
	f := os.NewFile(uintptr(fd), fmt.Sprintf("mcast-send-%s", subIf))
	conn, err := net.FilePacketConn(f)
	f.Close()
	if err != nil {
		return nil, fmt.Errorf("FilePacketConn: %w", err)
	}

	return conn, nil
}

// createUnicastSocket creates a UDP socket in srbase-default bound to
// localAddr:915 on the given subinterface, for TIE/TIDE/TIRE exchange.
func createUnicastSocket(localAddr netip.Addr, subIf string) (net.PacketConn, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	origNS, err := unix.Open("/proc/self/ns/net", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open current netns: %w", err)
	}
	defer unix.Close(origNS)

	defaultNS, err := unix.Open("/var/run/netns/srbase-default", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open srbase-default netns: %w", err)
	}
	defer unix.Close(defaultNS)

	if err := unix.Setns(defaultNS, unix.CLONE_NEWNET); err != nil {
		return nil, fmt.Errorf("setns srbase-default: %w", err)
	}
	defer func() {
		_ = unix.Setns(origNS, unix.CLONE_NEWNET)
	}()

	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, unix.IPPROTO_UDP)
	if err != nil {
		return nil, fmt.Errorf("socket: %w", err)
	}

	if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("SO_REUSEADDR: %w", err)
	}
	if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEPORT, 1); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("SO_REUSEPORT: %w", err)
	}

	// Bind to subinterface.
	if err := unix.SetsockoptString(fd, unix.SOL_SOCKET, unix.SO_BINDTODEVICE, subIf); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("SO_BINDTODEVICE %s: %w", subIf, err)
	}

	// Bind to localAddr:915.
	addr4 := localAddr.As4()
	sa := &unix.SockaddrInet4{Port: riftFloodPort, Addr: addr4}
	if err := unix.Bind(fd, sa); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("bind %s:%d: %w", localAddr, riftFloodPort, err)
	}

	f := os.NewFile(uintptr(fd), fmt.Sprintf("flood-%s", subIf))
	conn, err := net.FilePacketConn(f)
	f.Close()
	if err != nil {
		return nil, fmt.Errorf("FilePacketConn: %w", err)
	}

	return conn, nil
}

// DiscoverInterfacePrefix gets the IPv4 prefix (address + length) of a
// Linux subinterface. The subinterface lives in srbase-default.
func DiscoverInterfacePrefix(ifName string) (netip.Prefix, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	origNS, err := unix.Open("/proc/self/ns/net", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("open current netns: %w", err)
	}
	defer unix.Close(origNS)

	defaultNS, err := unix.Open("/var/run/netns/srbase-default", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("open srbase-default netns: %w", err)
	}
	defer unix.Close(defaultNS)

	if err := unix.Setns(defaultNS, unix.CLONE_NEWNET); err != nil {
		return netip.Prefix{}, fmt.Errorf("setns srbase-default: %w", err)
	}
	defer func() {
		_ = unix.Setns(origNS, unix.CLONE_NEWNET)
	}()

	iface, err := net.InterfaceByName(ifName)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("interface %s: %w", ifName, err)
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("addrs %s: %w", ifName, err)
	}
	for _, a := range addrs {
		prefix, err := netip.ParsePrefix(a.String())
		if err != nil {
			continue
		}
		if prefix.Addr().Is4() {
			return prefix, nil
		}
	}
	return netip.Prefix{}, fmt.Errorf("no IPv4 prefix on %s", ifName)
}

// DiscoverInterfaceAddr gets the IPv4 address of a Linux subinterface.
// The subinterface lives in srbase-default, so we switch namespaces.
func DiscoverInterfaceAddr(ifName string) (netip.Addr, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	origNS, err := unix.Open("/proc/self/ns/net", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("open current netns: %w", err)
	}
	defer unix.Close(origNS)

	defaultNS, err := unix.Open("/var/run/netns/srbase-default", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("open srbase-default netns: %w", err)
	}
	defer unix.Close(defaultNS)

	if err := unix.Setns(defaultNS, unix.CLONE_NEWNET); err != nil {
		return netip.Addr{}, fmt.Errorf("setns srbase-default: %w", err)
	}
	defer func() {
		_ = unix.Setns(origNS, unix.CLONE_NEWNET)
	}()

	iface, err := net.InterfaceByName(ifName)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("interface %s: %w", ifName, err)
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return netip.Addr{}, fmt.Errorf("addrs %s: %w", ifName, err)
	}
	for _, a := range addrs {
		prefix, err := netip.ParsePrefix(a.String())
		if err != nil {
			continue
		}
		addr := prefix.Addr()
		if addr.Is4() {
			return addr, nil
		}
	}
	return netip.Addr{}, fmt.Errorf("no IPv4 address on %s", ifName)
}

// encodePacket serializes a ProtocolPacket into a security envelope.
func encodePacket(pkt *encoding.ProtocolPacket) ([]byte, error) {
	var payload bytes.Buffer
	enc := encoding.NewEncoder(&payload)
	if err := enc.EncodeProtocolPacket(pkt); err != nil {
		return nil, fmt.Errorf("encode protocol packet: %w", err)
	}

	env := &encoding.SecurityEnvelope{
		Payload: payload.Bytes(),
	}
	var out bytes.Buffer
	if err := encoding.EncodeEnvelope(&out, env); err != nil {
		return nil, fmt.Errorf("encode envelope: %w", err)
	}
	return out.Bytes(), nil
}

// encodeFloodPacket serializes a ProtocolPacket with the given remaining
// lifetime in the security envelope (used for TIE/TIDE/TIRE).
func encodeFloodPacket(pkt *encoding.ProtocolPacket, remainingLifetime encoding.LifeTimeInSecType) ([]byte, error) {
	var payload bytes.Buffer
	enc := encoding.NewEncoder(&payload)
	if err := enc.EncodeProtocolPacket(pkt); err != nil {
		return nil, fmt.Errorf("encode protocol packet: %w", err)
	}

	env := &encoding.SecurityEnvelope{
		RemainingLifetime: remainingLifetime,
		Payload:           payload.Bytes(),
	}
	var out bytes.Buffer
	if err := encoding.EncodeEnvelope(&out, env); err != nil {
		return nil, fmt.Errorf("encode envelope: %w", err)
	}
	return out.Bytes(), nil
}

// decodePacket deserializes a security envelope into a ProtocolPacket.
func decodePacket(data []byte) (*encoding.ProtocolPacket, error) {
	env, err := encoding.DecodeEnvelope(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode envelope: %w", err)
	}
	dec := encoding.NewDecoder(bytes.NewReader(env.Payload))
	pkt, err := dec.DecodeProtocolPacket()
	if err != nil {
		return nil, fmt.Errorf("decode protocol packet: %w", err)
	}
	return pkt, nil
}

// extractAddr extracts a netip.Addr from a net.Addr.
func extractAddr(addr net.Addr) (netip.Addr, error) {
	switch a := addr.(type) {
	case *net.UDPAddr:
		ip, ok := netip.AddrFromSlice(a.IP)
		if !ok {
			return netip.Addr{}, fmt.Errorf("invalid IP: %v", a.IP)
		}
		return ip.Unmap(), nil
	default:
		host, _, err := net.SplitHostPort(addr.String())
		if err != nil {
			return netip.Addr{}, fmt.Errorf("parse addr %s: %w", addr, err)
		}
		ip, err := netip.ParseAddr(host)
		if err != nil {
			return netip.Addr{}, fmt.Errorf("parse IP %s: %w", host, err)
		}
		return ip.Unmap(), nil
	}
}
