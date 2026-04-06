package agent

import (
	"context"
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

// Agent is the top-level RIFT NDK agent.
type Agent struct {
	logger   *slog.Logger
	conn     *grpc.ClientConn
	sdkMgr   ndkgo.SdkMgrServiceClient
	notifSvc ndkgo.SdkNotificationServiceClient
	streamID uint64
	appID    uint32
	md       metadata.MD
}

// New creates and registers a new RIFT agent with the NDK.
func New(ctx context.Context, logger *slog.Logger) (*Agent, error) {
	conn, err := grpc.Dial(ndkSocket,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("grpc dial: %w", err)
	}
	logger.Info("gRPC connected", "state", conn.GetState().String())

	a := &Agent{
		logger:   logger,
		conn:     conn,
		sdkMgr:   ndkgo.NewSdkMgrServiceClient(conn),
		notifSvc: ndkgo.NewSdkNotificationServiceClient(conn),
		md:       metadata.Pairs("agent_name", agentName),
	}

	// Register the agent.
	regResp, err := a.sdkMgr.AgentRegister(a.withMD(ctx), &ndkgo.AgentRegistrationRequest{
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
	a.appID = regResp.GetAppId()
	logger.Info("agent registered", "app_id", a.appID)

	// Create notification stream, then add interface subscription.
	// Two-step approach required: CREATE (no type) then ADD_SUBSCRIPTION.
	createResp, err := a.sdkMgr.NotificationRegister(a.withMD(ctx), &ndkgo.NotificationRegisterRequest{
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
	a.streamID = createResp.GetStreamId()

	addResp, err := a.sdkMgr.NotificationRegister(a.withMD(ctx), &ndkgo.NotificationRegisterRequest{
		StreamId: a.streamID,
		Op:       ndkgo.NotificationRegisterRequest_OPERATION_ADD_SUBSCRIPTION,
		SubscriptionTypes: &ndkgo.NotificationRegisterRequest_Interface{
			Interface: &ndkgo.InterfaceSubscriptionRequest{},
		},
	})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("interface subscription: %w", err)
	}
	if addResp.GetStatus() != ndkgo.SdkMgrStatus_SDK_MGR_STATUS_SUCCESS {
		conn.Close()
		return nil, fmt.Errorf("interface subscription failed")
	}
	logger.Info("subscribed to interface notifications",
		"stream_id", a.streamID,
		"subscription_id", addResp.GetSubscriptionId(),
	)

	return a, nil
}

// Run starts the agent's main loop. It blocks until ctx is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	stream, err := a.notifSvc.NotificationStream(a.withMD(ctx), &ndkgo.NotificationStreamRequest{
		StreamId: a.streamID,
	})
	if err != nil {
		return fmt.Errorf("notification stream: %w", err)
	}

	go a.keepAlive(ctx)

	a.logger.Info("agent running, waiting for notifications")

	for {
		resp, err := stream.Recv()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("notification recv: %w", err)
		}

		for _, notif := range resp.GetNotifications() {
			a.handleNotification(notif)
		}
	}
}

func (a *Agent) handleNotification(notif *ndkgo.Notification) {
	if intfNotif := notif.GetInterface(); intfNotif != nil {
		key := intfNotif.GetKey()
		data := intfNotif.GetData()
		a.logger.Info("interface event",
			"op", intfNotif.GetOp().String(),
			"name", key.GetInterfaceName(),
			"admin_up", data.GetAdminIsUp() == 1,
			"oper_up", data.GetOperIsUp() == 1,
			"mtu", data.GetMtu(),
		)
		return
	}
	if cfgNotif := notif.GetConfig(); cfgNotif != nil {
		a.logger.Info("config event",
			"op", cfgNotif.GetOp().String(),
			"path", cfgNotif.GetKey().GetJsPath(),
		)
		return
	}
	a.logger.Info("notification",
		"sub_id", notif.GetSubscriptionId(),
		"type", fmt.Sprintf("%T", notif.GetSubscriptionTypes()),
	)
}

func (a *Agent) keepAlive(ctx context.Context) {
	ticker := time.NewTicker(keepaliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, err := a.sdkMgr.KeepAlive(a.withMD(ctx), &ndkgo.KeepAliveRequest{})
			if err != nil {
				a.logger.Error("keepalive failed", "error", err)
			}
		}
	}
}

func (a *Agent) withMD(ctx context.Context) context.Context {
	return metadata.NewOutgoingContext(ctx, a.md)
}

// Close cleans up agent resources.
func (a *Agent) Close() {
	if a.conn != nil {
		a.conn.Close()
	}
}
