package internal

import (
	"github.com/robfig/cron/v3"

	"github.com/dimaskiddo/go-whatsapp-multidevice-rest/pkg/log"
	pkgWhatsApp "github.com/dimaskiddo/go-whatsapp-multidevice-rest/pkg/whatsapp"
)

func Routines(cron *cron.Cron) {
	log.Print(nil).Info("Running Routine Tasks")

	cron.AddFunc("@every 1m", func() {
		// Keep alive and reconnect any paired clients if disconnected
		if len(pkgWhatsApp.WhatsAppClient) > 0 {
			for jid, client := range pkgWhatsApp.WhatsAppClient {
				if client != nil && client.Store.ID != nil {
					if !client.IsConnected() {
						log.Print(nil).Infof("Routine checking: reconnecting client %s", jid)
						_ = client.Connect()
					}
				}
			}
		}
	})

	cron.Start()
}
