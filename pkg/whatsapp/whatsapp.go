package whatsapp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/forPelevin/gomoji"
	webp "github.com/nickalie/go-webpbin"
	"github.com/rivo/uniseg"
	"github.com/sunshineplan/imgconv"

	qrCode "github.com/skip2/go-qrcode"
	"google.golang.org/protobuf/proto"

	"go.mau.fi/whatsmeow"
	wabin "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/dimaskiddo/go-whatsapp-multidevice-rest/pkg/env"
	"github.com/dimaskiddo/go-whatsapp-multidevice-rest/pkg/log"
)

var WhatsAppDatastore *sqlstore.Container
var WhatsAppClient = make(map[string]*whatsmeow.Client)

var (
	WhatsAppClientProxyURL string
)

func init() {
	var err error

	dbType, err := env.GetEnvString("WHATSAPP_DATASTORE_TYPE")
	if err != nil {
		log.Print(nil).Fatal("Error Parse Environment Variable for WhatsApp Client Datastore Type")
	}

	dbURI, err := env.GetEnvString("WHATSAPP_DATASTORE_URI")
	if err != nil {
		log.Print(nil).Fatal("Error Parse Environment Variable for WhatsApp Client Datastore URI")
	}

	datastore, err := sqlstore.New(context.Background(), dbType, dbURI, nil)
	if err != nil {
		log.Print(nil).Fatal("Error Connect WhatsApp Client Datastore")
	}

	WhatsAppClientProxyURL, _ = env.GetEnvString("WHATSAPP_CLIENT_PROXY_URL")

	WhatsAppDatastore = datastore
}

func WhatsAppInitClient(device *store.Device, jid string) {
	var err error
	wabin.IndentXML = true

	// If existing client in map has a deleted or invalid store, clean it up
	if cli, exists := WhatsAppClient[jid]; exists && cli != nil {
		if cli.Store == nil || cli.Store.Deleted {
			cli.Disconnect()
			WhatsAppClient[jid] = nil
			delete(WhatsAppClient, jid)
		}
	}

	if WhatsAppClient[jid] == nil {
		if device == nil || device.Deleted {
			// Initialize New WhatsApp Client Device in Datastore
			device = WhatsAppDatastore.NewDevice()
		}

		// Set Operating System and Platform info consistent with WhatsApp Web Companion
		store.SetOSInfo("macOS", [3]uint32{14, 5, 0})
		store.DeviceProps.PlatformType = waCompanionReg.DeviceProps_CHROME.Enum()
		store.DeviceProps.RequireFullSync = proto.Bool(false)

		// Set Client Versions if customized in environment
		version.Major, err = env.GetEnvInt("WHATSAPP_VERSION_MAJOR")
		if err == nil {
			store.DeviceProps.Version.Primary = proto.Uint32(uint32(version.Major))
		}
		version.Minor, err = env.GetEnvInt("WHATSAPP_VERSION_MINOR")
		if err == nil {
			store.DeviceProps.Version.Secondary = proto.Uint32(uint32(version.Minor))
		}
		version.Patch, err = env.GetEnvInt("WHATSAPP_VERSION_PATCH")
		if err == nil {
			store.DeviceProps.Version.Tertiary = proto.Uint32(uint32(version.Patch))
		}

		// Initialize New WhatsApp Client with verbose logging
		waLogger := waLog.Stdout("Whatsmeow", "DEBUG", true)
		WhatsAppClient[jid] = whatsmeow.NewClient(device, waLogger)

		// Register Event Handlers to capture pairing and connection state
		targetJID := jid
		WhatsAppClient[jid].AddEventHandler(func(rawEvt any) {
			switch evt := rawEvt.(type) {
			case *events.PairSuccess:
				userJID := WhatsAppDecomposeJID(evt.ID.User)
				log.Print(nil).Infof("Device pairing SUCCESS for %s! Primary phone user JID: %s", targetJID, userJID)
				WhatsAppClient[userJID] = WhatsAppClient[targetJID]
				_ = WhatsAppClient[targetJID].Store.Save(context.Background())
				triggerWebhookEvent("pair_success", targetJID, map[string]interface{}{"phone": userJID})
			case *events.Connected:
				log.Print(nil).Infof("WhatsApp connected for %s", targetJID)
				triggerWebhookEvent("connected", targetJID, nil)
			case *events.Disconnected:
				log.Print(nil).Warnf("WhatsApp disconnected for %s", targetJID)
				triggerWebhookEvent("disconnected", targetJID, nil)
			case *events.LoggedOut:
				log.Print(nil).Warnf("WhatsApp logged out for %s (reason: %v)", targetJID, evt.Reason)
				triggerWebhookEvent("logged_out", targetJID, map[string]interface{}{"reason": fmt.Sprintf("%v", evt.Reason)})
				if cliRef, ok := WhatsAppClient[targetJID]; ok && cliRef != nil {
					cliRef.Disconnect()
					if cliRef.Store != nil && !cliRef.Store.Deleted {
						_ = cliRef.Store.Delete(context.Background())
					}
					delete(WhatsAppClient, targetJID)
				}
				liveQRMutex.Lock()
				delete(liveQRMap, targetJID)
				liveQRMutex.Unlock()
			}
		})

		// Set WhatsApp Client Proxy Address if Proxy URL is Provided
		if len(WhatsAppClientProxyURL) > 0 {
			WhatsAppClient[jid].SetProxyAddress(WhatsAppClientProxyURL)
		}

		// Set WhatsApp Client Auto Reconnect
		WhatsAppClient[jid].EnableAutoReconnect = true

		// Set WhatsApp Client Auto Trust Identity
		WhatsAppClient[jid].AutoTrustIdentity = true
	}
}

func triggerWebhookEvent(event string, session string, payload map[string]interface{}) {
	webhookURL, _ := env.GetEnvString("WHATSAPP_WEBHOOK_URL")
	if len(webhookURL) == 0 {
		webhookURL = "http://127.0.0.1:8005/api/v1/webhooks/whatsapp/baileys"
	}
	secret, _ := env.GetEnvString("AUTH_BASIC_PASSWORD")
	if len(secret) == 0 {
		secret = "ThisIsPassword"
	}

	go func() {
		data := map[string]interface{}{
			"event":     event,
			"session":   session,
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}
		for k, v := range payload {
			data[k] = v
		}
		jsonBytes, err := json.Marshal(data)
		if err != nil {
			return
		}

		client := &http.Client{Timeout: 5 * time.Second}
		req, err := http.NewRequest("POST", webhookURL, bytes.NewBuffer(jsonBytes))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Api-Key", secret)
		req.Header.Set("Authorization", "Bearer "+secret)
		req.Header.Set("User-Agent", "Go-WhatsApp-Rest/1.0")

		resp, err := client.Do(req)
		if err == nil && resp != nil {
			_ = resp.Body.Close()
		}
	}()
}

