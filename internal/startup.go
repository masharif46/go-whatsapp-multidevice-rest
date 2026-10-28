package internal

import (
	"context"

	"github.com/dimaskiddo/go-whatsapp-multidevice-rest/pkg/log"
	pkgWhatsApp "github.com/dimaskiddo/go-whatsapp-multidevice-rest/pkg/whatsapp"
)

func Startup() {
	log.Print(nil).Info("Running Startup Tasks")

	// Load All WhatsApp Client Devices from Datastore
	devices, err := pkgWhatsApp.WhatsAppDatastore.GetAllDevices(context.Background())
	if err != nil {
		log.Print(nil).Error("Failed to Load WhatsApp Client Devices from Datastore")
	}

	// Do Reconnect for Every Device in Datastore
	for _, device := range devices {
		// Get JID from Datastore
		jid := pkgWhatsApp.WhatsAppDecomposeJID(device.ID.User)

		// Mask JID for Logging Information
		maskJID := jid[0:len(jid)-4] + "xxxx"

		// Print Restore Log
		log.Print(nil).Info("Restoring WhatsApp Client for " + maskJID)

		// Initialize WhatsApp Client
		pkgWhatsApp.WhatsAppInitClient(device, jid)

		// Reconnect WhatsApp Client WebSocket
		err = pkgWhatsApp.WhatsAppReconnect(jid)
		if err != nil {
			log.Print(nil).Error(err.Error())
		}
	}

	// Map configured session aliases to devices
	mappings := pkgWhatsApp.LoadSessionMappings()
	for sessionName, phone := range mappings {
		if cli, ok := pkgWhatsApp.WhatsAppClient[phone]; ok && cli != nil {
			log.Print(nil).Infof("Mapped session alias %s -> %s", sessionName, phone)
			pkgWhatsApp.WhatsAppClient[sessionName] = cli
		}
	}

	// Ensure 'default' alias is mapped to the primary client
	if pkgWhatsApp.WhatsAppClient["default"] == nil {
		for _, cli := range pkgWhatsApp.WhatsAppClient {
			if cli != nil && cli.Store != nil && cli.Store.ID != nil {
				pkgWhatsApp.WhatsAppClient["default"] = cli
				log.Print(nil).Infof("Mapped fallback session alias 'default' -> %s", cli.Store.ID.User)
				break
			}
		}
	}
}
