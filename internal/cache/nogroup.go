package cache

import "strings"

// IsNoGroup reports whether err is Redis saying the stream or the consumer group does not exist ("NOGROUP ..."). It happens when
// Redis is restarted without persistence, or the stream key is deleted, while a worker is running: the group the worker created
// at start-up is gone, and every read fails until the group is created again.
func IsNoGroup(err error) bool {
	return err != nil && strings.Contains(err.Error(), "NOGROUP")
}