func WhatsAppGetUserAgent(agentType string) waCompanionReg.DeviceProps_PlatformType {
	switch strings.ToLower(agentType) {
	case "desktop":
		return waCompanionReg.DeviceProps_DESKTOP
	case "mac":
		return waCompanionReg.DeviceProps_CATALINA
	case "android":
		return waCompanionReg.DeviceProps_ANDROID_AMBIGUOUS
	case "android-phone":
		return waCompanionReg.DeviceProps_ANDROID_PHONE
	case "andorid-tablet":
		return waCompanionReg.DeviceProps_ANDROID_TABLET
	case "ios-phone":
		return waCompanionReg.DeviceProps_IOS_PHONE
	case "ios-catalyst":
		return waCompanionReg.DeviceProps_IOS_CATALYST
	case "ipad":
		return waCompanionReg.DeviceProps_IPAD
	case "wearos":
		return waCompanionReg.DeviceProps_WEAR_OS
	case "ie":
		return waCompanionReg.DeviceProps_IE
	case "edge":
		return waCompanionReg.DeviceProps_EDGE
	case "chrome":
		return waCompanionReg.DeviceProps_CHROME
	case "safari":
		return waCompanionReg.DeviceProps_SAFARI
	case "firefox":
		return waCompanionReg.DeviceProps_FIREFOX
	case "opera":
		return waCompanionReg.DeviceProps_OPERA
	case "uwp":
		return waCompanionReg.DeviceProps_UWP
	case "aloha":
		return waCompanionReg.DeviceProps_ALOHA
	case "tv-tcl":
		return waCompanionReg.DeviceProps_TCL_TV
	default:
		return waCompanionReg.DeviceProps_UNKNOWN
	}
}

func WhatsAppGetUserOS() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows"
	case "darwin":
		return "macOS"
	default:
		return "Linux"
	}
}

type LiveQRData struct {
	sync.RWMutex
	Code      string
	Image     string
	ExpiresAt time.Time
}

var liveQRMap = make(map[string]*LiveQRData)
var liveQRMutex sync.Mutex

func GetLiveQRState(jid string) *LiveQRData {
	liveQRMutex.Lock()
	defer liveQRMutex.Unlock()
	state, exists := liveQRMap[jid]
	if !exists {
		state = &LiveQRData{}
		liveQRMap[jid] = state
	}
	return state
}

func WhatsAppGenerateQR(qrChan <-chan whatsmeow.QRChannelItem) (string, int) {
	qrChanCode := make(chan string, 1)
	qrChanTimeout := make(chan int, 1)

	// Get QR Code Data and Timeout
	go func() {
		for evt := range qrChan {
			if evt.Event == "code" {
				qrChanCode <- evt.Code
				qrChanTimeout <- int(evt.Timeout.Seconds())
				return
			}
		}
	}()

	select {
	case qrTemp := <-qrChanCode:
		timeout := <-qrChanTimeout
		qrPNG, _ := qrCode.Encode(qrTemp, qrCode.Medium, 256)
		return base64.StdEncoding.EncodeToString(qrPNG), timeout
	case <-time.After(8 * time.Second):
		return "", 0
	}
}

func WhatsAppLogin(jid string) (string, int, error) {
	if WhatsAppClient[jid] == nil || WhatsAppClient[jid].Store == nil || WhatsAppClient[jid].Store.Deleted {
		WhatsAppInitClient(nil, jid)
	}

	if WhatsAppClient[jid] == nil {
		return "", 0, errors.New("WhatsApp Client is not Valid")
	}

	cli := WhatsAppClient[jid]

	// If already authenticated and has Store.ID and is actively connected
	if cli.Store != nil && !cli.Store.Deleted && cli.Store.ID != nil {
		if !cli.IsConnected() {
			_ = WhatsAppReconnect(jid)
		}
		if cli.IsConnected() {
			return "WhatsApp Client is Reconnected", 0, nil
		}
	}

	state := GetLiveQRState(jid)
	state.RLock()
	hasValidQR := cli.IsConnected() && len(state.Image) > 0 && time.Now().Before(state.ExpiresAt)
	currentImage := state.Image
	remainingTimeout := int(time.Until(state.ExpiresAt).Seconds())
	state.RUnlock()

	// If currently connected and has a valid QR with >3s remaining, return it immediately without disconnecting!
	if hasValidQR && remainingTimeout > 3 {
		return currentImage, remainingTimeout, nil
	}

	// Disconnect if previously connected before getting a fresh QR channel
	cli.Disconnect()

	qrChanGenerate, err := cli.GetQRChannel(context.Background())
	if err != nil {
		return "", 0, err
	}

	err = cli.Connect()
	if err != nil {
		return "", 0, err
	}

	codeReceived := make(chan struct{}, 1)

	// Keep listening to the QR channel until closed or paired
	go func(targetJid string, ch <-chan whatsmeow.QRChannelItem) {
		firstCode := true
		for evt := range ch {
			switch evt.Event {
			case "code":
				qrPNG, encErr := qrCode.Encode(evt.Code, qrCode.Medium, 256)
				if encErr == nil {
					st := GetLiveQRState(targetJid)
					st.Lock()
					st.Code = evt.Code
					st.Image = "data:image/png;base64," + base64.StdEncoding.EncodeToString(qrPNG)
					st.ExpiresAt = time.Now().Add(evt.Timeout)
					st.Unlock()
					log.Print(nil).Infof("New QR code generated for %s (timeout: %v)", targetJid, evt.Timeout)
				}
				if firstCode {
					firstCode = false
					select {
					case codeReceived <- struct{}{}:
					default:
					}
				}
			case "success":
				log.Print(nil).Infof("WhatsApp successfully paired for session %s!", targetJid)
				st := GetLiveQRState(targetJid)
				st.Lock()
				st.Image = ""
				st.ExpiresAt = time.Time{}
				st.Unlock()
			case "timeout":
				log.Print(nil).Warnf("WhatsApp QR channel timeout for %s", targetJid)
			default:
				log.Print(nil).Infof("WhatsApp QR channel event: %s for %s", evt.Event, targetJid)
			}
		}
	}(jid, qrChanGenerate)

	select {
	case <-codeReceived:
		state.RLock()
		img := state.Image
		timeout := int(time.Until(state.ExpiresAt).Seconds())
		state.RUnlock()
		return img, timeout, nil
	case <-time.After(10 * time.Second):
		return "", 0, errors.New("timeout waiting for QR code from WhatsApp server")
	}
}

