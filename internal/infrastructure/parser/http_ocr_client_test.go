package parser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPOCRClientRecognizesBlocks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("Authorization = %q", got)
		}
		if err := r.ParseMultipartForm(1024 * 1024); err != nil {
			t.Fatalf("ParseMultipartForm() error = %v", err)
		}
		if _, _, err := r.FormFile("file"); err != nil {
			t.Fatalf("missing file: %v", err)
		}
		_, _ = w.Write([]byte(`{"blocks":[{"id":"b1","type":"ocr_text","text":"hello scan","page_no":2,"bbox":{"x":1,"y":2,"width":3,"height":4},"metadata":{"confidence":0.98}}]}`))
	}))
	defer server.Close()

	client := NewHTTPOCRClient(server.URL, "token", time.Second)
	blocks, err := client.Recognize(context.Background(), "scan.png", strings.NewReader("image-bytes"))
	if err != nil {
		t.Fatalf("Recognize() error = %v", err)
	}
	if len(blocks) != 1 || blocks[0].Text != "hello scan" || blocks[0].PageNo == nil || *blocks[0].PageNo != 2 || blocks[0].BBox == nil {
		t.Fatalf("blocks = %+v", blocks)
	}
}

func TestHTTPOCRClientAcceptsTextFallbackResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"text":"plain OCR text"}}`))
	}))
	defer server.Close()

	client := NewHTTPOCRClient(server.URL, "", time.Second)
	blocks, err := client.Recognize(context.Background(), "scan.png", strings.NewReader("image-bytes"))
	if err != nil {
		t.Fatalf("Recognize() error = %v", err)
	}
	if len(blocks) != 1 || blocks[0].Text != "plain OCR text" || blocks[0].Type != "ocr_text" {
		t.Fatalf("blocks = %+v", blocks)
	}
}
