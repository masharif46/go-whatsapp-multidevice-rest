package whatsapp

import (
	"bytes"
	"io"
	"mime/multipart"
	"strconv"
	"strings"

	"github.com/golang-jwt/jwt"
	"github.com/labstack/echo/v4"

	"github.com/dimaskiddo/go-whatsapp-multidevice-rest/pkg/auth"
	"github.com/dimaskiddo/go-whatsapp-multidevice-rest/pkg/router"
	pkgWhatsApp "github.com/dimaskiddo/go-whatsapp-multidevice-rest/pkg/whatsapp"

	typAuth "github.com/dimaskiddo/go-whatsapp-multidevice-rest/internal/auth/types"
	typWhatsApp "github.com/dimaskiddo/go-whatsapp-multidevice-rest/internal/whatsapp/types"
)

func jwtPayload(c echo.Context) typAuth.AuthJWTClaimsPayload {
	jwtToken := c.Get("user").(*jwt.Token)
	jwtClaims := jwtToken.Claims.(*typAuth.AuthJWTClaims)

	return jwtClaims.Data
}

func convertFileToBytes(file multipart.File) ([]byte, error) {
	// Create Empty Buffer
	buffer := bytes.NewBuffer(nil)

	// Copy File Stream to Buffer
	_, err := io.Copy(buffer, file)
	if err != nil {
		return bytes.NewBuffer(nil).Bytes(), err
	}

	return buffer.Bytes(), nil
}

// Login
// @Summary     Generate QR Code for WhatsApp Multi-Device Login
// @Description Get QR Code for WhatsApp Multi-Device Login
// @Tags        WhatsApp Authentication
// @Accept      multipart/form-data
// @Produce     json
// @Produce     html
// @Param       output    formData  string  false  "Change Output Format in HTML or JSON"  Enums(html, json)  default(html)
// @Success     200
// @Security    BearerAuth
// @Router      /login [post]
func Login(c echo.Context) error {
	var err error
	jid := jwtPayload(c).JID

	var reqLogin typWhatsApp.RequestLogin
	reqLogin.Output = strings.TrimSpace(c.FormValue("output"))

	if len(reqLogin.Output) == 0 {
		reqLogin.Output = "html"
	}

	// Initialize WhatsApp Client
	pkgWhatsApp.WhatsAppInitClient(nil, jid)

	// Get WhatsApp QR Code Image
	qrCodeImage, qrCodeTimeout, err := pkgWhatsApp.WhatsAppLogin(jid)
	if err != nil {
		return router.ResponseInternalError(c, err.Error())
	}

	// If Return is Not QR Code But Reconnected
	// Then Return OK With Reconnected Status
	if qrCodeImage == "WhatsApp Client is Reconnected" {
		return router.ResponseSuccess(c, qrCodeImage)
	}

	var resLogin typWhatsApp.ResponseLogin
	resLogin.QRCode = qrCodeImage
	resLogin.Timeout = qrCodeTimeout

	if reqLogin.Output == "html" {
		htmlContent := `
    <html>
      <head>
        <title>WhatsApp Multi-Device Login</title>
        <meta name="viewport" content="width=device-width, initial-scale=1, shrink-to-fit=no" />
      </head>
      <body>
        <img src="` + resLogin.QRCode + `" />
        <p>
          <b>QR Code Scan</b>
          <br/>
          Timeout in ` + strconv.Itoa(resLogin.Timeout) + ` Second(s)
        </p>
      </body>
    </html>`

		return router.ResponseSuccessWithHTML(c, htmlContent)
	}

	return router.ResponseSuccessWithData(c, "Successfully Generated QR Code", resLogin)
}

// GetQR
// Returns live status and pairing QR code if session is not yet paired
func GetQR(c echo.Context) error {
	session := strings.TrimSpace(c.Param("session"))
	if len(session) == 0 {
		session = strings.TrimSpace(c.QueryParam("session"))
	}
	if len(session) == 0 {
		session = "default"
	}

	// Check if this session or any registered client is already connected
	client := pkgWhatsApp.WhatsAppClient[session]
	if client == nil || client.Store.ID == nil || !client.IsConnected() {
		for _, cli := range pkgWhatsApp.WhatsAppClient {
			if cli != nil && cli.Store.ID != nil && cli.IsConnected() {
				client = cli
				pkgWhatsApp.WhatsAppClient[session] = cli
				break
			}
		}
	}

	if client != nil && client.Store.ID != nil && client.IsConnected() {
		phone := client.Store.ID.User
		return c.JSON(200, echo.Map{
			"status":    true,
			"code":      200,
			"connected": true,
			"phone":     phone,
			"push_name": client.Store.PushName,
			"message":   "WhatsApp Client is Connected",
		})
	}

	// Initialize client if not yet created
	pkgWhatsApp.WhatsAppInitClient(nil, session)

	// Get WhatsApp QR Code Image
	qrCodeImage, qrCodeTimeout, err := pkgWhatsApp.WhatsAppLogin(session)
	if err != nil {
		return c.JSON(500, echo.Map{
			"status":    false,
			"code":      500,
			"connected": false,
			"error":     err.Error(),
		})
	}

	if qrCodeImage == "WhatsApp Client is Reconnected" {
		return c.JSON(200, echo.Map{
			"status":    true,
			"code":      200,
			"connected": true,
			"message":   "WhatsApp Client is Reconnected",
		})
	}

	return c.JSON(200, echo.Map{
		"status":    true,
		"code":      200,
		"connected": false,
		"qr":        qrCodeImage,
		"timeout":   qrCodeTimeout,
	})
}

