package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/go-resty/resty/v2"
)

func (d *Feishu) getAccessToken(ctx context.Context, force bool) (string, error) {
	d.tokenMu.Lock()
	defer d.tokenMu.Unlock()

	if !force && d.accessToken != "" && time.Until(d.tokenExpiresAt) > 5*time.Minute {
		return d.accessToken, nil
	}

	if d.RefreshToken == "" {
		return "", fmt.Errorf("Feishu OAuth refresh_token is empty; authorize the user again")
	}
	result, err := d.exchangeOAuthToken(ctx, map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": d.RefreshToken,
	})
	if err != nil {
		return "", fmt.Errorf("refresh Feishu user access token: %w", err)
	}
	d.applyOAuthToken(result, false)
	return d.accessToken, nil
}

func (d *Feishu) exchangeAuthorizationCode(ctx context.Context) (string, error) {
	d.tokenMu.Lock()
	defer d.tokenMu.Unlock()

	result, err := d.exchangeOAuthToken(ctx, map[string]string{
		"grant_type":   "authorization_code",
		"code":         d.AuthorizationCode,
		"redirect_uri": d.RedirectURI,
	})
	if err != nil {
		return "", fmt.Errorf("exchange Feishu OAuth authorization code: %w", err)
	}
	if result.RefreshToken == "" {
		return "", fmt.Errorf("exchange Feishu OAuth authorization code: no refresh_token returned; authorize with offline_access")
	}
	d.applyOAuthToken(result, true)
	return d.accessToken, nil
}

func (d *Feishu) exchangeOAuthToken(ctx context.Context, grant map[string]string) (oauthTokenResponse, error) {
	body := map[string]string{
		"client_id":     d.AppID,
		"client_secret": d.AppSecret,
	}
	for key, value := range grant {
		body[key] = value
	}

	var result oauthTokenResponse
	res, err := base.RestyClient.R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json; charset=utf-8").
		SetBody(body).
		Post(d.OAuthTokenURL)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(res.Body(), &result); err != nil {
		return result, fmt.Errorf("decode OAuth token response (HTTP %d): %w", res.StatusCode(), err)
	}
	if res.IsError() || result.Code != 0 || result.Error != "" || result.AccessToken == "" {
		return result, oauthResponseError(res, result)
	}
	return result, nil
}

func (d *Feishu) applyOAuthToken(result oauthTokenResponse, clearAuthorizationCode bool) {
	if result.ExpiresIn <= 0 {
		result.ExpiresIn = 7200
	}
	d.accessToken = result.AccessToken
	d.tokenExpiresAt = time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)

	shouldPersist := clearAuthorizationCode
	if result.RefreshToken != "" && result.RefreshToken != d.RefreshToken {
		d.RefreshToken = result.RefreshToken
		shouldPersist = true
	}
	if clearAuthorizationCode {
		d.AuthorizationCode = ""
	}
	if shouldPersist {
		d.persistStorage()
	}
}

func (d *Feishu) requestJSON(ctx context.Context, method, endpoint string, query map[string]string, body any, result any) error {
	for attempt := 0; attempt < 2; attempt++ {
		token, err := d.getAccessToken(ctx, attempt > 0)
		if err != nil {
			return err
		}
		req := base.RestyClient.R().
			SetContext(ctx).
			SetHeader("Authorization", "Bearer "+token).
			SetHeader("Content-Type", "application/json; charset=utf-8")
		if len(query) > 0 {
			req.SetQueryParams(query)
		}
		if body != nil {
			req.SetBody(body)
		}
		res, err := req.Execute(method, d.endpoint(endpoint))
		if err != nil {
			return fmt.Errorf("Feishu API %s %s: %w", method, endpoint, err)
		}
		var status apiStatus
		if len(res.Body()) > 0 {
			if err := json.Unmarshal(res.Body(), &status); err != nil {
				return fmt.Errorf("decode Feishu API response (HTTP %d): %w", res.StatusCode(), err)
			}
		}
		if isAuthFailure(res.StatusCode(), status.Code) && attempt == 0 {
			d.invalidateToken()
			continue
		}
		if res.IsError() || status.Code != 0 {
			return apiResponseError(res, status)
		}
		if result != nil {
			if err := json.Unmarshal(res.Body(), result); err != nil {
				return fmt.Errorf("decode Feishu API response body: %w", err)
			}
		}
		return nil
	}
	return fmt.Errorf("Feishu authentication failed after refreshing the access token")
}

func (d *Feishu) multipartRequest(ctx context.Context, endpoint string, fields map[string]string, fileName, contentType string, reader anyReader, result any) error {
	token, err := d.getAccessToken(ctx, false)
	if err != nil {
		return err
	}
	req := base.RestyClient.R().
		SetContext(ctx).
		SetHeader("Authorization", "Bearer "+token).
		SetMultipartFormData(fields).
		SetMultipartField("file", fileName, contentType, reader)
	res, err := req.Post(d.endpoint(endpoint))
	if err != nil {
		return fmt.Errorf("Feishu upload %s: %w", endpoint, err)
	}
	var status apiStatus
	if err := json.Unmarshal(res.Body(), &status); err != nil {
		return fmt.Errorf("decode Feishu upload response (HTTP %d): %w", res.StatusCode(), err)
	}
	if res.IsError() || status.Code != 0 {
		return apiResponseError(res, status)
	}
	if result != nil {
		if err := json.Unmarshal(res.Body(), result); err != nil {
			return fmt.Errorf("decode Feishu upload response body: %w", err)
		}
	}
	return nil
}

type anyReader interface {
	Read([]byte) (int, error)
}

func (d *Feishu) endpoint(endpoint string) string {
	return strings.TrimRight(d.APIBase, "/") + "/" + strings.TrimLeft(endpoint, "/")
}

func (d *Feishu) invalidateToken() {
	d.tokenMu.Lock()
	d.accessToken = ""
	d.tokenExpiresAt = time.Time{}
	d.tokenMu.Unlock()
}

func isAuthFailure(httpStatus, code int) bool {
	if httpStatus == http.StatusUnauthorized {
		return true
	}
	switch code {
	case 99991661, 99991663, 99991664:
		return true
	default:
		return false
	}
}

func apiResponseError(res *resty.Response, status apiStatus) error {
	requestID := res.Header().Get("X-Tt-Logid")
	if requestID == "" {
		requestID = res.Header().Get("X-Request-Id")
	}
	message := status.Msg
	if message == "" {
		message = http.StatusText(res.StatusCode())
	}
	if requestID != "" {
		return fmt.Errorf("Feishu API error: HTTP %d, code %d, %s (request_id=%s)", res.StatusCode(), status.Code, message, requestID)
	}
	return fmt.Errorf("Feishu API error: HTTP %d, code %d, %s", res.StatusCode(), status.Code, message)
}

func validateAPIBase(raw string) (string, error) {
	return validateAbsoluteURL(raw, "Feishu API base URL")
}

func validateAbsoluteURL(raw, label string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid %s %q", label, raw)
	}
	return raw, nil
}

func oauthResponseError(res *resty.Response, result oauthTokenResponse) error {
	message := result.ErrorDescription
	if message == "" {
		message = result.Msg
	}
	if message == "" {
		message = result.Error
	}
	if message == "" {
		message = http.StatusText(res.StatusCode())
	}
	return fmt.Errorf("Feishu OAuth error: HTTP %d, code %d, %s", res.StatusCode(), result.Code, message)
}
