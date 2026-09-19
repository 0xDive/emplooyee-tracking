package auth

import (
    "net/http"
    "strings"

    "github.com/getsentry/sentry-go"
    sentrygin "github.com/getsentry/sentry-go/gin"
    "github.com/gin-gonic/gin"
)

const contextUserID = "user_id"
const contextTokenVersion = "token_version"
const contextSessionID = "session_id"

func (m *Manager) Required() gin.HandlerFunc {
    return func(c *gin.Context) {
        header := c.GetHeader("Authorization")
        token, ok := strings.CutPrefix(header, "Bearer ")
        if !ok || token == "" {
            c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token", "code": "authentication_required"})
            return
        }
        userID, version, sessionID, err := m.ParseAccessSession(token)
        if err != nil {
            c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token", "code": "authentication_required"})
            return
        }
        c.Set(contextUserID, userID)
        c.Set(contextTokenVersion, version)
        c.Set(contextSessionID, sessionID)
        if hub := sentrygin.GetHubFromContext(c); hub != nil { hub.Scope().SetUser(sentry.User{ID: userID}) }
        c.Next()
    }
}

func UserID(c *gin.Context) (string, bool) {
    v, ok := c.Get(contextUserID)
    if !ok { return "", false }
    id, ok := v.(string)
    return id, ok
}

func TokenVersion(c *gin.Context) (int, bool) {
    v, ok := c.Get(contextTokenVersion)
    if !ok { return 0, false }
    n, ok := v.(int)
    return n, ok
}

func SessionID(c *gin.Context) (string, bool) {
    v, ok := c.Get(contextSessionID)
    if !ok { return "", false }
    id, ok := v.(string)
    return id, ok
}