func WhatsAppLoginPairPhone(jid string, phone string) (string, int, error) {
	if WhatsAppClient[jid] == nil {
		return "", 0, errors.New("WhatsApp Client is not Valid")
	}

	cli := WhatsAppClient[jid]
	cli.Disconnect()

	if cli.Store.ID == nil {
		err := cli.Connect()
		if err != nil {
			return "", 0, err
		}

		code, err := cli.PairPhone(context.Background(), phone, true, whatsmeow.PairClientChrome, "Chrome (macOS)")
		if err != nil {
			return "", 0, err
		}

		return code, 160, nil
	} else {
		err := WhatsAppReconnect(jid)
		if err != nil {
			return "", 0, err
		}
		return "WhatsApp Client is Reconnected", 0, nil
	}
}

func WhatsAppLoginPair(jid string) (string, int, error) {
	if WhatsAppClient[jid] != nil {
		// Make Sure WebSocket Connection is Disconnected
		WhatsAppClient[jid].Disconnect()

		if WhatsAppClient[jid].Store.ID == nil {
			// Connect WebSocket while also Requesting Pairing Code
			err := WhatsAppClient[jid].Connect()
			if err != nil {
				return "", 0, err
			}

			// Request Pairing Code
			code, err := WhatsAppClient[jid].PairPhone(context.Background(), jid, true, whatsmeow.PairClientChrome, "Chrome ("+WhatsAppGetUserOS()+")")
			if err != nil {
				return "", 0, err
			}

			return code, 160, nil
		} else {
			// Device ID is Exist
			// Reconnect WebSocket
			err := WhatsAppReconnect(jid)
			if err != nil {
				return "", 0, err
			}

			return "WhatsApp Client is Reconnected", 0, nil
		}
	}

	// Return Error WhatsApp Client is not Valid
	return "", 0, errors.New("WhatsApp Client is not Valid")
}

func WhatsAppReconnect(jid string) error {
	if WhatsAppClient[jid] != nil {
		// Make Sure WebSocket Connection is Disconnected
		WhatsAppClient[jid].Disconnect()

		// Make Sure Store ID is not Empty
		// To do Reconnection
		if WhatsAppClient[jid] != nil {
			err := WhatsAppClient[jid].Connect()
			if err != nil {
				return err
			}

			return nil
		}

		return errors.New("WhatsApp Client Store ID is Empty, Please Re-Login and Scan QR Code Again")
	}

	return errors.New("WhatsApp Client is not Valid")
}

func WhatsAppLogout(jid string) error {
	if WhatsAppClient[jid] != nil {
		// Make Sure Store ID is not Empty
		if WhatsAppClient[jid] != nil {
			var err error

			// Set WhatsApp Client Presence to Unavailable
			WhatsAppPresence(jid, false)

			// Logout WhatsApp Client and Disconnect from WebSocket
			err = WhatsAppClient[jid].Logout(context.Background())
			if err != nil {
				// Force Disconnect
				WhatsAppClient[jid].Disconnect()

				// Manually Delete Device from Datastore Store
				err = WhatsAppClient[jid].Store.Delete(context.Background())
				if err != nil {
					return err
				}
			}

			// Free WhatsApp Client Map
			WhatsAppClient[jid] = nil
			delete(WhatsAppClient, jid)

			return nil
		}

		return errors.New("WhatsApp Client Store ID is Empty, Please Re-Login and Scan QR Code Again")
	}

	// Return Error WhatsApp Client is not Valid
	return errors.New("WhatsApp Client is not Valid")
}

func WhatsAppUnauthenticatedLogout(jid string) error {
	liveQRMutex.Lock()
	delete(liveQRMap, jid)
	liveQRMutex.Unlock()

	// Disconnect and clean target client
	if cli, ok := WhatsAppClient[jid]; ok && cli != nil {
		cli.Disconnect()
		if cli.Store != nil && !cli.Store.Deleted {
			_ = cli.Store.Delete(context.Background())
		}
	}
	delete(WhatsAppClient, jid)

	// Clean any other references sharing this client or with deleted stores
	for k, cli := range WhatsAppClient {
		if cli == nil || cli.Store == nil || cli.Store.Deleted {
			delete(WhatsAppClient, k)
		}
	}

	return nil
}

func WhatsAppResetSession(jid string) (string, int, error) {
	// 1. Force unauthenticated logout of current session
	_ = WhatsAppUnauthenticatedLogout(jid)

	// 2. Also delete any stale unlinked devices from SQLite datastore
	devices, err := WhatsAppDatastore.GetAllDevices(context.Background())
	if err == nil {
		for _, dev := range devices {
			if dev != nil && !dev.Deleted && (dev.ID == nil || dev.ID.User == jid) {
				_ = dev.Delete(context.Background())
			}
		}
	}

	// 3. Initialize fresh device for this session
	freshDevice := WhatsAppDatastore.NewDevice()
	WhatsAppInitClient(freshDevice, jid)

	// 4. Trigger fresh login & QR emission
	return WhatsAppLogin(jid)
}

func WhatsAppIsClientOK(jid string) error {
	// Make Sure WhatsApp Client is Connected
	if !WhatsAppClient[jid].IsConnected() {
		return errors.New("WhatsApp Client is not Connected")
	}

	// Make Sure WhatsApp Client is Logged In
	if !WhatsAppClient[jid].IsLoggedIn() {
		return errors.New("WhatsApp Client is not Logged In")
	}

	return nil
}

func WhatsAppGetJID(jid string, id string) types.JID {
	if WhatsAppClient[jid] != nil {
		var ids []string

		ids = append(ids, "+"+id)
		infos, err := WhatsAppClient[jid].IsOnWhatsApp(context.Background(), ids)
		if err == nil {
			// If WhatsApp ID is Registered Then
			// Return ID Information
			if infos[0].IsIn {
				return infos[0].JID
			}
		}
	}

	// Return Empty ID Information
	return types.EmptyJID
}

func WhatsAppCheckJID(jid string, id string) (types.JID, error) {
	if WhatsAppClient[jid] != nil {
		// Compose New Remote JID
		remoteJID := WhatsAppComposeJID(id)
		if remoteJID.Server != types.GroupServer {
			// Validate JID if Remote JID is not Group JID
			if WhatsAppGetJID(jid, remoteJID.String()).IsEmpty() {
				return types.EmptyJID, errors.New("WhatsApp Personal ID is Not Registered")
			}
		}

		// Return Remote ID Information
		return remoteJID, nil
	}

	// Return Empty ID Information
	return types.EmptyJID, nil
}

