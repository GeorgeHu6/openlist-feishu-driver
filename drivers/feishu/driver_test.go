package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"hash/adler32"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	streamPkg "github.com/OpenListTeam/OpenList/v4/internal/stream"
	"github.com/go-resty/resty/v2"
)

func TestMain(m *testing.M) {
	base.RestyClient = resty.New()
	os.Exit(m.Run())
}

func TestListPaginatesAndMapsObjects(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("unexpected authorization header: %q", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/drive/v1/files" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{
					"files":    []map[string]string{{"token": "fld1", "name": "folder", "type": "folder", "created_time": "1700000000"}},
					"has_more": true, "next_page_token": "next",
				},
			})
			return
		}
		if r.URL.Query().Get("page_token") != "next" {
			t.Fatalf("missing page token: %s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{
				"files": []map[string]string{
					{"token": "box1", "name": "file.bin", "type": "file", "modified_time": "1700000001"},
					{"token": "doc1", "name": "document", "type": "docx"},
					{"token": "mind1", "name": "mind", "type": "mindnote"},
				},
				"has_more": false,
			},
		})
	}))
	defer server.Close()

	driver := newTestDriver(server.URL)
	objects, err := driver.List(context.Background(), &model.Object{ID: "root-folder", IsFolder: true}, model.ListArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != 3 {
		t.Fatalf("expected 3 supported objects, got %d", len(objects))
	}
	if objects[0].GetID() != "folder:fld1" || !objects[0].IsDir() {
		t.Fatalf("unexpected folder object: %#v", objects[0])
	}
	if objects[1].GetID() != "file:box1" || objects[1].GetSize() != 0 {
		t.Fatalf("unexpected file object: %#v", objects[1])
	}
	if objects[2].GetID() != "docx:doc1" {
		t.Fatalf("unexpected document object: %#v", objects[2])
	}
}

func TestRequestRefreshesExpiredToken(t *testing.T) {
	t.Parallel()
	var listCalls atomic.Int32
	var saveCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/drive/v1/files":
			if listCalls.Add(1) == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"code":99991663,"msg":"token invalid"}`))
				return
			}
			if r.Header.Get("Authorization") != "Bearer renewed-token" {
				t.Fatalf("request did not use renewed token")
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"files":[],"has_more":false}}`))
		case "/oauth/v3/token":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["grant_type"] != "refresh_token" || body["refresh_token"] != "initial-refresh-token" || body["client_id"] != "app-id" || body["client_secret"] != "app-secret" {
				t.Fatalf("unexpected refresh request: %#v", body)
			}
			_, _ = w.Write([]byte(`{"access_token":"renewed-token","refresh_token":"rotated-refresh-token","expires_in":7200}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	driver := newTestDriver(server.URL)
	driver.saveStorage = func() { saveCalls.Add(1) }
	var result listResponse
	if err := driver.requestJSON(context.Background(), http.MethodGet, "/drive/v1/files", nil, nil, &result); err != nil {
		t.Fatal(err)
	}
	if listCalls.Load() != 2 {
		t.Fatalf("expected one retry, got %d calls", listCalls.Load())
	}
	if driver.RefreshToken != "rotated-refresh-token" || saveCalls.Load() != 1 {
		t.Fatalf("rotated refresh token was not persisted: token=%q saves=%d", driver.RefreshToken, saveCalls.Load())
	}
}

func TestAuthorizationCodeIsExchangedAndCleared(t *testing.T) {
	t.Parallel()
	var saveCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/v3/token" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["grant_type"] != "authorization_code" || body["code"] != "one-time-code" || body["redirect_uri"] != "http://127.0.0.1:53682/callback" {
			t.Fatalf("unexpected authorization code request: %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"user-token","refresh_token":"new-refresh-token","expires_in":3600}`))
	}))
	defer server.Close()

	driver := newTestDriver(server.URL)
	driver.accessToken = ""
	driver.tokenExpiresAt = time.Time{}
	driver.RefreshToken = ""
	driver.AuthorizationCode = "one-time-code"
	driver.RedirectURI = "http://127.0.0.1:53682/callback"
	driver.saveStorage = func() { saveCalls.Add(1) }

	token, err := driver.exchangeAuthorizationCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if token != "user-token" || driver.RefreshToken != "new-refresh-token" || driver.AuthorizationCode != "" {
		t.Fatalf("unexpected token state: access=%q refresh=%q code=%q", token, driver.RefreshToken, driver.AuthorizationCode)
	}
	if saveCalls.Load() != 1 {
		t.Fatalf("expected one storage save, got %d", saveCalls.Load())
	}
}

