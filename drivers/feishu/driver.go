package feishu

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

type Feishu struct {
	model.Storage
	Addition

	tokenMu        sync.Mutex
	accessToken    string
	tokenExpiresAt time.Time
	saveStorage    func()
}

func (d *Feishu) Config() driver.Config {
	return config
}

func (d *Feishu) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *Feishu) GetRootId() string {
	return d.RootFolderID
}

func (d *Feishu) Init(ctx context.Context) error {
	if d.AppID == "" || d.AppSecret == "" {
		return fmt.Errorf("app_id and app_secret are required")
	}
	apiBase, err := validateAPIBase(d.APIBase)
	if err != nil {
		return err
	}
	d.APIBase = apiBase
	if d.OAuthTokenURL == "" {
		if strings.Contains(apiBase, "larksuite.com") {
			d.OAuthTokenURL = "https://accounts.larksuite.com/oauth/v3/token"
		} else {
			d.OAuthTokenURL = "https://accounts.feishu.cn/oauth/v3/token"
		}
	}
	oauthTokenURL, err := validateAbsoluteURL(d.OAuthTokenURL, "OAuth token URL")
	if err != nil {
		return err
	}
	d.OAuthTokenURL = oauthTokenURL
	if d.ExportTimeout <= 0 {
		d.ExportTimeout = 30
	}
	// The Drive list API only accepts CreatedTime and EditedTime. Older
	// versions of this driver incorrectly exposed Name, so migrate it too.
	if d.OrderBy == "" || d.OrderBy == "Name" {
		d.OrderBy = "EditedTime"
	}
	if d.OrderBy != "EditedTime" && d.OrderBy != "CreatedTime" {
		return fmt.Errorf("order_by must be EditedTime or CreatedTime")
	}
	if d.OrderDirection == "" {
		d.OrderDirection = "ASC"
	}
	if d.OnlineDocumentMode == "" {
		d.OnlineDocumentMode = "export"
	}
	if d.OnlineDocumentMode != "export" && d.OnlineDocumentMode != "hide" {
		return fmt.Errorf("online_document_mode must be export or hide")
	}
	if d.AuthorizationCode != "" {
		if d.RedirectURI == "" {
			return fmt.Errorf("redirect_uri is required when authorization_code is set")
		}
		if _, err := d.exchangeAuthorizationCode(ctx); err != nil {
			return err
		}
	} else if d.RefreshToken == "" {
		return fmt.Errorf("refresh_token or authorization_code is required")
	} else if _, err := d.getAccessToken(ctx, true); err != nil {
		return err
	}
	if d.RootFolderID == "" {
		if err := d.requestJSON(ctx, http.MethodGet, "/drive/explorer/v2/root_folder/meta", nil, nil, nil); err != nil {
			return fmt.Errorf("check personal Drive root access: %w", err)
		}
	} else {
		if _, err := d.listPage(ctx, d.RootFolderID, "", 1); err != nil {
			return fmt.Errorf("check root folder access: %w", err)
		}
	}
	return nil
}

func (d *Feishu) persistStorage() {
	if d.saveStorage != nil {
		d.saveStorage()
		return
	}
	op.MustSaveDriverStorage(d)
}

func (d *Feishu) Drop(context.Context) error {
	d.invalidateToken()
	return nil
}

func (d *Feishu) List(ctx context.Context, dir model.Obj, _ model.ListArgs) ([]model.Obj, error) {
	folderToken := tokenOf(dir)
	pageToken := ""
	objects := make([]model.Obj, 0)
	for {
		page, err := d.listPage(ctx, folderToken, pageToken, 200)
		if err != nil {
			return nil, err
		}
		for _, item := range page.Data.Files {
			if d.shouldInclude(item.Type) {
				objects = append(objects, itemToObj(item))
			}
		}
		if !page.Data.HasMore || page.Data.NextPageToken == "" {
			break
		}
		pageToken = page.Data.NextPageToken
	}
	return objects, nil
}

func (d *Feishu) Link(ctx context.Context, file model.Obj, _ model.LinkArgs) (*model.Link, error) {
	kind := kindOf(file)
	token := tokenOf(file)

	endpoint := "/drive/v1/files/" + url.PathEscape(token) + "/download"
	expiration := 4 * time.Minute
	if kind != kindFile {
		if d.OnlineDocumentMode != "export" {
			return nil, errs.NotSupport
		}
		exportedToken, err := d.exportOnlineDocument(ctx, token, kind)
		if err != nil {
			return nil, err
		}
		endpoint = "/drive/v1/export_tasks/file/" + url.PathEscape(exportedToken) + "/download"
		expiration = time.Minute
	}
	// Export creation may refresh an invalid token, so obtain the token used by
	// the actual download only after the export has completed.
	accessToken, err := d.getAccessToken(ctx, false)
	if err != nil {
		return nil, err
	}
	return &model.Link{
		URL: d.endpoint(endpoint),
		Header: http.Header{
			"Authorization": []string{"Bearer " + accessToken},
		},
		Expiration: &expiration,
	}, nil
}

func (d *Feishu) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) (model.Obj, error) {
	if err := validateName(dirName); err != nil {
		return nil, err
	}
	var result createFolderResponse
	err := d.requestJSON(ctx, http.MethodPost, "/drive/v1/files/create_folder", nil, map[string]string{
		"name":         dirName,
		"folder_token": tokenOf(parentDir),
	}, &result)
	if err != nil {
		return nil, err
	}
	if result.Data.Token == "" {
		return nil, fmt.Errorf("Feishu created a folder without returning its token")
	}
	return &Object{
		Object: model.Object{ID: encodeID(kindFolder, result.Data.Token), Name: dirName, IsFolder: true, Modified: time.Now()},
		Kind:   kindFolder,
		URL:    result.Data.URL,
	}, nil
}

