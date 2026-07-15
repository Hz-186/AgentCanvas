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
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	c.Writer.Flush()
	return &Writer{c: c}
}

func (w *Writer) Event(event string, data any) error {
	return w.EventWithID(0, event, data)
}

func (w *Writer) EventWithID(id int64, event string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	prefix := ""
	if id > 0 {
		prefix = fmt.Sprintf("id: %d\n", id)
	}
	if _, err := fmt.Fprintf(w.c.Writer, "%sevent: %s\ndata: %s\n\n", prefix, event, payload); err != nil {
		return err
	}
	w.c.Writer.Flush()
	return nil
}