func WhatsAppComposeJID(id string) types.JID {
	// Decompose WhatsApp ID First Before Recomposing
	id = WhatsAppDecomposeJID(id)

	// Check if ID is Group or Not By Detecting '-' for Old Group ID
	// Or By ID Length That Should be 18 Digits or More
	if strings.ContainsRune(id, '-') || len(id) >= 18 {
		// Return New Group User JID
		return types.NewJID(id, types.GroupServer)
	}

	// Return New Standard User JID
	return types.NewJID(id, types.DefaultUserServer)
}

func WhatsAppDecomposeJID(id string) string {
	// Check if WhatsApp ID Contains '@' Symbol
	if strings.ContainsRune(id, '@') {
		// Split WhatsApp ID Based on '@' Symbol
		// and Get Only The First Section Before The Symbol
		buffers := strings.Split(id, "@")
		id = buffers[0]
	}

	// Check if WhatsApp ID First Character is '+' Symbol
	if id[0] == '+' {
		// Remove '+' Symbol from WhatsApp ID
		id = id[1:]
	}

	return id
}

func WhatsAppPresence(jid string, isAvailable bool) {
	if isAvailable {
		_ = WhatsAppClient[jid].SendPresence(context.Background(), types.PresenceAvailable)
	} else {
		_ = WhatsAppClient[jid].SendPresence(context.Background(), types.PresenceUnavailable)
	}
}

func WhatsAppComposeStatus(jid string, rjid types.JID, isComposing bool, isAudio bool) {
	// Set Compose Status
	var typeCompose types.ChatPresence
	if isComposing {
		typeCompose = types.ChatPresenceComposing
	} else {
		typeCompose = types.ChatPresencePaused
	}

	// Set Compose Media Audio (Recording) or Text (Typing)
	var typeComposeMedia types.ChatPresenceMedia
	if isAudio {
		typeComposeMedia = types.ChatPresenceMediaAudio
	} else {
		typeComposeMedia = types.ChatPresenceMediaText
	}

	// Send Chat Compose Status
	_ = WhatsAppClient[jid].SendChatPresence(context.Background(), rjid, typeCompose, typeComposeMedia)
}

func WhatsAppCheckRegistered(jid string, id string) error {
	if WhatsAppClient[jid] != nil {
		var err error

		// Make Sure WhatsApp Client is OK
		err = WhatsAppIsClientOK(jid)
		if err != nil {
			return err
		}

		// Make Sure WhatsApp ID is Registered
		remoteJID, err := WhatsAppCheckJID(jid, id)
		if err != nil {
			return err
		}

		// Make Sure WhatsApp ID is Not Empty or It is Not Group ID
		if remoteJID.IsEmpty() || remoteJID.Server == types.GroupServer {
			return errors.New("WhatsApp Personal ID is Not Registered")
		}

		return nil
	}

	// Return Error WhatsApp Client is not Valid
	return errors.New("WhatsApp Client is not Valid")
}

func WhatsAppSendText(jid string, rjid string, message string) (string, error) {
	if WhatsAppClient[jid] != nil {
		var err error

		// Make Sure WhatsApp Client is OK
		err = WhatsAppIsClientOK(jid)
		if err != nil {
			return "", err
		}

		// Make Sure WhatsApp ID is Registered
		remoteJID, err := WhatsAppCheckJID(jid, rjid)
		if err != nil {
			return "", err
		}

		// Set Chat Presence
		WhatsAppPresence(jid, true)
		WhatsAppComposeStatus(jid, remoteJID, true, false)
		defer func() {
			WhatsAppComposeStatus(jid, remoteJID, false, false)
			WhatsAppPresence(jid, false)
		}()

		// Compose WhatsApp Proto
		msgExtra := whatsmeow.SendRequestExtra{
			ID: WhatsAppClient[jid].GenerateMessageID(),
		}
		msgContent := &waE2E.Message{
			Conversation: proto.String(message),
		}

		// Send WhatsApp Message Proto
		_, err = WhatsAppClient[jid].SendMessage(context.Background(), remoteJID, msgContent, msgExtra)
		if err != nil {
			return "", err
		}

		return msgExtra.ID, nil
	}

	// Return Error WhatsApp Client is not Valid
	return "", errors.New("WhatsApp Client is not Valid")
}

func WhatsAppSendLocation(jid string, rjid string, latitude float64, longitude float64) (string, error) {
	if WhatsAppClient[jid] != nil {
		var err error

		// Make Sure WhatsApp Client is OK
		err = WhatsAppIsClientOK(jid)
		if err != nil {
			return "", err
		}

		// Make Sure WhatsApp ID is Registered
		remoteJID, err := WhatsAppCheckJID(jid, rjid)
		if err != nil {
			return "", err
		}

		// Set Chat Presence
		WhatsAppPresence(jid, true)
		WhatsAppComposeStatus(jid, remoteJID, true, false)
		defer func() {
			WhatsAppComposeStatus(jid, remoteJID, false, false)
			WhatsAppPresence(jid, false)
		}()

		// Compose WhatsApp Proto
		msgExtra := whatsmeow.SendRequestExtra{
			ID: WhatsAppClient[jid].GenerateMessageID(),
		}
		msgContent := &waE2E.Message{
			LocationMessage: &waE2E.LocationMessage{
				DegreesLatitude:  proto.Float64(latitude),
				DegreesLongitude: proto.Float64(longitude),
			},
		}

		// Send WhatsApp Message Proto
		_, err = WhatsAppClient[jid].SendMessage(context.Background(), remoteJID, msgContent, msgExtra)
		if err != nil {
			return "", err
		}

		return msgExtra.ID, nil
	}

	// Return Error WhatsApp Client is not Valid
	return "", errors.New("WhatsApp Client is not Valid")
}