// GetSessionStatus returns connection state and details for a given session or primary device
func GetSessionStatus(c echo.Context) error {
	session := strings.TrimSpace(c.Param("session"))
	if len(session) == 0 {
		session = strings.TrimSpace(c.QueryParam("session"))
	}
	if len(session) == 0 {
		session = "default"
	}

	client := pkgWhatsApp.WhatsAppClient[session]
	if client == nil || client.Store.ID == nil || !client.IsConnected() {
		for _, cli := range pkgWhatsApp.WhatsAppClient {
			if cli != nil && cli.Store.ID != nil && cli.IsConnected() {
				client = cli
				pkgWhatsApp.WhatsAppClient[session] = cli
				break
			}
		}
	}

	if client != nil && client.Store.ID != nil && client.IsConnected() {
		return c.JSON(200, echo.Map{
			"status":    true,
			"code":      200,
			"connected": true,
			"session":   session,
			"phone":     client.Store.ID.User,
			"push_name": client.Store.PushName,
			"message":   "WhatsApp Client is Connected",
		})
	}

	return c.JSON(200, echo.Map{
		"status":    false,
		"code":      200,
		"connected": false,
		"session":   session,
		"message":   "WhatsApp Client is not Connected. Scan QR Code to pair.",
	})
}

// GetPairCode returns an 8-digit pairing code for linking phone directly without QR scanning
func GetPairCode(c echo.Context) error {
	session := strings.TrimSpace(c.Param("session"))
	if len(session) == 0 {
		session = strings.TrimSpace(c.QueryParam("session"))
	}
	if len(session) == 0 {
		session = "default"
	}

	phone := strings.TrimSpace(c.QueryParam("phone"))
	if len(phone) == 0 {
		phone = strings.TrimSpace(c.FormValue("phone"))
	}
	if len(phone) == 0 {
		return c.JSON(400, echo.Map{
			"status": false,
			"code":   400,
			"error":  "Missing phone parameter. Please pass phone number with country code, e.g. ?phone=88017xxxxxxxx",
		})
	}

	phone = strings.ReplaceAll(phone, "+", "")
	phone = strings.ReplaceAll(phone, " ", "")
	phone = strings.ReplaceAll(phone, "-", "")

	pkgWhatsApp.WhatsAppInitClient(nil, session)
	client := pkgWhatsApp.WhatsAppClient[session]
	if client != nil && client.Store.ID != nil && client.IsConnected() {
		return c.JSON(200, echo.Map{
			"status":    true,
			"code":      200,
			"connected": true,
			"message":   "WhatsApp Client is already Connected",
		})
	}

	pairCode, timeout, err := pkgWhatsApp.WhatsAppLoginPairPhone(session, phone)
	if err != nil {
		return c.JSON(500, echo.Map{
			"status": false,
			"code":   500,
			"error":  err.Error(),
		})
	}

	return c.JSON(200, echo.Map{
		"status":    true,
		"code":      200,
		"pair_code": pairCode,
		"timeout":   timeout,
	})
}

// PairPhone
// @Summary     Pair Phone for WhatsApp Multi-Device Login
// @Description Get Pairing Code for WhatsApp Multi-Device Login
// @Tags        WhatsApp Authentication
// @Accept      multipart/form-data
// @Produce     json
// @Success     200
// @Security    BearerAuth
// @Router      /login/pair [post]
func LoginPair(c echo.Context) error {
	var err error
	jid := jwtPayload(c).JID

	// Initialize WhatsApp Client
	pkgWhatsApp.WhatsAppInitClient(nil, jid)

	// Get WhatsApp pairing Code text
	pairCode, pairCodeTimeout, err := pkgWhatsApp.WhatsAppLoginPair(jid)
	if err != nil {
		return router.ResponseInternalError(c, err.Error())
	}

	// If Return is not pairing code but Reconnected
	// Then Return OK With Reconnected Status
	if pairCode == "WhatsApp Client is Reconnected" {
		return router.ResponseSuccess(c, pairCode)
	}

	var resPairing typWhatsApp.ResponsePairing
	resPairing.PairCode = pairCode
	resPairing.Timeout = pairCodeTimeout

	return router.ResponseSuccessWithData(c, "Successfully Generated Pairing Code", resPairing)
}

