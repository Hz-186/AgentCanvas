package sse

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

type Writer struct {
	c *gin.Context
}

func NewWriter(c *gin.Context) *Writer {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(http.StatusOK)
	return &Writer{c: c}
}

func (w *Writer) Event(event string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w.c.Writer, "event: %s\ndata: %s\n\n", event, payload); err != nil {
		return err
	}
	w.c.Writer.Flush()
	return nil
}
