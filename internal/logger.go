package internal

import (
	"bytes"

	"github.com/gin-gonic/gin"
)

type ResponseLogWriter struct {
	gin.ResponseWriter
	Body *bytes.Buffer
}

func (w ResponseLogWriter) Write(b []byte) (int, error) {
	w.Body.Write(b)
	return w.ResponseWriter.Write(b)
}