// Logout
// @Summary     Logout Device from WhatsApp Multi-Device
// @Description Make Device Logout from WhatsApp Multi-Device
// @Tags        WhatsApp Authentication
// @Produce     json
// @Success     200
// @Security    BearerAuth
// @Router      /logout [post]
func Logout(c echo.Context) error {
	var err error
	jid := jwtPayload(c).JID

	err = pkgWhatsApp.WhatsAppLogout(jid)
	if err != nil {
		return router.ResponseInternalError(c, err.Error())
	}

	return router.ResponseSuccess(c, "Successfully Logged Out")
}

// Registered
// @Summary     Check If WhatsApp Personal ID is Registered
// @Description Check WhatsApp Personal ID is Registered
// @Tags        WhatsApp Information
// @Produce     json
// @Param       msisdn    query  string  true  "WhatsApp Personal ID to Check"
// @Success     200
// @Security    BearerAuth
// @Router      /registered [get]
func Registered(c echo.Context) error {
	jid := jwtPayload(c).JID
	remoteJID := strings.TrimSpace(c.QueryParam("msisdn"))

	if len(remoteJID) == 0 {
		return router.ResponseInternalError(c, "Missing Query Value MSISDN")
	}

	err := pkgWhatsApp.WhatsAppCheckRegistered(jid, remoteJID)
	if err != nil {
		return router.ResponseInternalError(c, err.Error())
	}

	return router.ResponseSuccess(c, "WhatsApp Personal ID is Registered")
}

// GetGroup
// @Summary     Get Joined Groups Information
// @Description Get Joined Groups Information from WhatsApp
// @Tags        WhatsApp Group
// @Produce     json
// @Success     200
// @Security    BearerAuth
// @Router      /group [get]
func GetGroup(c echo.Context) error {
	var err error
	jid := jwtPayload(c).JID

	group, err := pkgWhatsApp.WhatsAppGroupGet(jid)
	if err != nil {
		return router.ResponseInternalError(c, err.Error())
	}

	return router.ResponseSuccessWithData(c, "Successfully List Joined Groups", group)
}

// JoinGroup
// @Summary     Join Group From Invitation Link
// @Description Joining to Group From Invitation Link from WhatsApp
// @Tags        WhatsApp Group
// @Produce     json
// @Param       link    formData  string  true  "Group Invitation Link"
// @Success     200
// @Security    BearerAuth
// @Router      /group/join [post]
func JoinGroup(c echo.Context) error {
	var err error
	jid := jwtPayload(c).JID

	var reqGroupJoin typWhatsApp.RequestGroupJoin
	reqGroupJoin.Link = strings.TrimSpace(c.FormValue("link"))

	if len(reqGroupJoin.Link) == 0 {
		return router.ResponseBadRequest(c, "Missing Form Value Link")
	}

	group, err := pkgWhatsApp.WhatsAppGroupJoin(jid, reqGroupJoin.Link)
	if err != nil {
		return router.ResponseInternalError(c, err.Error())
	}

	return router.ResponseSuccessWithData(c, "Successfully Joined Group From Invitation Link", group)
}

// LeaveGroup
// @Summary     Leave Group By Group ID
// @Description Leaving Group By Group ID from WhatsApp
// @Tags        WhatsApp Group
// @Produce     json
// @Param       groupid    formData  string  true  "Group ID"
// @Success     200
// @Security    BearerAuth
// @Router      /group/leave [post]
func LeaveGroup(c echo.Context) error {
	var err error
	jid := jwtPayload(c).JID

	var reqGroupLeave typWhatsApp.RequestGroupLeave
	reqGroupLeave.GID = strings.TrimSpace(c.FormValue("groupid"))

	if len(reqGroupLeave.GID) == 0 {
		return router.ResponseBadRequest(c, "Missing Form Value Group ID")
	}

	err = pkgWhatsApp.WhatsAppGroupLeave(jid, reqGroupLeave.GID)
	if err != nil {
		return router.ResponseInternalError(c, err.Error())
	}

	return router.ResponseSuccess(c, "Successfully Leave Group By Group ID")
}

// SendText
// @Summary     Send Text Message
// @Description Send Text Message to Spesific WhatsApp Personal ID or Group ID
// @Tags        WhatsApp Send Message
// @Accept      multipart/form-data
// @Produce     json
// @Param       msisdn    formData  string  true  "Destination WhatsApp Personal ID or Group ID"
// @Param       message   formData  string  true  "Text Message"
// @Success     200
// @Security    BearerAuth
// @Router      /send/text [post]
func SendText(c echo.Context) error {
	var err error
	jid := jwtPayload(c).JID

	var reqSendMessage typWhatsApp.RequestSendMessage
	reqSendMessage.RJID = strings.TrimSpace(c.FormValue("msisdn"))
	reqSendMessage.Message = strings.Replace(strings.TrimSpace(c.FormValue("message")), "\\n", "\n", -1)

	if len(reqSendMessage.RJID) == 0 {
		return router.ResponseBadRequest(c, "Missing Form Value MSISDN")
	}

	if len(reqSendMessage.Message) == 0 {
		return router.ResponseBadRequest(c, "Missing Form Value Message")
	}

	var resSendMessage typWhatsApp.ResponseSendMessage
	resSendMessage.MsgID, err = pkgWhatsApp.WhatsAppSendText(jid, reqSendMessage.RJID, reqSendMessage.Message)
	if err != nil {
		return router.ResponseInternalError(c, err.Error())
	}

	return router.ResponseSuccessWithData(c, "Successfully Send Text Message", resSendMessage)
}