func WhatsAppSendDocument(jid string, rjid string, fileBytes []byte, fileType string, fileName string, caption string) (string, error) {
	if WhatsAppClient[jid] != nil {
		var err error

		// Make Sure WhatsApp Client is OK
		err = WhatsAppIsClientOK(jid)
		if err != nil {
			return "", err
		}

		// Make Sure WhatsApp ID is Registered
		remoteJID, err := WhatsAppCheckJID(jid, rjid)
		if err != nil {
			return "", err
		}

		// Set Chat Presence
		WhatsAppPresence(jid, true)
		WhatsAppComposeStatus(jid, remoteJID, true, false)
		defer func() {
			WhatsAppComposeStatus(jid, remoteJID, false, false)
			WhatsAppPresence(jid, false)
		}()

		// Upload File to WhatsApp Storage Server
		fileUploaded, err := WhatsAppClient[jid].Upload(context.Background(), fileBytes, whatsmeow.MediaDocument)
		if err != nil {
			return "", errors.New("Error While Uploading Media to WhatsApp Server")
		}

		// Compose WhatsApp Proto
		msgExtra := whatsmeow.SendRequestExtra{
			ID: WhatsAppClient[jid].GenerateMessageID(),
		}
		msgContent := &waE2E.Message{
			DocumentMessage: &waE2E.DocumentMessage{
				URL:           proto.String(fileUploaded.URL),
				DirectPath:    proto.String(fileUploaded.DirectPath),
				Mimetype:      proto.String(fileType),
				Title:         proto.String(fileName),
				FileName:      proto.String(fileName),
				Caption:       proto.String(caption),
				FileLength:    proto.Uint64(fileUploaded.FileLength),
				FileSHA256:    fileUploaded.FileSHA256,
				FileEncSHA256: fileUploaded.FileEncSHA256,
				MediaKey:      fileUploaded.MediaKey,
			},
		}

		// Send WhatsApp Message Proto
		_, err = WhatsAppClient[jid].SendMessage(context.Background(), remoteJID, msgContent, msgExtra)
		if err != nil {
			return "", err
		}

		return msgExtra.ID, nil
	}

	// Return Error WhatsApp Client is not Valid
	return "", errors.New("WhatsApp Client is not Valid")
}

func WhatsAppSendImage(jid string, rjid string, imageBytes []byte, imageType string, imageCaption string, isViewOnce bool) (string, error) {
	if WhatsAppClient[jid] != nil {
		var err error

		// Make Sure WhatsApp Client is OK
		err = WhatsAppIsClientOK(jid)
		if err != nil {
			return "", err
		}

		// Make Sure WhatsApp ID is Registered
		remoteJID, err := WhatsAppCheckJID(jid, rjid)
		if err != nil {
			return "", err
		}

		// Set Chat Presence
		WhatsAppPresence(jid, true)
		WhatsAppComposeStatus(jid, remoteJID, true, false)
		defer func() {
			WhatsAppComposeStatus(jid, remoteJID, false, false)
			WhatsAppPresence(jid, false)
		}()

		// Issue #7 Old Version Client Cannot Render WebP Format
		// If MIME Type is "image/webp" Then Convert it as PNG
		isWhatsAppImageConvertWebP, err := env.GetEnvBool("WHATSAPP_MEDIA_IMAGE_CONVERT_WEBP")
		if err != nil {
			isWhatsAppImageConvertWebP = false
		}

		if imageType == "image/webp" && isWhatsAppImageConvertWebP {
			imgConvDecode, err := imgconv.Decode(bytes.NewReader(imageBytes))
			if err != nil {
				return "", errors.New("Error While Decoding Convert Image Stream")
			}

			imgConvEncode := new(bytes.Buffer)

			err = imgconv.Write(imgConvEncode, imgConvDecode, &imgconv.FormatOption{Format: imgconv.PNG})
			if err != nil {
				return "", errors.New("Error While Encoding Convert Image Stream")
			}

			imageBytes = imgConvEncode.Bytes()
			imageType = "image/png"
		}

		// If WhatsApp Media Compression Enabled
		// Then Resize The Image to Width 1024px and Preserve Aspect Ratio
		isWhatsAppImageCompression, err := env.GetEnvBool("WHATSAPP_MEDIA_IMAGE_COMPRESSION")
		if err != nil {
			isWhatsAppImageCompression = false
		}

		if isWhatsAppImageCompression {
			imgResizeDecode, err := imgconv.Decode(bytes.NewReader(imageBytes))
			if err != nil {
				return "", errors.New("Error While Decoding Resize Image Stream")
			}

			imgResizeEncode := new(bytes.Buffer)

			err = imgconv.Write(imgResizeEncode,
				imgconv.Resize(imgResizeDecode, &imgconv.ResizeOption{Width: 1024}),
				&imgconv.FormatOption{})

			if err != nil {
				return "", errors.New("Error While Encoding Resize Image Stream")
			}

			imageBytes = imgResizeEncode.Bytes()
		}

		// Creating Image JPEG Thumbnail
		// With Permanent Width 640px and Preserve Aspect Ratio
		imgThumbDecode, err := imgconv.Decode(bytes.NewReader(imageBytes))
		if err != nil {
			return "", errors.New("Error While Decoding Thumbnail Image Stream")
		}

		imgThumbEncode := new(bytes.Buffer)

		err = imgconv.Write(imgThumbEncode,
			imgconv.Resize(imgThumbDecode, &imgconv.ResizeOption{Width: 72}),
			&imgconv.FormatOption{Format: imgconv.JPEG})

		if err != nil {
			return "", errors.New("Error While Encoding Thumbnail Image Stream")
		}

		// Upload Image to WhatsApp Storage Server
		imageUploaded, err := WhatsAppClient[jid].Upload(context.Background(), imageBytes, whatsmeow.MediaImage)
		if err != nil {
			return "", errors.New("Error While Uploading Media to WhatsApp Server")
		}

		// Upload Image Thumbnail to WhatsApp Storage Server
		imageThumbUploaded, err := WhatsAppClient[jid].Upload(context.Background(), imgThumbEncode.Bytes(), whatsmeow.MediaLinkThumbnail)
		if err != nil {
			return "", errors.New("Error while Uploading Image Thumbnail to WhatsApp Server")
		}

		// Compose WhatsApp Proto
		msgExtra := whatsmeow.SendRequestExtra{
			ID: WhatsAppClient[jid].GenerateMessageID(),
		}
		msgContent := &waE2E.Message{
			ImageMessage: &waE2E.ImageMessage{
				URL:                 proto.String(imageUploaded.URL),
				DirectPath:          proto.String(imageUploaded.DirectPath),
				Mimetype:            proto.String(imageType),
				Caption:             proto.String(imageCaption),
				FileLength:          proto.Uint64(imageUploaded.FileLength),
				FileSHA256:          imageUploaded.FileSHA256,
				FileEncSHA256:       imageUploaded.FileEncSHA256,
				MediaKey:            imageUploaded.MediaKey,
				JPEGThumbnail:       imgThumbEncode.Bytes(),
				ThumbnailDirectPath: &imageThumbUploaded.DirectPath,
				ThumbnailSHA256:     imageThumbUploaded.FileSHA256,
				ThumbnailEncSHA256:  imageThumbUploaded.FileEncSHA256,
				ViewOnce:            proto.Bool(isViewOnce),
			},
		}

		// Send WhatsApp Message Proto
		_, err = WhatsAppClient[jid].SendMessage(context.Background(), remoteJID, msgContent, msgExtra)
		if err != nil {
			return "", err
		}

		return msgExtra.ID, nil
	}

	// Return Error WhatsApp Client is not Valid
	return "", errors.New("WhatsApp Client is not Valid")
}

