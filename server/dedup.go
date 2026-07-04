package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

func makeEventID(serverID, postID, recipientUserID, notificationType string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s:%s", serverID, postID, recipientUserID, notificationType)))
	return hex.EncodeToString(sum[:])
}