func (d *Feishu) Move(ctx context.Context, srcObj, dstDir model.Obj) (model.Obj, error) {
	var result taskResponse
	err := d.requestJSON(ctx, http.MethodPost, "/drive/v1/files/"+url.PathEscape(tokenOf(srcObj))+"/move", nil, map[string]string{
		"type":         kindOf(srcObj),
		"folder_token": tokenOf(dstDir),
	}, &result)
	if err != nil {
		return nil, err
	}
	if err := d.waitTask(ctx, result.Data.TaskID); err != nil {
		return nil, err
	}
	return srcObj, nil
}

func (d *Feishu) Copy(ctx context.Context, srcObj, dstDir model.Obj) (model.Obj, error) {
	if srcObj.IsDir() {
		return nil, errs.NotSupport
	}
	var result copyResponse
	err := d.requestJSON(ctx, http.MethodPost, "/drive/v1/files/"+url.PathEscape(tokenOf(srcObj))+"/copy", nil, map[string]string{
		"name":         srcObj.GetName(),
		"type":         kindOf(srcObj),
		"folder_token": tokenOf(dstDir),
	}, &result)
	if err != nil {
		return nil, err
	}
	if result.Data.File == nil {
		return nil, fmt.Errorf("Feishu copy succeeded without returning the copied file")
	}
	item := *result.Data.File
	if item.Type == "" {
		item.Type = kindOf(srcObj)
	}
	if item.Name == "" {
		item.Name = srcObj.GetName()
	}
	return itemToObj(item), nil
}

func (d *Feishu) Remove(ctx context.Context, obj model.Obj) error {
	query := map[string]string{
		"type":  kindOf(obj),
		"async": "false",
	}
	var result taskResponse
	if err := d.requestJSON(ctx, http.MethodDelete, "/drive/v1/files/"+url.PathEscape(tokenOf(obj)), query, nil, &result); err != nil {
		return err
	}
	return d.waitTask(ctx, result.Data.TaskID)
}

func (d *Feishu) listPage(ctx context.Context, folderToken, pageToken string, pageSize int) (*listResponse, error) {
	query := map[string]string{"page_size": strconv.Itoa(pageSize)}
	if folderToken != "" {
		query["folder_token"] = folderToken
	}
	if pageToken != "" {
		query["page_token"] = pageToken
	}
	if d.OrderBy != "" {
		query["order_by"] = d.OrderBy
		query["direction"] = d.OrderDirection
	}
	var result listResponse
	if err := d.requestJSON(ctx, http.MethodGet, "/drive/v1/files", query, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (d *Feishu) shouldInclude(kind string) bool {
	if kind == kindFolder || kind == kindFile {
		return true
	}
	if d.OnlineDocumentMode != "export" {
		return false
	}
	_, supported := exportExtension(kind)
	return supported
}

func (d *Feishu) waitTask(ctx context.Context, taskID string) error {
	if taskID == "" {
		return nil
	}
	taskCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()
	for {
		var result taskResponse
		err := d.requestJSON(taskCtx, http.MethodGet, "/drive/v1/files/task_check", map[string]string{"task_id": taskID}, nil, &result)
		if err != nil {
			return err
		}
		switch result.Data.Status {
		case "success":
			return nil
		case "fail":
			return fmt.Errorf("Feishu asynchronous task %s failed", taskID)
		}
		select {
		case <-taskCtx.Done():
			return fmt.Errorf("wait for Feishu asynchronous task %s: %w", taskID, taskCtx.Err())
		case <-ticker.C:
		}
	}
}

func (d *Feishu) exportOnlineDocument(ctx context.Context, token, kind string) (string, error) {
	extension, ok := exportExtension(kind)
	if !ok {
		return "", fmt.Errorf("Feishu online document type %q cannot be exported", kind)
	}
	var created exportCreateResponse
	if err := d.requestJSON(ctx, http.MethodPost, "/drive/v1/export_tasks", nil, map[string]string{
		"file_extension": extension,
		"token":          token,
		"type":           kind,
	}, &created); err != nil {
		return "", err
	}
	if created.Data.Ticket == "" {
		return "", fmt.Errorf("Feishu did not return an export ticket")
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(d.ExportTimeout)*time.Second)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		var result exportGetResponse
		endpoint := "/drive/v1/export_tasks/" + url.PathEscape(created.Data.Ticket)
		if err := d.requestJSON(timeoutCtx, http.MethodGet, endpoint, map[string]string{"token": token}, nil, &result); err != nil {
			return "", err
		}
		if result.Data.Result != nil {
			if result.Data.Result.JobStatus == 0 && result.Data.Result.FileToken != "" {
				return result.Data.Result.FileToken, nil
			}
			if result.Data.Result.JobErrorMsg != "" && result.Data.Result.JobErrorMsg != "success" {
				return "", fmt.Errorf("Feishu export failed: %s", result.Data.Result.JobErrorMsg)
			}
		}
		select {
		case <-timeoutCtx.Done():
			return "", fmt.Errorf("wait for Feishu export: %w", timeoutCtx.Err())
		case <-ticker.C:
		}
	}
}

var _ driver.Driver = (*Feishu)(nil)
var _ driver.IRootId = (*Feishu)(nil)
var _ driver.MkdirResult = (*Feishu)(nil)
var _ driver.MoveResult = (*Feishu)(nil)
var _ driver.CopyResult = (*Feishu)(nil)
var _ driver.Remove = (*Feishu)(nil)
