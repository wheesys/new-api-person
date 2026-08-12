package controller

import (
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/service/smartrouting"
	"github.com/gin-gonic/gin"
)

// channelHealthItem is the wire shape of one channel/model health snapshot.
type channelHealthItem struct {
	ChannelID           int     `json:"channel_id"`
	ModelName           string  `json:"model_name"`
	State               string  `json:"state"`
	Reliability         float64 `json:"reliability"`
	ConsecutiveFailures int     `json:"consecutive_failures"`
	CooldownSeconds     float64 `json:"cooldown_seconds"`
	OpenUntil           string  `json:"open_until,omitempty"`
}

// ChannelHealth returns the aggregate circuit-breaker health view of every
// observed channel/model pair. It is the read endpoint for the channel health
// dashboard column.
func ChannelHealth(c *gin.Context) {
	snapshots := smartrouting.GetRuntimeHealthSnapshotAll()
	items := make([]channelHealthItem, 0, len(snapshots))
	for _, s := range snapshots {
		item := channelHealthItem{
			ChannelID:           s.ChannelID,
			ModelName:           s.ModelName,
			State:               string(s.State),
			Reliability:         s.Reliability,
			ConsecutiveFailures: s.ConsecutiveFailures,
			CooldownSeconds:     s.Cooldown.Seconds(),
		}
		if !s.OpenUntil.IsZero() {
			item.OpenUntil = s.OpenUntil.Format(time.RFC3339)
		}
		items = append(items, item)
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    items,
	})
}
