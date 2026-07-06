package parser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

type HTTPOCRClient struct {
	Endpoint string
	Token    string
	Client   *http.Client
}

func NewHTTPOCRClient(endpoint, token string, timeout time.Duration) *HTTPOCRClient {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &HTTPOCRClient{Endpoint: strings.TrimSpace(endpoint), Token: strings.TrimSpace(token), Client: &http.Client{Timeout: timeout}}
}

func (c *HTTPOCRClient) Recognize(ctx context.Context, filename string, reader io.Reader) ([]DocumentBlock, error) {
	if c == nil || strings.TrimSpace(c.Endpoint) == "" {
		return nil, fmt.Errorf("ocr endpoint is required")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fileWriter, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(fileWriter, reader); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, &body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	if c.Token != "" {
		request.Header.Set("Authorization", "Bearer "+c.Token)
	}
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("ocr request failed: status=%d body=%s", response.StatusCode, strings.TrimSpace(string(data)))
	}
	blocks, err := decodeOCRResponse(data)
	if err != nil {
		return nil, err
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("ocr response contains no text blocks")
	}
	return blocks, nil
}

type ocrResponse struct {
	Text   string           `json:"text"`
	Blocks []ocrBlock       `json:"blocks"`
	Data   *ocrResponseData `json:"data"`
}

type ocrResponseData struct {
	Text   string     `json:"text"`
	Blocks []ocrBlock `json:"blocks"`
}

type ocrBlock struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Text     string         `json:"text"`
	PageNo   int            `json:"page_no"`
	BBox     *BBox          `json:"bbox"`
	Metadata map[string]any `json:"metadata"`
}

func decodeOCRResponse(data []byte) ([]DocumentBlock, error) {
	var resp ocrResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	blocks := resp.Blocks
	text := resp.Text
	if resp.Data != nil {
		if len(blocks) == 0 {
			blocks = resp.Data.Blocks
		}
		if text == "" {
			text = resp.Data.Text
		}
	}
	if len(blocks) == 0 && strings.TrimSpace(text) != "" {
		blocks = []ocrBlock{{Type: "ocr_text", Text: text, PageNo: 1}}
	}
	out := make([]DocumentBlock, 0, len(blocks))
	for i, block := range blocks {
		text := strings.TrimSpace(block.Text)
		if text == "" {
			continue
		}
		pageNo := block.PageNo
		if pageNo <= 0 {
			pageNo = 1
		}
		blockType := strings.TrimSpace(block.Type)
		if blockType == "" {
			blockType = "ocr_text"
		}
		id := strings.TrimSpace(block.ID)
		if id == "" {
			id = fmt.Sprintf("ocr%d", i+1)
		}
		page := pageNo
		out = append(out, DocumentBlock{ID: id, Type: blockType, Text: text, PageNo: &page, BBox: block.BBox, Metadata: block.Metadata})
	}
	return out, nil
}
