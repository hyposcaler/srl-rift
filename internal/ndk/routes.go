package ndk

import (
	"context"
	"fmt"
	"net/netip"

	ndkgo "github.com/nokia/srlinux-ndk-go/ndk"
)

const routePreference = 250

// prefixToNDK converts a CIDR string (e.g. "10.0.1.1/32") to an NDK IpAddrPrefLenPb.
func prefixToNDK(cidr string) (*ndkgo.IpAddrPrefLenPb, error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return nil, fmt.Errorf("parse prefix %q: %w", cidr, err)
	}
	addr := prefix.Addr()
	ipBytes := addr.As4()
	return &ndkgo.IpAddrPrefLenPb{
		IpAddr: &ndkgo.IpAddressPb{
			IpAddress: ipBytes[:],
		},
		PrefixLength: uint32(prefix.Bits()),
	}, nil
}

// addrToNDK converts a netip.Addr to an NDK IpAddressPb.
func addrToNDK(addr netip.Addr) *ndkgo.IpAddressPb {
	ipBytes := addr.As4()
	return &ndkgo.IpAddressPb{
		IpAddress: ipBytes[:],
	}
}

// RouteSyncStart signals the start of a route sync operation.
func (c *Client) RouteSyncStart(ctx context.Context) error {
	resp, err := c.routeSvc.SyncStart(c.withMD(ctx), &ndkgo.SyncRequest{})
	if err != nil {
		return fmt.Errorf("route sync start: %w", err)
	}
	if resp.GetStatus() != ndkgo.SdkMgrStatus_SDK_MGR_STATUS_SUCCESS {
		return fmt.Errorf("route sync start failed: %s", resp.GetErrorStr())
	}
	return nil
}

// RouteSyncEnd signals the end of a route sync operation.
func (c *Client) RouteSyncEnd(ctx context.Context) error {
	resp, err := c.routeSvc.SyncEnd(c.withMD(ctx), &ndkgo.SyncRequest{})
	if err != nil {
		return fmt.Errorf("route sync end: %w", err)
	}
	if resp.GetStatus() != ndkgo.SdkMgrStatus_SDK_MGR_STATUS_SUCCESS {
		return fmt.Errorf("route sync end failed: %s", resp.GetErrorStr())
	}
	return nil
}

// AddOrUpdateNextHopGroup creates or updates a next-hop group.
func (c *Client) AddOrUpdateNextHopGroup(ctx context.Context, name, netInst string, nexthops []netip.Addr) error {
	nhList := make([]*ndkgo.NextHop, 0, len(nexthops))
	for _, addr := range nexthops {
		nhList = append(nhList, &ndkgo.NextHop{
			ResolveTo: ndkgo.NextHop_RESOLVE_TO_TYPE_DIRECT,
			Type:      ndkgo.NextHop_RESOLUTION_TYPE_REGULAR,
			Nexthop: &ndkgo.NextHop_IpNexthop{
				IpNexthop: addrToNDK(addr),
			},
		})
	}

	resp, err := c.nhgSvc.NextHopGroupAddOrUpdate(c.withMD(ctx), &ndkgo.NextHopGroupRequest{
		GroupInfos: []*ndkgo.NextHopGroupInfo{
			{
				Key: &ndkgo.NextHopGroupKey{
					Name:                name,
					NetworkInstanceName: netInst,
				},
				Data: &ndkgo.NextHopGroup{
					Nexthops: nhList,
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("nhg add %q: %w", name, err)
	}
	if resp.GetStatus() != ndkgo.SdkMgrStatus_SDK_MGR_STATUS_SUCCESS {
		return fmt.Errorf("nhg add %q failed: %s", name, resp.GetErrorStr())
	}
	return nil
}

// DeleteNextHopGroups removes next-hop groups by name.
func (c *Client) DeleteNextHopGroups(ctx context.Context, netInst string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	keys := make([]*ndkgo.NextHopGroupKey, 0, len(names))
	for _, name := range names {
		keys = append(keys, &ndkgo.NextHopGroupKey{
			Name:                name,
			NetworkInstanceName: netInst,
		})
	}
	resp, err := c.nhgSvc.NextHopGroupDelete(c.withMD(ctx), &ndkgo.NextHopGroupDeleteRequest{
		GroupKeys: keys,
	})
	if err != nil {
		return fmt.Errorf("nhg delete: %w", err)
	}
	if resp.GetStatus() != ndkgo.SdkMgrStatus_SDK_MGR_STATUS_SUCCESS {
		return fmt.Errorf("nhg delete failed: %s", resp.GetErrorStr())
	}
	return nil
}

// AddOrUpdateRoutes installs or updates routes. Each route references an NHG by name.
func (c *Client) AddOrUpdateRoutes(ctx context.Context, netInst string, routes []RouteEntry) error {
	if len(routes) == 0 {
		return nil
	}
	infos := make([]*ndkgo.RouteInfo, 0, len(routes))
	for _, r := range routes {
		pfx, err := prefixToNDK(r.Prefix)
		if err != nil {
			return err
		}
		infos = append(infos, &ndkgo.RouteInfo{
			Key: &ndkgo.RouteKey{
				NetworkInstanceName: netInst,
				IpPrefix:            pfx,
			},
			Data: &ndkgo.Route{
				NexthopGroupName: r.NHGName,
				Preference:       routePreference,
				Metric:           r.Metric,
			},
		})
	}
	resp, err := c.routeSvc.RouteAddOrUpdate(c.withMD(ctx), &ndkgo.RouteAddRequest{
		Routes: infos,
	})
	if err != nil {
		return fmt.Errorf("route add: %w", err)
	}
	if resp.GetStatus() != ndkgo.SdkMgrStatus_SDK_MGR_STATUS_SUCCESS {
		return fmt.Errorf("route add failed: %s", resp.GetErrorStr())
	}
	return nil
}

// DeleteRoutes withdraws routes by prefix.
func (c *Client) DeleteRoutes(ctx context.Context, netInst string, prefixes []string) error {
	if len(prefixes) == 0 {
		return nil
	}
	keys := make([]*ndkgo.RouteKey, 0, len(prefixes))
	for _, cidr := range prefixes {
		pfx, err := prefixToNDK(cidr)
		if err != nil {
			return err
		}
		keys = append(keys, &ndkgo.RouteKey{
			NetworkInstanceName: netInst,
			IpPrefix:            pfx,
		})
	}
	resp, err := c.routeSvc.RouteDelete(c.withMD(ctx), &ndkgo.RouteDeleteRequest{
		Routes: keys,
	})
	if err != nil {
		return fmt.Errorf("route delete: %w", err)
	}
	if resp.GetStatus() != ndkgo.SdkMgrStatus_SDK_MGR_STATUS_SUCCESS {
		return fmt.Errorf("route delete failed: %s", resp.GetErrorStr())
	}
	return nil
}

// RouteEntry is a route to be programmed via NDK.
type RouteEntry struct {
	Prefix  string
	NHGName string
	Metric  uint32
}