func WhatsAppSendAudio(jid string, rjid string, audioBytes []byte, audioType string) (string, error) {
	if WhatsAppClient[jid] != nil {
		var err error

		// Make Sure WhatsApp Client is OK
		err = WhatsAppIsClientOK(jid)
		if err != nil {
			return "", err
		}

		// Make Sure WhatsApp ID is Registered
		remoteJID, err := WhatsAppCheckJID(jid, rjid)
		if err != nil {
			return "", err
		}

		// Set Chat Presence
		WhatsAppPresence(jid, true)
		WhatsAppComposeStatus(jid, remoteJID, true, true)
		defer func() {
			WhatsAppComposeStatus(jid, remoteJID, false, true)
			WhatsAppPresence(jid, false)
		}()

		// Upload Audio to WhatsApp Storage Server
		audioUploaded, err := WhatsAppClient[jid].Upload(context.Background(), audioBytes, whatsmeow.MediaAudio)
		if err != nil {
			return "", errors.New("Error While Uploading Media to WhatsApp Server")
		}

		// Compose WhatsApp Proto
		msgExtra := whatsmeow.SendRequestExtra{
			ID: WhatsAppClient[jid].GenerateMessageID(),
		}
		msgContent := &waE2E.Message{
			AudioMessage: &waE2E.AudioMessage{
				URL:           proto.String(audioUploaded.URL),
				DirectPath:    proto.String(audioUploaded.DirectPath),
				Mimetype:      proto.String(audioType),
				FileLength:    proto.Uint64(audioUploaded.FileLength),
				FileSHA256:    audioUploaded.FileSHA256,
				FileEncSHA256: audioUploaded.FileEncSHA256,
				MediaKey:      audioUploaded.MediaKey,
			},
		}

		// Send WhatsApp Message Proto
		_, err = WhatsAppClient[jid].SendMessage(context.Background(), remoteJID, msgContent, msgExtra)
		if err != nil {
			return "", err
		}

		return msgExtra.ID, nil
	}

	// Return Error WhatsApp Client is not Valid
	return "", errors.New("WhatsApp Client is not Valid")
}

func WhatsAppSendVideo(jid string, rjid string, videoBytes []byte, videoType string, videoCaption string, isViewOnce bool) (string, error) {
	if WhatsAppClient[jid] != nil {
		var err error

		// Make Sure WhatsApp Client is OK
		err = WhatsAppIsClientOK(jid)
		if err != nil {
			return "", err
		}

		// Make Sure WhatsApp ID is Registered
		remoteJID, err := WhatsAppCheckJID(jid, rjid)
		if err != nil {
			return "", err
		}

		// Set Chat Presence
		WhatsAppPresence(jid, true)
		WhatsAppComposeStatus(jid, remoteJID, true, false)
		defer func() {
			WhatsAppComposeStatus(jid, remoteJID, false, false)
			WhatsAppPresence(jid, false)
		}()

		// Upload Video to WhatsApp Storage Server
		videoUploaded, err := WhatsAppClient[jid].Upload(context.Background(), videoBytes, whatsmeow.MediaVideo)
		if err != nil {
			return "", errors.New("Error While Uploading Media to WhatsApp Server")
		}

		// Compose WhatsApp Proto
		msgExtra := whatsmeow.SendRequestExtra{
			ID: WhatsAppClient[jid].GenerateMessageID(),
		}
		msgContent := &waE2E.Message{
			VideoMessage: &waE2E.VideoMessage{
				URL:           proto.String(videoUploaded.URL),
				DirectPath:    proto.String(videoUploaded.DirectPath),
				Mimetype:      proto.String(videoType),
				Caption:       proto.String(videoCaption),
				FileLength:    proto.Uint64(videoUploaded.FileLength),
				FileSHA256:    videoUploaded.FileSHA256,
				FileEncSHA256: videoUploaded.FileEncSHA256,
				MediaKey:      videoUploaded.MediaKey,
				ViewOnce:      proto.Bool(isViewOnce),
			},
		}

		// Send WhatsApp Message Proto
		_, err = WhatsAppClient[jid].SendMessage(context.Background(), remoteJID, msgContent, msgExtra)
		if err != nil {
			return "", err
		}

		return msgExtra.ID, nil
	}

	// Return Error WhatsApp Client is not Valid
	return "", errors.New("WhatsApp Client is not Valid")
}

func WhatsAppSendContact(jid string, rjid string, contactName string, contactNumber string) (string, error) {
	if WhatsAppClient[jid] != nil {
		var err error

		// Make Sure WhatsApp Client is OK
		err = WhatsAppIsClientOK(jid)
		if err != nil {
			return "", err
		}

		// Make Sure WhatsApp ID is Registered
		remoteJID, err := WhatsAppCheckJID(jid, rjid)
		if err != nil {
			return "", err
		}

		// Set Chat Presence
		WhatsAppPresence(jid, true)
		WhatsAppComposeStatus(jid, remoteJID, true, false)
		defer func() {
			WhatsAppComposeStatus(jid, remoteJID, false, false)
			WhatsAppPresence(jid, false)
		}()

		// Compose WhatsApp Proto
		msgExtra := whatsmeow.SendRequestExtra{
			ID: WhatsAppClient[jid].GenerateMessageID(),
		}
		msgVCard := fmt.Sprintf("BEGIN:VCARD\nVERSION:3.0\nN:;%v;;;\nFN:%v\nTEL;type=CELL;waid=%v:+%v\nEND:VCARD",
			contactName, contactName, contactNumber, contactNumber)
		msgContent := &waE2E.Message{
			ContactMessage: &waE2E.ContactMessage{
				DisplayName: proto.String(contactName),
				Vcard:       proto.String(msgVCard),
			},
		}

		// Send WhatsApp Message Proto
		_, err = WhatsAppClient[jid].SendMessage(context.Background(), remoteJID, msgContent, msgExtra)
		if err != nil {
			return "", err
		}

		return msgExtra.ID, nil
	}

	// Return Error WhatsApp Client is not Valid
	return "", errors.New("WhatsApp Client is not Valid")
}

