package server

import (
    "net/http"

    "actilens/backend/internal/auth"
    "actilens/backend/internal/store"

    "github.com/gin-gonic/gin"
)

// accountGuard makes archive/password-reset token revocation immediate for protected APIs.
func accountGuard(st *store.Store) gin.HandlerFunc {
    return func(c *gin.Context) {
        userID, ok := auth.UserID(c)
        tokenVersion, vok := auth.TokenVersion(c)
        sessionID, _ := auth.SessionID(c)
        if !ok || !vok {
            c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token", "code": "authentication_required"})
            return
        }
        active, currentVersion, err := st.UserSecurity(c.Request.Context(), userID)
        if err != nil || !active || currentVersion != tokenVersion {
            c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "session revoked", "code": "session_revoked"})
            return
        }
        sessionActive, err := st.SessionActive(c.Request.Context(), userID, sessionID)
        if err != nil || !sessionActive {
            c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "session revoked", "code": "session_revoked"})
            return
        }
        c.Next()
    }
}
