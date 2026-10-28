package internal

import (
	"context"

	"github.com/robfig/cron/v3"
	"go.mau.fi/whatsmeow/types"

	"github.com/dimaskiddo/go-whatsapp-multidevice-rest/pkg/log"
	pkgWhatsApp "github.com/dimaskiddo/go-whatsapp-multidevice-rest/pkg/whatsapp"
)

func Routines(cron *cron.Cron) {
	log.Print(nil).Info("Running Routine Tasks")

	// 1. Auto-Reconnect Watchdog (every 1 minute)
	cron.AddFunc("@every 1m", func() {
		if len(pkgWhatsApp.WhatsAppClient) > 0 {
			for jid, client := range pkgWhatsApp.WhatsAppClient {
				if client != nil && client.Store != nil && client.Store.ID != nil {
					if !client.IsConnected() {
						log.Print(nil).Infof("Routine watchdog: reconnecting idle/dropped client %s", jid)
						_ = client.Connect()
					}
				}
			}
		}
	})

	// 2. Active Heartbeat & Presence Keep-Alive (every 2 minutes)
	// Keeps the WebSocket socket warm through NAT firewalls and prevents idle companion drops
	cron.AddFunc("@every 2m", func() {
		if len(pkgWhatsApp.WhatsAppClient) > 0 {
			for _, client := range pkgWhatsApp.WhatsAppClient {
				if client != nil && client.Store != nil && client.Store.ID != nil && client.IsConnected() {
					// Send lightweight presence available ping to keep session warm
					_ = client.SendPresence(context.Background(), types.PresenceAvailable)
				}
			}
		}
	})

	cron.Start()
}
