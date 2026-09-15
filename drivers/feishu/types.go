package feishu

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

const (
	kindFile   = "file"
	kindFolder = "folder"
)

type apiStatus struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

type oauthTokenResponse struct {
	apiStatus
	Error                 string `json:"error"`
	ErrorDescription      string `json:"error_description"`
	AccessToken           string `json:"access_token"`
	TokenType             string `json:"token_type"`
	ExpiresIn             int    `json:"expires_in"`
	RefreshToken          string `json:"refresh_token"`
	RefreshTokenExpiresIn int    `json:"refresh_token_expires_in"`
	Scope                 string `json:"scope"`
}

type fileItem struct {
	Token        string `json:"token"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	ParentToken  string `json:"parent_token"`
	URL          string `json:"url"`
	CreatedTime  string `json:"created_time"`
	ModifiedTime string `json:"modified_time"`
}

type listResponse struct {
	apiStatus
	Data struct {
		Files         []fileItem `json:"files"`
		NextPageToken string     `json:"next_page_token"`
		HasMore       bool       `json:"has_more"`
	} `json:"data"`
}

type createFolderResponse struct {
	apiStatus
	Data struct {
		Token string `json:"token"`
		URL   string `json:"url"`
	} `json:"data"`
}

type taskResponse struct {
	apiStatus
	Data struct {
		TaskID string `json:"task_id"`
		Status string `json:"status"`
	} `json:"data"`
}

type uploadResponse struct {
	apiStatus
	Data struct {
		FileToken string `json:"file_token"`
		URL       string `json:"url"`
		Version   string `json:"version"`
	} `json:"data"`
}

type uploadPrepareResponse struct {
	apiStatus
	Data struct {
		UploadID  string `json:"upload_id"`
		BlockSize int    `json:"block_size"`
		BlockNum  int    `json:"block_num"`
	} `json:"data"`
}

type copyResponse struct {
	apiStatus
	Data struct {
		File *fileItem `json:"file"`
	} `json:"data"`
}

type exportCreateResponse struct {
	apiStatus
	Data struct {
		Ticket string `json:"ticket"`
	} `json:"data"`
}

type exportGetResponse struct {
	apiStatus
	Data struct {
		Result *struct {
			FileToken   string `json:"file_token"`
			FileSize    int64  `json:"file_size"`
			JobStatus   int    `json:"job_status"`
			JobErrorMsg string `json:"job_error_msg"`
		} `json:"result"`
	} `json:"data"`
}

type Object struct {
	model.Object
	Kind string
	URL  string
}

func encodeID(kind, token string) string {
	return kind + ":" + token
}

func decodeID(id string) (kind, token string) {
	kind, token, ok := strings.Cut(id, ":")
	if !ok {
		return kindFolder, id
	}
	return kind, token
}

func kindOf(obj model.Obj) string {
	if typed, ok := obj.(*Object); ok && typed.Kind != "" {
		return typed.Kind
	}
	kind, _ := decodeID(obj.GetID())
	if obj.IsDir() {
		return kindFolder
	}
	return kind
}

func tokenOf(obj model.Obj) string {
	_, token := decodeID(obj.GetID())
	return token
}

func itemToObj(item fileItem) *Object {
	return &Object{
		Object: model.Object{
			ID:       encodeID(item.Type, item.Token),
			Name:     item.Name,
			Modified: parseTimestamp(item.ModifiedTime),
			Ctime:    parseTimestamp(item.CreatedTime),
			IsFolder: item.Type == kindFolder,
		},
		Kind: item.Type,
		URL:  item.URL,
	}
}

func parseTimestamp(value string) time.Time {
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds <= 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0)
}

func exportExtension(kind string) (string, bool) {
	switch kind {
	case "doc", "docx":
		return "docx", true
	case "sheet", "bitable":
		return "xlsx", true
	case "slides":
		return "pptx", true
	default:
		return "", false
	}
}

func validateName(name string) error {
	if len(name) == 0 || len(name) > 256 {
		return fmt.Errorf("Feishu names must contain 1 to 256 bytes")
	}
	return nil
}