func WhatsAppSendLink(jid string, rjid string, linkCaption string, linkURL string) (string, error) {
	if WhatsAppClient[jid] != nil {
		var err error
		var urlTitle, urlDescription string

		// Make Sure WhatsApp Client is OK
		err = WhatsAppIsClientOK(jid)
		if err != nil {
			return "", err
		}

		// Make Sure WhatsApp ID is Registered
		remoteJID, err := WhatsAppCheckJID(jid, rjid)
		if err != nil {
			return "", err
		}

		// Set Chat Presence
		WhatsAppPresence(jid, true)
		WhatsAppComposeStatus(jid, remoteJID, true, false)
		defer func() {
			WhatsAppComposeStatus(jid, remoteJID, false, false)
			WhatsAppPresence(jid, false)
		}()

		// Get URL Metadata
		urlResponse, err := http.Get(linkURL)
		if err != nil {
			return "", err
		}
		defer urlResponse.Body.Close()

		if urlResponse.StatusCode != 200 {
			return "", errors.New("Error While Fetching URL Metadata!")
		}

		// Query URL Metadata
		docData, err := goquery.NewDocumentFromReader(urlResponse.Body)
		if err != nil {
			return "", err
		}

		docData.Find("title").Each(func(index int, element *goquery.Selection) {
			urlTitle = element.Text()
		})

		docData.Find("meta[name='description']").Each(func(index int, element *goquery.Selection) {
			urlDescription, _ = element.Attr("content")
		})

		// Compose WhatsApp Proto
		msgExtra := whatsmeow.SendRequestExtra{
			ID: WhatsAppClient[jid].GenerateMessageID(),
		}
		msgText := linkURL

		if len(strings.TrimSpace(linkCaption)) > 0 {
			msgText = fmt.Sprintf("%s\n🔗 %s", linkCaption, linkURL)
		}

		msgContent := &waE2E.Message{
			ExtendedTextMessage: &waE2E.ExtendedTextMessage{
				Text:        proto.String(msgText),
				Title:       proto.String(urlTitle),
				MatchedText: proto.String(linkURL),
				Description: proto.String(urlDescription),
			},
		}

		// Send WhatsApp Message Proto
		_, err = WhatsAppClient[jid].SendMessage(context.Background(), remoteJID, msgContent, msgExtra)
		if err != nil {
			return "", err
		}

		return msgExtra.ID, nil
	}

	// Return Error WhatsApp Client is not Valid
	return "", errors.New("WhatsApp Client is not Valid")
}

func WhatsAppSendSticker(jid string, rjid string, stickerBytes []byte) (string, error) {
	if WhatsAppClient[jid] != nil {
		var err error

		// Make Sure WhatsApp Client is OK
		err = WhatsAppIsClientOK(jid)
		if err != nil {
			return "", err
		}

		// Make Sure WhatsApp ID is Registered
		remoteJID, err := WhatsAppCheckJID(jid, rjid)
		if err != nil {
			return "", err
		}

		// Set Chat Presence
		WhatsAppPresence(jid, true)
		WhatsAppComposeStatus(jid, remoteJID, true, false)
		defer func() {
			WhatsAppComposeStatus(jid, remoteJID, false, false)
			WhatsAppPresence(jid, false)
		}()

		stickerConvDecode, err := imgconv.Decode(bytes.NewReader(stickerBytes))
		if err != nil {
			return "", errors.New("Error While Decoding Convert Sticker Stream")
		}

		stickerConvResize := imgconv.Resize(stickerConvDecode, &imgconv.ResizeOption{Width: 512, Height: 512})
		stickerConvEncode := new(bytes.Buffer)

		err = webp.Encode(stickerConvEncode, stickerConvResize)
		if err != nil {
			return "", errors.New("Error While Encoding Convert Sticker Stream")
		}

		stickerBytes = stickerConvEncode.Bytes()

		// Upload Image to WhatsApp Storage Server
		stickerUploaded, err := WhatsAppClient[jid].Upload(context.Background(), stickerBytes, whatsmeow.MediaImage)
		if err != nil {
			return "", errors.New("Error While Uploading Media to WhatsApp Server")
		}

		// Compose WhatsApp Proto
		msgExtra := whatsmeow.SendRequestExtra{
			ID: WhatsAppClient[jid].GenerateMessageID(),
		}
		msgContent := &waE2E.Message{
			StickerMessage: &waE2E.StickerMessage{
				URL:           proto.String(stickerUploaded.URL),
				DirectPath:    proto.String(stickerUploaded.DirectPath),
				Mimetype:      proto.String("image/webp"),
				FileLength:    proto.Uint64(stickerUploaded.FileLength),
				FileSHA256:    stickerUploaded.FileSHA256,
				FileEncSHA256: stickerUploaded.FileEncSHA256,
				MediaKey:      stickerUploaded.MediaKey,
			},
		}

		// Send WhatsApp Message Proto
		_, err = WhatsAppClient[jid].SendMessage(context.Background(), remoteJID, msgContent, msgExtra)
		if err != nil {
			return "", err
		}

		return msgExtra.ID, nil
	}

	// Return Error WhatsApp Client is not Valid
	return "", errors.New("WhatsApp Client is not Valid")
}

func WhatsAppSendPoll(jid string, rjid string, question string, options []string, isMultiAnswer bool) (string, error) {
	if WhatsAppClient[jid] != nil {
		var err error

		// Make Sure WhatsApp Client is OK
		err = WhatsAppIsClientOK(jid)
		if err != nil {
			return "", err
		}

		// Make Sure WhatsApp ID is Registered
		remoteJID, err := WhatsAppCheckJID(jid, rjid)
		if err != nil {
			return "", err
		}

		// Set Chat Presence
		WhatsAppPresence(jid, true)
		WhatsAppComposeStatus(jid, remoteJID, true, false)
		defer func() {
			WhatsAppComposeStatus(jid, remoteJID, false, false)
			WhatsAppPresence(jid, false)
		}()

		// Check Options Must Be Equal or Greater Than 2
		if len(options) < 2 {
			return "", errors.New("WhatsApp Poll Options / Choices Must Be Equal or Greater Than 2")
		}

		// Check if Poll Allow Multiple Answer
		pollAnswerMax := 1
		if isMultiAnswer {
			pollAnswerMax = len(options)
		}

		// Compose WhatsApp Proto
		msgExtra := whatsmeow.SendRequestExtra{
			ID: WhatsAppClient[jid].GenerateMessageID(),
		}

		// Send WhatsApp Message Proto
		_, err = WhatsAppClient[jid].SendMessage(context.Background(), remoteJID, WhatsAppClient[jid].BuildPollCreation(question, options, pollAnswerMax), msgExtra)
		if err != nil {
			return "", err
		}

		return msgExtra.ID, nil
	}

	// Return Error WhatsApp Client is not Valid
	return "", errors.New("WhatsApp Client is not Valid")
}

