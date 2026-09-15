package feishu

import (
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

type Addition struct {
	RootFolderID       string `json:"root_folder_id" help:"Folder token to mount. Leave empty to mount the authorized user's personal Drive root."`
	AppID              string `json:"app_id" required:"true" help:"App ID of a Feishu custom application."`
	AppSecret          string `json:"app_secret" required:"true" secret:"true" help:"App Secret of the Feishu custom application."`
	RefreshToken       string `json:"refresh_token" secret:"true" help:"OAuth refresh token with drive:drive and offline_access. It is rotated and saved automatically."`
	AuthorizationCode  string `json:"authorization_code" secret:"true" help:"Optional one-time OAuth authorization code. When set, it is exchanged for tokens and then cleared automatically."`
	RedirectURI        string `json:"redirect_uri" default:"http://127.0.0.1:53682/callback" help:"OAuth redirect URI used to obtain the authorization code. It must exactly match the URI registered in Feishu."`
	OAuthTokenURL      string `json:"oauth_token_url" required:"true" default:"https://accounts.feishu.cn/oauth/v3/token" help:"Use https://accounts.larksuite.com/oauth/v3/token for Lark."`
	APIBase            string `json:"api_base" required:"true" default:"https://open.feishu.cn/open-apis" help:"Use https://open.larksuite.com/open-apis for Lark."`
	OrderBy            string `json:"order_by" type:"select" options:"EditedTime,CreatedTime" default:"EditedTime"`
	OrderDirection     string `json:"order_direction" type:"select" options:"ASC,DESC" default:"ASC"`
	OnlineDocumentMode string `json:"online_document_mode" type:"select" options:"export,hide" default:"export" help:"Export supported online documents when downloaded, or hide them."`
	ExportTimeout      int    `json:"export_timeout" type:"number" default:"30" help:"Maximum seconds to wait for an online document export."`
}

var config = driver.Config{
	Name:        "FeishuDrive",
	OnlyProxy:   true,
	CheckStatus: true,
	Alert:       "warning|User OAuth requires drive:drive and offline_access. The Drive list API does not expose ordinary file sizes, so they are displayed as 0 bytes.",
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &Feishu{}
	})
}
