// Package ndk wraps the SR Linux NDK gRPC API for the RIFT agent.
package ndk

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	ndkgo "github.com/nokia/srlinux-ndk-go/ndk"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

const (
	ndkSocket         = "unix:///opt/srlinux/var/run/sr_sdk_service_manager:50053"
	agentName         = "rift-srl"
	keepaliveInterval = 10 * time.Second
)

// Client wraps NDK gRPC connections.
type Client struct {
	conn      *grpc.ClientConn
	sdkMgr    ndkgo.SdkMgrServiceClient
	notifSvc  ndkgo.SdkNotificationServiceClient
	telemetry ndkgo.SdkMgrTelemetryServiceClient
	routeSvc  ndkgo.SdkMgrRouteServiceClient
	nhgSvc    ndkgo.SdkMgrNextHopGroupServiceClient
	md        metadata.MD
	streamID  uint64
	appID     uint32
	logger    *slog.Logger
}

// New creates and registers a new NDK client.
func New(ctx context.Context, logger *slog.Logger) (*Client, error) {
	conn, err := grpc.Dial(ndkSocket,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("grpc dial: %w", err)
	}
	logger.Info("gRPC connected", "state", conn.GetState().String())

	c := &Client{
		conn:      conn,
		sdkMgr:    ndkgo.NewSdkMgrServiceClient(conn),
		notifSvc:  ndkgo.NewSdkNotificationServiceClient(conn),
		telemetry: ndkgo.NewSdkMgrTelemetryServiceClient(conn),
		routeSvc:  ndkgo.NewSdkMgrRouteServiceClient(conn),
		nhgSvc:    ndkgo.NewSdkMgrNextHopGroupServiceClient(conn),
		md:        metadata.Pairs("agent_name", agentName),
		logger:    logger,
	}

	// Register the agent.
	regResp, err := c.sdkMgr.AgentRegister(c.withMD(ctx), &ndkgo.AgentRegistrationRequest{
		AgentLiveliness: uint32(keepaliveInterval.Seconds()) * 3,
	})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("agent register: %w", err)
	}
	if regResp.GetStatus() != ndkgo.SdkMgrStatus_SDK_MGR_STATUS_SUCCESS {
		conn.Close()
		return nil, fmt.Errorf("agent register failed: %s", regResp.GetErrorStr())
	}
	c.appID = regResp.GetAppId()
	logger.Info("agent registered", "app_id", c.appID)

	// Create notification stream (step 1: CREATE).
	createResp, err := c.sdkMgr.NotificationRegister(c.withMD(ctx), &ndkgo.NotificationRegisterRequest{
		Op: ndkgo.NotificationRegisterRequest_OPERATION_CREATE,
	})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("notification stream create: %w", err)
	}
	if createResp.GetStatus() != ndkgo.SdkMgrStatus_SDK_MGR_STATUS_SUCCESS {
		conn.Close()
		return nil, fmt.Errorf("notification stream create failed")
	}
	c.streamID = createResp.GetStreamId()

	return c, nil
}

// SubscribeInterfaces adds interface notification subscription (step 2).
func (c *Client) SubscribeInterfaces(ctx context.Context) error {
	resp, err := c.sdkMgr.NotificationRegister(c.withMD(ctx), &ndkgo.NotificationRegisterRequest{
		StreamId: c.streamID,
		Op:       ndkgo.NotificationRegisterRequest_OPERATION_ADD_SUBSCRIPTION,
		SubscriptionTypes: &ndkgo.NotificationRegisterRequest_Interface{
			Interface: &ndkgo.InterfaceSubscriptionRequest{},
		},
	})
	if err != nil {
		return fmt.Errorf("interface subscription: %w", err)
	}
	if resp.GetStatus() != ndkgo.SdkMgrStatus_SDK_MGR_STATUS_SUCCESS {
		return fmt.Errorf("interface subscription failed")
	}
	c.logger.Info("subscribed to interface notifications",
		"stream_id", c.streamID,
		"subscription_id", resp.GetSubscriptionId(),
	)
	return nil
}

// SubscribeConfig adds config notification subscription.
func (c *Client) SubscribeConfig(ctx context.Context) error {
	resp, err := c.sdkMgr.NotificationRegister(c.withMD(ctx), &ndkgo.NotificationRegisterRequest{
		StreamId: c.streamID,
		Op:       ndkgo.NotificationRegisterRequest_OPERATION_ADD_SUBSCRIPTION,
		SubscriptionTypes: &ndkgo.NotificationRegisterRequest_Config{
			Config: &ndkgo.ConfigSubscriptionRequest{},
		},
	})
	if err != nil {
		return fmt.Errorf("config subscription: %w", err)
	}
	if resp.GetStatus() != ndkgo.SdkMgrStatus_SDK_MGR_STATUS_SUCCESS {
		return fmt.Errorf("config subscription failed")
	}
	c.logger.Info("subscribed to config notifications",
		"subscription_id", resp.GetSubscriptionId(),
	)
	return nil
}

// NotificationStream returns the notification stream.
func (c *Client) NotificationStream(ctx context.Context) (ndkgo.SdkNotificationService_NotificationStreamClient, error) {
	stream, err := c.notifSvc.NotificationStream(c.withMD(ctx), &ndkgo.NotificationStreamRequest{
		StreamId: c.streamID,
	})
	if err != nil {
		return nil, fmt.Errorf("notification stream: %w", err)
	}
	return stream, nil
}

// KeepAlive sends a single keepalive RPC.
func (c *Client) KeepAlive(ctx context.Context) error {
	_, err := c.sdkMgr.KeepAlive(c.withMD(ctx), &ndkgo.KeepAliveRequest{})
	return err
}

// KeepaliveInterval returns the keepalive interval.
func (c *Client) KeepaliveInterval() time.Duration {
	return keepaliveInterval
}

// UpdateTelemetry pushes telemetry state to IDB.
func (c *Client) UpdateTelemetry(ctx context.Context, jsPath string, data any) error {
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal telemetry: %w", err)
	}

	_, err = c.telemetry.TelemetryAddOrUpdate(c.withMD(ctx), &ndkgo.TelemetryUpdateRequest{
		States: []*ndkgo.TelemetryInfo{
			{
				Key: &ndkgo.TelemetryKey{
					JsPath: jsPath,
				},
				Data: &ndkgo.TelemetryData{
					JsonContent: string(jsonBytes),
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("telemetry update: %w", err)
	}
	return nil
}

// Close cleans up NDK resources.
func (c *Client) Close() {
	if c.conn != nil {
		c.conn.Close()
	}
}

func (c *Client) withMD(ctx context.Context) context.Context {
	return metadata.NewOutgoingContext(ctx, c.md)
}