func TestAuthorizationCodeRequiresOfflineAccess(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"user-token","expires_in":3600}`))
	}))
	defer server.Close()

	driver := newTestDriver(server.URL)
	driver.AuthorizationCode = "one-time-code"
	driver.RedirectURI = "http://127.0.0.1:53682/callback"
	if _, err := driver.exchangeAuthorizationCode(context.Background()); err == nil {
		t.Fatal("expected missing refresh token to fail")
	}
}

func TestInitMigratesUnsupportedNameOrder(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/oauth/v3/token":
			_, _ = w.Write([]byte(`{"access_token":"user-token","refresh_token":"initial-refresh-token","expires_in":3600}`))
		case "/drive/v1/files":
			if r.URL.Query().Get("folder_token") != "fld-root" || r.URL.Query().Get("order_by") != "EditedTime" {
				t.Fatalf("unexpected list query: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"files":[],"has_more":false}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	driver := newTestDriver(server.URL)
	driver.RootFolderID = "fld-root"
	driver.OrderBy = "Name"
	if err := driver.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if driver.OrderBy != "EditedTime" {
		t.Fatalf("expected migrated order, got %q", driver.OrderBy)
	}
}

func TestOnlineDocumentLinkExportsFirst(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/drive/v1/export_tasks":
			_, _ = w.Write([]byte(`{"code":0,"data":{"ticket":"ticket1"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/drive/v1/export_tasks/ticket1":
			if r.URL.Query().Get("token") != "doc1" {
				t.Fatalf("unexpected token query: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"result":{"file_token":"export1","job_status":0}}}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	driver := newTestDriver(server.URL)
	link, err := driver.Link(context.Background(), itemToObj(fileItem{Token: "doc1", Name: "doc", Type: "docx"}), model.LinkArgs{})
	if err != nil {
		t.Fatal(err)
	}
	expected, _ := url.JoinPath(server.URL, "/drive/v1/export_tasks/file/export1/download")
	if link.URL != expected {
		t.Fatalf("unexpected link: %s", link.URL)
	}
	if link.Header.Get("Authorization") != "Bearer test-token" {
		t.Fatalf("missing link authorization header")
	}
}

func TestIDAndExportMappings(t *testing.T) {
	t.Parallel()
	if kind, token := decodeID("sheet:sht1"); kind != "sheet" || token != "sht1" {
		t.Fatalf("unexpected decoded ID: %q %q", kind, token)
	}
	if kind, token := decodeID("plain-root-token"); kind != kindFolder || token != "plain-root-token" {
		t.Fatalf("unexpected root ID: %q %q", kind, token)
	}
	if ext, ok := exportExtension("bitable"); !ok || ext != "xlsx" {
		t.Fatalf("unexpected export mapping: %q %v", ext, ok)
	}
}

func TestSmallUploadSendsExplorerMultipartForm(t *testing.T) {
	t.Parallel()
	content := []byte("hello from OpenList")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/drive/v1/files/upload_all" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		if r.FormValue("file_name") != "hello.txt" || r.FormValue("parent_type") != "explorer" || r.FormValue("parent_node") != "fld1" {
			t.Fatalf("unexpected upload fields: %#v", r.MultipartForm.Value)
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		got, err := io.ReadAll(file)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, content) {
			t.Fatalf("unexpected file content: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"file_token":"box1","url":"https://example.feishu.cn/file/box1"}}`))
	}))
	defer server.Close()

	driver := newTestDriver(server.URL)
	stream := newFileStream("hello.txt", "text/plain", content)
	var progress float64
	obj, err := driver.Put(context.Background(), &model.Object{ID: "fld1", IsFolder: true}, stream, func(value float64) { progress = value })
	if err != nil {
		t.Fatal(err)
	}
	if obj.GetID() != "file:box1" || obj.GetSize() != int64(len(content)) {
		t.Fatalf("unexpected uploaded object: %#v", obj)
	}
	if progress != 100 {
		t.Fatalf("expected completed progress, got %f", progress)
	}
}

func TestMultipartUploadUsesServerBlockStrategyAndChecksums(t *testing.T) {
	t.Parallel()
	blockSize := 4 * 1024 * 1024
	content := bytes.Repeat([]byte("x"), int(simpleUploadLimit)+1)
	expectedBlocks := (len(content) + blockSize - 1) / blockSize
	var partCount atomic.Int32
	var uploaded atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/drive/v1/files/upload_prepare":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["parent_type"] != "explorer" || body["parent_node"] != "fld1" {
				t.Fatalf("unexpected prepare body: %#v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"upload_id": "upload1", "block_size": blockSize, "block_num": expectedBlocks,
			}})
		case "/drive/v1/files/upload_part":
			if err := r.ParseMultipartForm(int64(blockSize + 1024)); err != nil {
				t.Fatal(err)
			}
			file, _, err := r.FormFile("file")
			if err != nil {
				t.Fatal(err)
			}
			part, err := io.ReadAll(file)
			_ = file.Close()
			if err != nil {
				t.Fatal(err)
			}
			if r.FormValue("upload_id") != "upload1" || r.FormValue("checksum") != strconv.FormatUint(uint64(adler32.Checksum(part)), 10) {
				t.Fatalf("unexpected part metadata: %#v", r.MultipartForm.Value)
			}
			partCount.Add(1)
			uploaded.Add(int64(len(part)))
			_, _ = w.Write([]byte(`{"code":0}`))
		case "/drive/v1/files/upload_finish":
			_, _ = w.Write([]byte(`{"code":0,"data":{"file_token":"box-large"}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	driver := newTestDriver(server.URL)
	obj, err := driver.Put(context.Background(), &model.Object{ID: "fld1", IsFolder: true}, newFileStream("large.bin", "application/octet-stream", content), nil)
	if err != nil {
		t.Fatal(err)
	}
	if obj.GetID() != "file:box-large" || partCount.Load() != int32(expectedBlocks) || uploaded.Load() != int64(len(content)) {
		t.Fatalf("unexpected multipart result: obj=%s parts=%d bytes=%d", obj.GetID(), partCount.Load(), uploaded.Load())
	}
}

func newFileStream(name, mimeType string, content []byte) *streamPkg.FileStream {
	return &streamPkg.FileStream{
		Ctx:      context.Background(),
		Obj:      &model.Object{Name: name, Size: int64(len(content))},
		Reader:   bytes.NewReader(content),
		Mimetype: mimeType,
	}
}

func newTestDriver(apiBase string) *Feishu {
	return &Feishu{
		Addition: Addition{
			AppID:              "app-id",
			AppSecret:          "app-secret",
			RefreshToken:       "initial-refresh-token",
			OAuthTokenURL:      apiBase + "/oauth/v3/token",
			APIBase:            apiBase,
			OnlineDocumentMode: "export",
			ExportTimeout:      2,
		},
		accessToken:    "test-token",
		tokenExpiresAt: time.Now().Add(time.Hour),
	}
}
