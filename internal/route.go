package internal

import (
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	eSwagger "github.com/swaggo/echo-swagger"

	"github.com/dimaskiddo/go-whatsapp-multidevice-rest/docs"
	"github.com/dimaskiddo/go-whatsapp-multidevice-rest/pkg/auth"
	"github.com/dimaskiddo/go-whatsapp-multidevice-rest/pkg/router"

	ctlAuth "github.com/dimaskiddo/go-whatsapp-multidevice-rest/internal/auth"
	typAuth "github.com/dimaskiddo/go-whatsapp-multidevice-rest/internal/auth/types"

	ctlIndex "github.com/dimaskiddo/go-whatsapp-multidevice-rest/internal/index"
	ctlWhatsApp "github.com/dimaskiddo/go-whatsapp-multidevice-rest/internal/whatsapp"
)

func Routes(e *echo.Echo) {
	// Configure OpenAPI / Swagger
	docs.SwaggerInfo.BasePath = router.BaseURL

	// Route for Index & Health Checks
	// ---------------------------------------------
	e.GET("/", ctlIndex.Index)
	e.GET("/status", ctlIndex.Index)
	e.GET("/health", ctlIndex.Index)
	e.GET(router.BaseURL, ctlIndex.Index)
	e.GET(router.BaseURL+"/", ctlIndex.Index)
	e.GET(router.BaseURL+"/status", ctlIndex.Index)
	e.GET(router.BaseURL+"/health", ctlIndex.Index)

	// Route for Pairing QR Code, Pairing Code & Session Status (supports Filament Admin & third-party)
	// ---------------------------------------------
	e.GET("/qr", ctlWhatsApp.GetQR)
	e.GET("/sessions/:session/qr", ctlWhatsApp.GetQR)
	e.GET("/pair-code", ctlWhatsApp.GetPairCode)
	e.POST("/pair-code", ctlWhatsApp.GetPairCode)
	e.GET("/sessions/:session/pair-code", ctlWhatsApp.GetPairCode)
	e.POST("/sessions/:session/pair-code", ctlWhatsApp.GetPairCode)
	e.GET("/sessions/:session/status", ctlWhatsApp.GetSessionStatus)
	e.GET("/session/status", ctlWhatsApp.GetSessionStatus)

	// Route for Re-pairing, Resetting and Disconnecting Sessions
	e.GET("/repair", ctlWhatsApp.RepairSession)
	e.POST("/repair", ctlWhatsApp.RepairSession)
	e.GET("/sessions/:session/repair", ctlWhatsApp.RepairSession)
	e.POST("/sessions/:session/repair", ctlWhatsApp.RepairSession)
	e.GET("/reset", ctlWhatsApp.RepairSession)
	e.POST("/reset", ctlWhatsApp.RepairSession)
	e.GET("/sessions/:session/reset", ctlWhatsApp.RepairSession)
	e.POST("/sessions/:session/reset", ctlWhatsApp.RepairSession)
	e.GET("/sessions/:session/logout", ctlWhatsApp.UnauthenticatedLogout)
	e.POST("/sessions/:session/logout", ctlWhatsApp.UnauthenticatedLogout)
	e.GET("/logout-session", ctlWhatsApp.UnauthenticatedLogout)
	e.POST("/logout-session", ctlWhatsApp.UnauthenticatedLogout)

	e.GET(router.BaseURL+"/qr", ctlWhatsApp.GetQR)
	e.GET(router.BaseURL+"/sessions/:session/qr", ctlWhatsApp.GetQR)
	e.GET(router.BaseURL+"/pair-code", ctlWhatsApp.GetPairCode)
	e.POST(router.BaseURL+"/pair-code", ctlWhatsApp.GetPairCode)
	e.GET(router.BaseURL+"/sessions/:session/pair-code", ctlWhatsApp.GetPairCode)
	e.POST(router.BaseURL+"/sessions/:session/pair-code", ctlWhatsApp.GetPairCode)
	e.GET(router.BaseURL+"/sessions/:session/status", ctlWhatsApp.GetSessionStatus)
	e.GET(router.BaseURL+"/session/status", ctlWhatsApp.GetSessionStatus)
	e.GET(router.BaseURL+"/repair", ctlWhatsApp.RepairSession)
	e.POST(router.BaseURL+"/repair", ctlWhatsApp.RepairSession)
	e.GET(router.BaseURL+"/sessions/:session/repair", ctlWhatsApp.RepairSession)
	e.POST(router.BaseURL+"/sessions/:session/repair", ctlWhatsApp.RepairSession)
	e.GET(router.BaseURL+"/sessions/:session/reset", ctlWhatsApp.RepairSession)
	e.POST(router.BaseURL+"/sessions/:session/reset", ctlWhatsApp.RepairSession)
	e.GET(router.BaseURL+"/sessions/:session/logout", ctlWhatsApp.UnauthenticatedLogout)
	e.POST(router.BaseURL+"/sessions/:session/logout", ctlWhatsApp.UnauthenticatedLogout)

	// Route for OpenAPI / Swagger
	// ---------------------------------------------
	e.GET(router.BaseURL+"/docs/*", eSwagger.WrapHandler)

	// Route for Auth
	// ---------------------------------------------
	e.GET(router.BaseURL+"/auth", ctlAuth.Auth, auth.BasicAuth())

	// Route for WhatsApp
	// ---------------------------------------------
	authJWTConfig := middleware.JWTConfig{
		Claims:     &typAuth.AuthJWTClaims{},
		SigningKey: []byte(auth.AuthJWTSecret),
	}

	e.POST(router.BaseURL+"/login", ctlWhatsApp.Login, middleware.JWTWithConfig(authJWTConfig))
	e.POST(router.BaseURL+"/login/pair", ctlWhatsApp.LoginPair, middleware.JWTWithConfig(authJWTConfig))
	e.POST(router.BaseURL+"/logout", ctlWhatsApp.Logout, middleware.JWTWithConfig(authJWTConfig))

	e.GET(router.BaseURL+"/registered", ctlWhatsApp.Registered, middleware.JWTWithConfig(authJWTConfig))

	e.GET(router.BaseURL+"/group", ctlWhatsApp.GetGroup, middleware.JWTWithConfig(authJWTConfig))
	e.POST(router.BaseURL+"/group/join", ctlWhatsApp.JoinGroup, middleware.JWTWithConfig(authJWTConfig))
	e.POST(router.BaseURL+"/group/leave", ctlWhatsApp.LeaveGroup, middleware.JWTWithConfig(authJWTConfig))

	// Route for Direct / PHPNuxBill Integration (GET / POST)
	e.GET(router.BaseURL+"/send/message", ctlWhatsApp.SendMessageGet)
	e.POST(router.BaseURL+"/send/message", ctlWhatsApp.SendMessageGet)
	e.GET("/send/message", ctlWhatsApp.SendMessageGet)
	e.POST("/send/message", ctlWhatsApp.SendMessageGet)

	e.POST(router.BaseURL+"/send/text", ctlWhatsApp.SendText, middleware.JWTWithConfig(authJWTConfig))
	e.POST(router.BaseURL+"/send/location", ctlWhatsApp.SendLocation, middleware.JWTWithConfig(authJWTConfig))
	e.POST(router.BaseURL+"/send/contact", ctlWhatsApp.SendContact, middleware.JWTWithConfig(authJWTConfig))
	e.POST(router.BaseURL+"/send/link", ctlWhatsApp.SendLink, middleware.JWTWithConfig(authJWTConfig))
	e.POST(router.BaseURL+"/send/document", ctlWhatsApp.SendDocument, middleware.JWTWithConfig(authJWTConfig))
	e.POST(router.BaseURL+"/send/image", ctlWhatsApp.SendImage, middleware.JWTWithConfig(authJWTConfig))
	e.POST(router.BaseURL+"/send/audio", ctlWhatsApp.SendAudio, middleware.JWTWithConfig(authJWTConfig))
	e.POST(router.BaseURL+"/send/video", ctlWhatsApp.SendVideo, middleware.JWTWithConfig(authJWTConfig))
	e.POST(router.BaseURL+"/send/sticker", ctlWhatsApp.SendSticker, middleware.JWTWithConfig(authJWTConfig))
	e.POST(router.BaseURL+"/send/poll", ctlWhatsApp.SendPoll, middleware.JWTWithConfig(authJWTConfig))

	e.POST(router.BaseURL+"/message/edit", ctlWhatsApp.MessageEdit, middleware.JWTWithConfig(authJWTConfig))
	e.POST(router.BaseURL+"/message/react", ctlWhatsApp.MessageEdit, middleware.JWTWithConfig(authJWTConfig))
	e.POST(router.BaseURL+"/message/delete", ctlWhatsApp.MessageDelete, middleware.JWTWithConfig(authJWTConfig))
}