// SendMessageGet
// Handles GET and POST /send/message with query, form, or JSON parameters for easy third-party / PHPNuxBill integration
func SendMessageGet(c echo.Context) error {
	var err error

	type msgReq struct {
		Phone   string `json:"phone" form:"phone" query:"phone"`
		MSISDN  string `json:"msisdn" form:"msisdn" query:"msisdn"`
		Number  string `json:"number" form:"number" query:"number"`
		Message string `json:"message" form:"message" query:"message"`
		Text    string `json:"text" form:"text" query:"text"`
		Secret  string `json:"secret" form:"secret" query:"secret"`
		Token   string `json:"token" form:"token" query:"token"`
		Pass    string `json:"password" form:"password" query:"password"`
		JID     string `json:"jid" form:"jid" query:"jid"`
	}

	var req msgReq
	_ = c.Bind(&req)

	// Fallback to manual extraction if binding missed anything
	secret := strings.TrimSpace(req.Secret)
	if len(secret) == 0 {
		secret = strings.TrimSpace(req.Token)
	}
	if len(secret) == 0 {
		secret = strings.TrimSpace(req.Pass)
	}
	if len(secret) == 0 {
		secret = strings.TrimSpace(c.QueryParam("secret"))
	}
	if len(secret) == 0 {
		secret = strings.TrimSpace(c.FormValue("secret"))
	}
	if len(secret) == 0 {
		secret = strings.TrimSpace(c.Request().Header.Get("X-Api-Key"))
	}

	// Verify secret against AuthBasicPassword or AuthJWTSecret
	if len(auth.AuthBasicPassword) > 0 && secret != auth.AuthBasicPassword && secret != auth.AuthJWTSecret {
		return router.ResponseBadRequest(c, "Invalid or Missing Secret/Password")
	}

	// Extract destination phone / number
	rjid := strings.TrimSpace(req.Phone)
	if len(rjid) == 0 {
		rjid = strings.TrimSpace(req.MSISDN)
	}
	if len(rjid) == 0 {
		rjid = strings.TrimSpace(req.Number)
	}
	if len(rjid) == 0 {
		rjid = strings.TrimSpace(c.QueryParam("phone"))
	}
	if len(rjid) == 0 {
		rjid = strings.TrimSpace(c.FormValue("phone"))
	}
	if len(rjid) == 0 {
		rjid = strings.TrimSpace(c.FormValue("number"))
	}

	// Clean destination number (strip leading +, spaces, hyphens)
	rjid = strings.ReplaceAll(rjid, "+", "")
	rjid = strings.ReplaceAll(rjid, " ", "")
	rjid = strings.ReplaceAll(rjid, "-", "")

	// Extract message
	message := strings.TrimSpace(req.Message)
	if len(message) == 0 {
		message = strings.TrimSpace(req.Text)
	}
	if len(message) == 0 {
		message = strings.TrimSpace(c.QueryParam("message"))
	}
	if len(message) == 0 {
		message = strings.TrimSpace(c.FormValue("message"))
	}
	message = strings.Replace(message, "\\n", "\n", -1)

	if len(rjid) == 0 {
		return router.ResponseBadRequest(c, "Missing Destination Parameter (phone/msisdn/number)")
	}
	if len(message) == 0 {
		return router.ResponseBadRequest(c, "Missing Message Parameter (message/text)")
	}

	// Determine sender JID
	jid := strings.TrimSpace(req.JID)
	if len(jid) == 0 {
		jid = strings.TrimSpace(c.QueryParam("jid"))
	}
	if len(jid) == 0 {
		jid = strings.TrimSpace(c.FormValue("jid"))
	}

	// If no JID specified, auto-select the first connected client
	if len(jid) == 0 {
		for k, client := range pkgWhatsApp.WhatsAppClient {
			if client != nil && client.IsConnected() {
				jid = k
				break
			}
		}
		// Fallback to any client in the map
		if len(jid) == 0 {
			for k := range pkgWhatsApp.WhatsAppClient {
				jid = k
				break
			}
		}
	}

	if len(jid) == 0 || pkgWhatsApp.WhatsAppClient[jid] == nil {
		return router.ResponseInternalError(c, "No WhatsApp client found. Please scan QR or login first.")
	}

	var resSendMessage typWhatsApp.ResponseSendMessage
	resSendMessage.MsgID, err = pkgWhatsApp.WhatsAppSendText(jid, rjid, message)
	if err != nil {
		return router.ResponseInternalError(c, err.Error())
	}

	return router.ResponseSuccessWithData(c, "Successfully Send Text Message", resSendMessage)
}

