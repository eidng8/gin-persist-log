package server

import (
	"bytes"

	"github.com/gin-gonic/gin"
)

type responseLogWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (w responseLogWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w responseLogWriter) WriteString(s string) (int, error) {
	w.body.WriteString(s)
	return w.ResponseWriter.WriteString(s)
}
