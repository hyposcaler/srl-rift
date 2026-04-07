package spf

import (
	"net/netip"

	"github.com/hyposcaler/srl-rift/internal/encoding"
)

// NextHop represents a single next-hop for a computed route.
type NextHop struct {
	NeighborID encoding.SystemIDType
	Address    netip.Addr
	Interface  string // SRL interface name (e.g. "ethernet-1/1")
}

// Route is a single RIB entry computed by SPF.
type Route struct {
	Prefix    string
	Metric    encoding.MetricType
	RouteType encoding.RouteType
	NextHops  []NextHop
}

// RIB is the Routing Information Base produced by SPF computation.
type RIB map[string]*Route