// SendLocation
// @Summary     Send Location Message
// @Description Send Location Message to Spesific WhatsApp Personal ID or Group ID
// @Tags        WhatsApp Send Message
// @Accept      multipart/form-data
// @Produce     json
// @Param       msisdn    formData  string  true  "Destination WhatsApp Personal ID or Group ID"
// @Param       latitude  formData  number  true  "Location Latitude"
// @Param       longitude formData  number  true  "Location Longitude"
// @Success     200
// @Security    BearerAuth
// @Router      /send/location [post]
func SendLocation(c echo.Context) error {
	var err error
	jid := jwtPayload(c).JID

	var reqSendLocation typWhatsApp.RequestSendLocation
	reqSendLocation.RJID = strings.TrimSpace(c.FormValue("msisdn"))

	reqSendLocation.Latitude, err = strconv.ParseFloat(strings.TrimSpace(c.FormValue("latitude")), 64)
	if err != nil {
		return router.ResponseInternalError(c, "Error While Decoding Latitude to Float64")
	}

	reqSendLocation.Longitude, err = strconv.ParseFloat(strings.TrimSpace(c.FormValue("longitude")), 64)
	if err != nil {
		return router.ResponseInternalError(c, "Error While Decoding Longitude to Float64")
	}

	if len(reqSendLocation.RJID) == 0 {
		return router.ResponseBadRequest(c, "Missing Form Value MSISDN")
	}

	var resSendMessage typWhatsApp.ResponseSendMessage
	resSendMessage.MsgID, err = pkgWhatsApp.WhatsAppSendLocation(jid, reqSendLocation.RJID, reqSendLocation.Latitude, reqSendLocation.Longitude)
	if err != nil {
		return router.ResponseInternalError(c, err.Error())
	}

	return router.ResponseSuccessWithData(c, "Successfully Send Location Message", resSendMessage)
}

// SendContact
// @Summary     Send Contact Message
// @Description Send Contact Message to Spesific WhatsApp Personal ID or Group ID
// @Tags        WhatsApp Send Message
// @Accept      multipart/form-data
// @Produce     json
// @Param       msisdn    formData  string  true  "Destination WhatsApp Personal ID or Group ID"
// @Param       name      formData  string  true  "Contact Name"
// @Param       phone     formData  string  true  "Contact Phone"
// @Success     200
// @Security    BearerAuth
// @Router      /send/contact [post]
func SendContact(c echo.Context) error {
	var err error
	jid := jwtPayload(c).JID

	var reqSendContact typWhatsApp.RequestSendContact
	reqSendContact.RJID = strings.TrimSpace(c.FormValue("msisdn"))
	reqSendContact.Name = strings.TrimSpace(c.FormValue("name"))
	reqSendContact.Phone = strings.TrimSpace(c.FormValue("phone"))

	if len(reqSendContact.RJID) == 0 {
		return router.ResponseBadRequest(c, "Missing Form Value MSISDN")
	}

	if len(reqSendContact.Name) == 0 {
		return router.ResponseBadRequest(c, "Missing Form Value Name")
	}

	if len(reqSendContact.Phone) == 0 {
		return router.ResponseBadRequest(c, "Missing Form Value Phone")
	}

	var resSendMessage typWhatsApp.ResponseSendMessage
	resSendMessage.MsgID, err = pkgWhatsApp.WhatsAppSendContact(jid, reqSendContact.RJID, reqSendContact.Name, reqSendContact.Phone)
	if err != nil {
		return router.ResponseInternalError(c, err.Error())
	}

	return router.ResponseSuccessWithData(c, "Successfully Send Contact Message", resSendMessage)
}

// SendLink
// @Summary     Send Link Message
// @Description Send Link Message to Spesific WhatsApp Personal ID or Group ID
// @Tags        WhatsApp Send Message
// @Accept      multipart/form-data
// @Produce     json
// @Param       msisdn    formData  string  true  "Destination WhatsApp Personal ID or Group ID"
// @Param       caption   formData  string  false "Link Caption"
// @Param       url       formData  string  true  "Link URL"
// @Success     200
// @Security    BearerAuth
// @Router      /send/link [post]
func SendLink(c echo.Context) error {
	var err error
	jid := jwtPayload(c).JID

	var reqSendLink typWhatsApp.RequestSendLink
	reqSendLink.RJID = strings.TrimSpace(c.FormValue("msisdn"))
	reqSendLink.Caption = strings.TrimSpace(c.FormValue("caption"))
	reqSendLink.URL = strings.TrimSpace(c.FormValue("url"))

	if len(reqSendLink.RJID) == 0 {
		return router.ResponseBadRequest(c, "Missing Form Value MSISDN")
	}

	if len(reqSendLink.URL) == 0 {
		return router.ResponseBadRequest(c, "Missing Form Value URL")
	}

	var resSendMessage typWhatsApp.ResponseSendMessage
	resSendMessage.MsgID, err = pkgWhatsApp.WhatsAppSendLink(jid, reqSendLink.RJID, reqSendLink.Caption, reqSendLink.URL)
	if err != nil {
		return router.ResponseInternalError(c, err.Error())
	}

	return router.ResponseSuccessWithData(c, "Successfully Send Link Message", resSendMessage)
}

