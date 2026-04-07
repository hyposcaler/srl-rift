package ndk

import (
	"context"
	"fmt"
	"log/slog"

	ndkgo "github.com/nokia/srlinux-ndk-go/ndk"
)

// InterfaceEvent represents an interface state change from NDK.
type InterfaceEvent struct {
	Name    string // SRL interface name, e.g. "ethernet-1/1"
	AdminUp bool
	OperUp  bool
	MTU     uint32
}

// ConfigEvent represents a config change from NDK.
type ConfigEvent struct {
	JSPath       string   // e.g. ".rift" or ".rift.interface"
	PathWithKeys string   // e.g. ".rift.interface{.name==\"ethernet-1/1\"}"
	Keys         []string // e.g. ["ethernet-1/1"]
	Data         string   // JSON content
}

// NotificationHandler dispatches NDK notifications to typed channels.
type NotificationHandler struct {
	InterfaceCh chan InterfaceEvent
	ConfigCh    chan ConfigEvent
	logger      *slog.Logger
}

// NewNotificationHandler creates a new notification handler.
func NewNotificationHandler(logger *slog.Logger) *NotificationHandler {
	return &NotificationHandler{
		InterfaceCh: make(chan InterfaceEvent, 64),
		ConfigCh:    make(chan ConfigEvent, 16),
		logger:      logger,
	}
}

// Run reads the notification stream and dispatches to channels.
// Blocks until ctx is cancelled or the stream ends.
func (h *NotificationHandler) Run(ctx context.Context, stream ndkgo.SdkNotificationService_NotificationStreamClient) error {
	for {
		resp, err := stream.Recv()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("notification recv: %w", err)
		}

		for _, notif := range resp.GetNotifications() {
			h.handle(ctx, notif)
		}
	}
}

func (h *NotificationHandler) handle(ctx context.Context, notif *ndkgo.Notification) {
	if intfNotif := notif.GetInterface(); intfNotif != nil {
		key := intfNotif.GetKey()
		data := intfNotif.GetData()
		ev := InterfaceEvent{
			Name:    key.GetInterfaceName(),
			AdminUp: data.GetAdminIsUp() == 1,
			OperUp:  data.GetOperIsUp() == 1,
			MTU:     data.GetMtu(),
		}
		h.logger.Info("interface event",
			"op", intfNotif.GetOp().String(),
			"name", ev.Name,
			"admin_up", ev.AdminUp,
			"oper_up", ev.OperUp,
			"mtu", ev.MTU,
		)
		select {
		case h.InterfaceCh <- ev:
		case <-ctx.Done():
		}
		return
	}

	if cfgNotif := notif.GetConfig(); cfgNotif != nil {
		key := cfgNotif.GetKey()
		data := cfgNotif.GetData()
		jsonData := data.GetJson()
		ev := ConfigEvent{
			JSPath:       key.GetJsPath(),
			PathWithKeys: key.GetJsPathWithKeys(),
			Keys:         key.GetKeys(),
			Data:         jsonData,
		}
		h.logger.Info("config event",
			"op", cfgNotif.GetOp().String(),
			"path", ev.PathWithKeys,
		)
		select {
		case h.ConfigCh <- ev:
		case <-ctx.Done():
		}
		return
	}

	h.logger.Debug("unhandled notification",
		"sub_id", notif.GetSubscriptionId(),
		"type", fmt.Sprintf("%T", notif.GetSubscriptionTypes()),
	)
}
