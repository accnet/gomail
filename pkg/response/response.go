package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, data)
}

func Created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, data)
}

func Error(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, ErrorBody{Code: code, Message: message})
}

func BadRequest(c *gin.Context, message string) {
	Error(c, http.StatusBadRequest, "bad_request", message)
}

func ServerError(c *gin.Context, message string) {
	Error(c, http.StatusInternalServerError, "server_error", message)
}

func NotFound(c *gin.Context, message string) {
	Error(c, http.StatusNotFound, "not_found", message)
}

func Unauthorized(c *gin.Context, message string) {
	Error(c, http.StatusUnauthorized, "unauthorized", message)
}

func Forbidden(c *gin.Context, message string) {
	Error(c, http.StatusForbidden, "forbidden", message)
}