// SendDocument
// @Summary     Send Document Message
// @Description Send Document Message to Spesific WhatsApp Personal ID or Group ID
// @Tags        WhatsApp Send Message
// @Accept      multipart/form-data
// @Produce     json
// @Param       msisdn    formData  string  true   "Destination WhatsApp Personal ID or Group ID"
// @Param       document  formData  file    true   "Document File"
// @Param       caption   formData  string  false  "Caption Document Message"
// @Success     200
// @Security    BearerAuth
// @Router      /send/document [post]
func SendDocument(c echo.Context) error {
	return sendMedia(c, "document")
}

// SendImage
// @Summary     Send Image Message
// @Description Send Image Message to Spesific WhatsApp Personal ID or Group ID
// @Tags        WhatsApp Send Message
// @Accept      multipart/form-data
// @Produce     json
// @Param       msisdn    formData  string  true  "Destination WhatsApp Personal ID or Group ID"
// @Param       caption   formData  string  true  "Caption Image Message"
// @Param       image     formData  file    true  "Image File"
// @Param       viewonce  formData  bool    false "Is View Once"              default(false)
// @Success     200
// @Security    BearerAuth
// @Router      /send/image [post]
func SendImage(c echo.Context) error {
	return sendMedia(c, "image")
}

// SendAudio
// @Summary     Send Audio Message
// @Description Send Audio Message to Spesific WhatsApp Personal ID or Group ID
// @Tags        WhatsApp Send Message
// @Accept      multipart/form-data
// @Produce     json
// @Param       msisdn    formData  string  true  "Destination WhatsApp Personal ID or Group ID"
// @Param       audio     formData  file    true  "Audio File"
// @Success     200
// @Security    BearerAuth
// @Router      /send/audio [post]
func SendAudio(c echo.Context) error {
	return sendMedia(c, "audio")
}

// SendVideo
// @Summary     Send Video Message
// @Description Send Video Message to Spesific WhatsApp Personal ID or Group ID
// @Tags        WhatsApp Send Message
// @Accept      multipart/form-data
// @Produce     json
// @Param       msisdn    formData  string  true  "Destination WhatsApp Personal ID or Group ID"
// @Param       caption   formData  string  true  "Caption Video Message"
// @Param       video     formData  file    true  "Video File"
// @Param       viewonce  formData  bool    false "Is View Once"              default(false)
// @Success     200
// @Security    BearerAuth
// @Router      /send/video [post]
func SendVideo(c echo.Context) error {
	return sendMedia(c, "video")
}

// SendSticker
// @Summary     Send Sticker Message
// @Description Send Sticker Message to Spesific WhatsApp Personal ID or Group ID
// @Tags        WhatsApp Send Message
// @Accept      multipart/form-data
// @Produce     json
// @Param       msisdn    formData  string  true  "Destination WhatsApp Personal ID or Group ID"
// @Param       sticker   formData  file    true  "Sticker File"
// @Success     200
// @Security    BearerAuth
// @Router      /send/sticker [post]
func SendSticker(c echo.Context) error {
	return sendMedia(c, "sticker")
}

