package feishu

import (
	"bytes"
	"context"
	"fmt"
	"hash/adler32"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

const simpleUploadLimit int64 = 20 * 1024 * 1024

func (d *Feishu) Put(ctx context.Context, dstDir model.Obj, stream model.FileStreamer, up driver.UpdateProgress) (model.Obj, error) {
	if up == nil {
		up = func(float64) {}
	}
	if err := validateName(stream.GetName()); err != nil {
		return nil, err
	}
	if stream.GetSize() < 0 || uint64(stream.GetSize()) > uint64(maxInt()) {
		return nil, fmt.Errorf("file size %d cannot be represented by the Feishu API on this platform", stream.GetSize())
	}
	var (
		token string
		url   string
		err   error
	)
	if stream.GetSize() <= simpleUploadLimit {
		token, url, err = d.uploadAll(ctx, dstDir, stream, up)
	} else {
		token, url, err = d.uploadParts(ctx, dstDir, stream, up)
	}
	if err != nil {
		return nil, err
	}
	if token == "" {
		return nil, fmt.Errorf("Feishu upload succeeded without returning a file token")
	}
	return &Object{
		Object: model.Object{
			ID:       encodeID(kindFile, token),
			Name:     stream.GetName(),
			Size:     stream.GetSize(),
			Modified: time.Now(),
		},
		Kind: kindFile,
		URL:  url,
	}, nil
}

func (d *Feishu) uploadAll(ctx context.Context, dstDir model.Obj, stream model.FileStreamer, up driver.UpdateProgress) (string, string, error) {
	fields := map[string]string{
		"file_name":   stream.GetName(),
		"parent_type": "explorer",
		"parent_node": tokenOf(dstDir),
		"size":        strconv.FormatInt(stream.GetSize(), 10),
	}
	if existing := stream.GetExist(); existing != nil {
		fields["file_token"] = tokenOf(existing)
	}
	reader := io.TeeReader(driver.NewLimitedUploadStream(ctx, stream), driver.NewProgress(stream.GetSize(), up))
	var result uploadResponse
	if err := d.multipartRequest(ctx, "/drive/v1/files/upload_all", fields, stream.GetName(), contentType(stream), reader, &result); err != nil {
		return "", "", err
	}
	up(100)
	return result.Data.FileToken, result.Data.URL, nil
}

func (d *Feishu) uploadParts(ctx context.Context, dstDir model.Obj, stream model.FileStreamer, up driver.UpdateProgress) (string, string, error) {
	info := map[string]any{
		"file_name":   stream.GetName(),
		"parent_type": "explorer",
		"parent_node": tokenOf(dstDir),
		"size":        stream.GetSize(),
	}
	if existing := stream.GetExist(); existing != nil {
		info["file_token"] = tokenOf(existing)
	}
	var prepared uploadPrepareResponse
	if err := d.requestJSON(ctx, http.MethodPost, "/drive/v1/files/upload_prepare", nil, info, &prepared); err != nil {
		return "", "", err
	}
	if prepared.Data.UploadID == "" || prepared.Data.BlockSize <= 0 || prepared.Data.BlockNum <= 0 {
		return "", "", fmt.Errorf("Feishu returned an invalid multipart upload strategy")
	}
	expectedBlocks := int((stream.GetSize() + int64(prepared.Data.BlockSize) - 1) / int64(prepared.Data.BlockSize))
	if prepared.Data.BlockNum != expectedBlocks {
		return "", "", fmt.Errorf("Feishu returned %d upload blocks, expected %d", prepared.Data.BlockNum, expectedBlocks)
	}

	buffer := make([]byte, prepared.Data.BlockSize)
	var uploaded int64
	for seq := 0; seq < prepared.Data.BlockNum; seq++ {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		remaining := stream.GetSize() - uploaded
		want := int64(prepared.Data.BlockSize)
		if remaining < want {
			want = remaining
		}
		read, err := io.ReadFull(stream, buffer[:int(want)])
		if err != nil && err != io.ErrUnexpectedEOF {
			return "", "", fmt.Errorf("read upload part %d: %w", seq, err)
		}
		if int64(read) != want {
			return "", "", fmt.Errorf("upload stream ended early: expected %d bytes, read %d", stream.GetSize(), uploaded+int64(read))
		}
		part := buffer[:read]
		fields := map[string]string{
			"upload_id": prepared.Data.UploadID,
			"seq":       strconv.Itoa(seq),
			"size":      strconv.Itoa(read),
			"checksum":  strconv.FormatUint(uint64(adler32.Checksum(part)), 10),
		}
		limited := driver.NewLimitedUploadStream(ctx, bytes.NewReader(part))
		progress := &partProgress{base: uploaded, total: stream.GetSize(), update: up}
		reader := io.TeeReader(limited, progress)
		var result apiStatus
		if err := d.multipartRequest(ctx, "/drive/v1/files/upload_part", fields, stream.GetName(), "application/octet-stream", reader, &result); err != nil {
			return "", "", fmt.Errorf("upload Feishu part %d: %w", seq, err)
		}
		uploaded += int64(read)
		up(float64(uploaded) / float64(stream.GetSize()) * 100)
	}
	if uploaded != stream.GetSize() {
		return "", "", fmt.Errorf("multipart upload size mismatch: expected %d bytes, uploaded %d", stream.GetSize(), uploaded)
	}

	var finished uploadResponse
	if err := d.requestJSON(ctx, http.MethodPost, "/drive/v1/files/upload_finish", nil, map[string]any{
		"upload_id": prepared.Data.UploadID,
		"block_num": prepared.Data.BlockNum,
	}, &finished); err != nil {
		return "", "", err
	}
	up(100)
	return finished.Data.FileToken, finished.Data.URL, nil
}

type partProgress struct {
	base   int64
	done   int64
	total  int64
	update driver.UpdateProgress
}

func (p *partProgress) Write(data []byte) (int, error) {
	p.done += int64(len(data))
	if p.total > 0 {
		p.update(float64(p.base+p.done) / float64(p.total) * 100)
	}
	return len(data), nil
}

func contentType(stream model.FileStreamer) string {
	if stream.GetMimetype() != "" {
		return stream.GetMimetype()
	}
	return "application/octet-stream"
}

func maxInt() int {
	return int(^uint(0) >> 1)
}

var _ driver.PutResult = (*Feishu)(nil)