func WhatsAppMessageEdit(jid string, rjid string, msgid string, message string) (string, error) {
	if WhatsAppClient[jid] != nil {
		var err error

		// Make Sure WhatsApp Client is OK
		err = WhatsAppIsClientOK(jid)
		if err != nil {
			return "", err
		}

		// Make Sure WhatsApp ID is Registered
		remoteJID, err := WhatsAppCheckJID(jid, rjid)
		if err != nil {
			return "", err
		}

		// Set Chat Presence
		WhatsAppPresence(jid, true)
		WhatsAppComposeStatus(jid, remoteJID, true, false)
		defer func() {
			WhatsAppComposeStatus(jid, remoteJID, false, false)
			WhatsAppPresence(jid, false)
		}()

		// Compose WhatsApp Proto
		msgContent := &waE2E.Message{
			Conversation: proto.String(message),
		}

		// Send WhatsApp Message Proto in Edit Mode
		_, err = WhatsAppClient[jid].SendMessage(context.Background(), remoteJID, WhatsAppClient[jid].BuildEdit(remoteJID, msgid, msgContent))
		if err != nil {
			return "", err
		}

		return msgid, nil
	}

	// Return Error WhatsApp Client is not Valid
	return "", errors.New("WhatsApp Client is not Valid")
}

func WhatsAppMessageReact(jid string, rjid string, msgid string, emoji string) (string, error) {
	if WhatsAppClient[jid] != nil {
		var err error

		// Make Sure WhatsApp Client is OK
		err = WhatsAppIsClientOK(jid)
		if err != nil {
			return "", err
		}

		// Make Sure WhatsApp ID is Registered
		remoteJID, err := WhatsAppCheckJID(jid, rjid)
		if err != nil {
			return "", err
		}

		// Set Chat Presence
		WhatsAppPresence(jid, true)
		WhatsAppComposeStatus(jid, remoteJID, true, false)
		defer func() {
			WhatsAppComposeStatus(jid, remoteJID, false, false)
			WhatsAppPresence(jid, false)
		}()

		// Check Emoji Must Be Contain Only 1 Emoji Character
		if !gomoji.ContainsEmoji(emoji) && uniseg.GraphemeClusterCount(emoji) != 1 {
			return "", errors.New("WhatsApp Message React Emoji Must Be Contain Only 1 Emoji Character")
		}

		// Compose WhatsApp Proto
		msgReact := &waE2E.Message{
			ReactionMessage: &waE2E.ReactionMessage{
				Key: &waCommon.MessageKey{
					FromMe:    proto.Bool(true),
					ID:        proto.String(msgid),
					RemoteJID: proto.String(remoteJID.String()),
				},
				Text:              proto.String(emoji),
				SenderTimestampMS: proto.Int64(time.Now().UnixMilli()),
			},
		}

		// Send WhatsApp Message Proto
		_, err = WhatsAppClient[jid].SendMessage(context.Background(), remoteJID, msgReact)
		if err != nil {
			return "", err
		}

		return msgid, nil
	}

	// Return Error WhatsApp Client is not Valid
	return "", errors.New("WhatsApp Client is not Valid")

}

func WhatsAppMessageDelete(jid string, rjid string, msgid string) error {
	if WhatsAppClient[jid] != nil {
		var err error

		// Make Sure WhatsApp Client is OK
		err = WhatsAppIsClientOK(jid)
		if err != nil {
			return err
		}

		// Make Sure WhatsApp ID is Registered
		remoteJID, err := WhatsAppCheckJID(jid, rjid)
		if err != nil {
			return err
		}

		// Set Chat Presence
		WhatsAppPresence(jid, true)
		WhatsAppComposeStatus(jid, remoteJID, true, false)
		defer func() {
			WhatsAppComposeStatus(jid, remoteJID, false, false)
			WhatsAppPresence(jid, false)
		}()

		// Send WhatsApp Message Proto in Revoke Mode
		_, err = WhatsAppClient[jid].SendMessage(context.Background(), remoteJID, WhatsAppClient[jid].BuildRevoke(remoteJID, types.EmptyJID, msgid))
		if err != nil {
			return err
		}

		return nil
	}

	// Return Error WhatsApp Client is not Valid
	return errors.New("WhatsApp Client is not Valid")
}

func WhatsAppGroupGet(jid string) ([]types.GroupInfo, error) {
	if WhatsAppClient[jid] != nil {
		var err error

		// Make Sure WhatsApp Client is OK
		err = WhatsAppIsClientOK(jid)
		if err != nil {
			return nil, err
		}

		// Get Joined Group List
		groups, err := WhatsAppClient[jid].GetJoinedGroups(context.Background())
		if err != nil {
			return nil, err
		}

		// Put Group Information in List
		var gids []types.GroupInfo
		for _, group := range groups {
			gids = append(gids, *group)
		}

		// Return Group Information List
		return gids, nil
	}

	// Return Error WhatsApp Client is not Valid
	return nil, errors.New("WhatsApp Client is not Valid")
}

func WhatsAppGroupJoin(jid string, link string) (string, error) {
	if WhatsAppClient[jid] != nil {
		var err error

		// Make Sure WhatsApp Client is OK
		err = WhatsAppIsClientOK(jid)
		if err != nil {
			return "", err
		}

		// Join Group By Invitation Link
		gid, err := WhatsAppClient[jid].JoinGroupWithLink(context.Background(), link)
		if err != nil {
			return "", err
		}

		// Return Joined Group ID
		return gid.String(), nil
	}

	// Return Error WhatsApp Client is not Valid
	return "", errors.New("WhatsApp Client is not Valid")
}

func WhatsAppGroupLeave(jid string, gjid string) error {
	if WhatsAppClient[jid] != nil {
		var err error

		// Make Sure WhatsApp Client is OK
		err = WhatsAppIsClientOK(jid)
		if err != nil {
			return err
		}

		// Make Sure WhatsApp ID is Registered
		groupJID, err := WhatsAppCheckJID(jid, gjid)
		if err != nil {
			return err
		}

		// Make Sure WhatsApp ID is Group Server
		if groupJID.Server != types.GroupServer {
			return errors.New("WhatsApp Group ID is Not Group Server")
		}

		// Leave Group By Group ID
		return WhatsAppClient[jid].LeaveGroup(context.Background(), groupJID)
	}

	// Return Error WhatsApp Client is not Valid
	return errors.New("WhatsApp Client is not Valid")
}