func sendMedia(c echo.Context, mediaType string) error {
	var err error
	jid := jwtPayload(c).JID

	var reqSendMessage typWhatsApp.RequestSendMessage
	reqSendMessage.RJID = strings.TrimSpace(c.FormValue("msisdn"))

	// Read Uploaded File Based on Send Media Type
	var fileStream multipart.File
	var fileHeader *multipart.FileHeader

	switch mediaType {
	case "document":
		fileStream, fileHeader, err = c.Request().FormFile("document")
		reqSendMessage.Message = fileHeader.Filename
		reqSendMessage.Caption = strings.TrimSpace(c.FormValue("caption"))

	case "image":
		fileStream, fileHeader, err = c.Request().FormFile("image")
		reqSendMessage.Message = strings.TrimSpace(c.FormValue("caption"))

	case "audio":
		fileStream, fileHeader, err = c.Request().FormFile("audio")

	case "video":
		fileStream, fileHeader, err = c.Request().FormFile("video")
		reqSendMessage.Message = strings.TrimSpace(c.FormValue("caption"))

	case "sticker":
		fileStream, fileHeader, err = c.Request().FormFile("sticker")
	}

	// Don't Forget to Close The File Stream
	defer fileStream.Close()

	// Get Uploaded File MIME Type
	fileType := fileHeader.Header.Get("Content-Type")

	// If There are Some Errors While Opeening The File Stream
	// Return Bad Request with Original Error Message
	if err != nil {
		return router.ResponseBadRequest(c, err.Error())
	}

	// Make Sure RJID is Filled
	if len(reqSendMessage.RJID) == 0 {
		return router.ResponseBadRequest(c, "Missing Form Value MSISDN")
	}

	// Check if Media Type is "image" or "video"
	// Then Parse ViewOnce Parameter
	if mediaType == "image" || mediaType == "video" {
		isViewOnce := strings.TrimSpace(c.FormValue("viewonce"))

		if len(isViewOnce) == 0 {
			// If ViewOnce Parameter Doesn't Exist or Empty String
			// Then Set it Default to False
			reqSendMessage.ViewOnce = false
		} else {
			// If ViewOnce Parameter is not Empty
			// Then Parse it to Bool
			reqSendMessage.ViewOnce, err = strconv.ParseBool(isViewOnce)
			if err != nil {
				return router.ResponseBadRequest(c, err.Error())
			}
		}
	}

	// Convert File Stream in to Bytes
	// Since WhatsApp Proto for Media is only Accepting Bytes format
	fileBytes, err := convertFileToBytes(fileStream)
	if err != nil {
		return router.ResponseInternalError(c, err.Error())
	}

	// Send Media Message Based on Media Type
	var resSendMessage typWhatsApp.ResponseSendMessage
	switch mediaType {
	case "document":
		resSendMessage.MsgID, err = pkgWhatsApp.WhatsAppSendDocument(jid, reqSendMessage.RJID, fileBytes, fileType, reqSendMessage.Message, reqSendMessage.Caption)

	case "image":
		resSendMessage.MsgID, err = pkgWhatsApp.WhatsAppSendImage(jid, reqSendMessage.RJID, fileBytes, fileType, reqSendMessage.Message, reqSendMessage.ViewOnce)

	case "audio":
		resSendMessage.MsgID, err = pkgWhatsApp.WhatsAppSendAudio(jid, reqSendMessage.RJID, fileBytes, fileType)

	case "video":
		resSendMessage.MsgID, err = pkgWhatsApp.WhatsAppSendVideo(jid, reqSendMessage.RJID, fileBytes, fileType, reqSendMessage.Message, reqSendMessage.ViewOnce)

	case "sticker":
		resSendMessage.MsgID, err = pkgWhatsApp.WhatsAppSendSticker(jid, reqSendMessage.RJID, fileBytes)
	}

	// Return Internal Server Error
	// When Detected There are Some Errors While Sending The Media Message
	if err != nil {
		return router.ResponseInternalError(c, err.Error())
	}

	return router.ResponseSuccessWithData(c, "Successfully Send Media Message", resSendMessage)
}

// SendPoll
// @Summary     Send Poll
// @Description Send Poll to Spesific WhatsApp Personal ID or Group ID
// @Tags        WhatsApp Send Message
// @Accept      multipart/form-data
// @Produce     json
// @Param       msisdn       formData  string  true  "Destination WhatsApp Personal ID or Group ID"
// @Param       question     formData  string  true  "Poll Question"
// @Param       options      formData  string  true  "Poll Options (Comma Seperated for New Options)"
// @Param       multianswer  formData  bool    false "Is Multiple Answer"             default(false)
// @Success     200
// @Security    BearerAuth
// @Router      /send/poll [post]
func SendPoll(c echo.Context) error {
	var err error
	jid := jwtPayload(c).JID

	var reqSendPoll typWhatsApp.RequestSendPoll
	reqSendPoll.RJID = strings.TrimSpace(c.FormValue("msisdn"))
	reqSendPoll.Question = strings.TrimSpace(c.FormValue("question"))
	reqSendPoll.Options = strings.TrimSpace(c.FormValue("options"))

	if len(reqSendPoll.RJID) == 0 {
		return router.ResponseBadRequest(c, "Missing Form Value MSISDN")
	}

	if len(reqSendPoll.Question) == 0 {
		return router.ResponseBadRequest(c, "Missing Form Value Question")
	}

	if len(reqSendPoll.Options) == 0 {
		return router.ResponseBadRequest(c, "Missing Form Value Options")
	}

	isMultiAnswer := strings.TrimSpace(c.FormValue("multianswer"))
	if len(isMultiAnswer) == 0 {
		// If MultiAnswer Parameter Doesn't Exist or Empty String
		// Then Set it Default to False
		reqSendPoll.MultiAnswer = false
	} else {
		// If MultiAnswer Parameter is not Empty
		// Then Parse it to Bool
		reqSendPoll.MultiAnswer, err = strconv.ParseBool(isMultiAnswer)
		if err != nil {
			return router.ResponseBadRequest(c, err.Error())
		}
	}

	pollOptions := strings.Split(reqSendPoll.Options, ",")
	for i, str := range pollOptions {
		pollOptions[i] = strings.TrimSpace(str)
	}

	var resSendMessage typWhatsApp.ResponseSendMessage
	resSendMessage.MsgID, err = pkgWhatsApp.WhatsAppSendPoll(jid, reqSendPoll.RJID, reqSendPoll.Question, pollOptions, reqSendPoll.MultiAnswer)
	if err != nil {
		return router.ResponseInternalError(c, err.Error())
	}

	return router.ResponseSuccessWithData(c, "Successfully Send Poll Message", resSendMessage)
}

// MessageEdit
// @Summary     Update Message
// @Description Update Message to Spesific WhatsApp Personal ID or Group ID
// @Tags        WhatsApp Message
// @Accept      multipart/form-data
// @Produce     json
// @Param       msisdn    formData  string  true  "Destination WhatsApp Personal ID or Group ID"
// @Param       messageid formData  string  true  "Message ID"
// @Param       message   formData  string  true  "Text Message"
// @Success     200
// @Security    BearerAuth
// @Router      /message/edit [post]
func MessageEdit(c echo.Context) error {
	var err error
	jid := jwtPayload(c).JID

	var reqMessageUpdate typWhatsApp.RequestMessage
	reqMessageUpdate.RJID = strings.TrimSpace(c.FormValue("msisdn"))
	reqMessageUpdate.MSGID = strings.TrimSpace(c.FormValue("messageid"))
	reqMessageUpdate.Message = strings.TrimSpace(c.FormValue("message"))

	if len(reqMessageUpdate.RJID) == 0 {
		return router.ResponseBadRequest(c, "Missing Form Value MSISDN")
	}

	if len(reqMessageUpdate.MSGID) == 0 {
		return router.ResponseBadRequest(c, "Missing Form Value Message ID")
	}

	if len(reqMessageUpdate.Message) == 0 {
		return router.ResponseBadRequest(c, "Missing Form Value Message")
	}

	var resSendMessage typWhatsApp.ResponseSendMessage
	resSendMessage.MsgID, err = pkgWhatsApp.WhatsAppMessageEdit(jid, reqMessageUpdate.RJID, reqMessageUpdate.MSGID, reqMessageUpdate.Message)
	if err != nil {
		return router.ResponseInternalError(c, err.Error())
	}

	return router.ResponseSuccessWithData(c, "Successfully Update Message", resSendMessage)
}

// MessageReact
// @Summary     React Message
// @Description React Message to Spesific WhatsApp Personal ID or Group ID
// @Tags        WhatsApp Message
// @Accept      multipart/form-data
// @Produce     json
// @Param       msisdn    formData  string  true  "Destination WhatsApp Personal ID or Group ID"
// @Param       messageid formData  string  true  "Message ID"
// @Param       emoji     formData  string  true  "Reaction Emoji"
// @Success     200
// @Security    BearerAuth
// @Router      /message/react [post]
func MessageReact(c echo.Context) error {
	var err error
	jid := jwtPayload(c).JID

	var reqMessageUpdate typWhatsApp.RequestMessage
	reqMessageUpdate.RJID = strings.TrimSpace(c.FormValue("msisdn"))
	reqMessageUpdate.MSGID = strings.TrimSpace(c.FormValue("messageid"))
	reqMessageUpdate.Emoji = strings.TrimSpace(c.FormValue("emoji"))

	if len(reqMessageUpdate.RJID) == 0 {
		return router.ResponseBadRequest(c, "Missing Form Value MSISDN")
	}

	if len(reqMessageUpdate.MSGID) == 0 {
		return router.ResponseBadRequest(c, "Missing Form Value Message ID")
	}

	if len(reqMessageUpdate.Emoji) == 0 {
		return router.ResponseBadRequest(c, "Missing Form Value Emoji")
	}

	var resSendMessage typWhatsApp.ResponseSendMessage
	resSendMessage.MsgID, err = pkgWhatsApp.WhatsAppMessageReact(jid, reqMessageUpdate.RJID, reqMessageUpdate.MSGID, reqMessageUpdate.Emoji)
	if err != nil {
		return router.ResponseInternalError(c, err.Error())
	}

	return router.ResponseSuccessWithData(c, "Successfully React Message", resSendMessage)
}

// MessageDelete
// @Summary     Delete Message
// @Description Delete Message to Spesific WhatsApp Personal ID or Group ID
// @Tags        WhatsApp Message
// @Accept      multipart/form-data
// @Produce     json
// @Param       msisdn    formData  string  true  "Destination WhatsApp Personal ID or Group ID"
// @Param       messageid formData  string  true  "Message ID"
// @Success     200
// @Security    BearerAuth
// @Router      /message/delete [post]
func MessageDelete(c echo.Context) error {
	var err error
	jid := jwtPayload(c).JID

	var reqMessageUpdate typWhatsApp.RequestMessage
	reqMessageUpdate.RJID = strings.TrimSpace(c.FormValue("msisdn"))
	reqMessageUpdate.MSGID = strings.TrimSpace(c.FormValue("messageid"))

	if len(reqMessageUpdate.RJID) == 0 {
		return router.ResponseBadRequest(c, "Missing Form Value MSISDN")
	}

	if len(reqMessageUpdate.MSGID) == 0 {
		return router.ResponseBadRequest(c, "Missing Form Value Message ID")
	}

	err = pkgWhatsApp.WhatsAppMessageDelete(jid, reqMessageUpdate.RJID, reqMessageUpdate.MSGID)
	if err != nil {
		return router.ResponseInternalError(c, err.Error())
	}

	return router.ResponseSuccess(c, "Successfully Delete Message")
}
